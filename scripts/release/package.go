//go:build ignore

package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "package release asset: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	input := flag.String("input", "", "directory to package")
	output := flag.String("output", "", "new archive path")
	format := flag.String("format", "", "tar.gz or zip")
	epoch := flag.Int64("epoch", 0, "archive timestamp as Unix seconds")
	flag.Parse()
	if *input == "" || *output == "" || (*format != "tar.gz" && *format != "zip") || *epoch < 1 {
		return fmt.Errorf("input, output, format, and positive epoch are required")
	}
	if _, err := os.Stat(*output); err == nil {
		return fmt.Errorf("output already exists: %s", *output)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat output: %w", err)
	}
	files, err := regularFiles(*input)
	if err != nil {
		return fmt.Errorf("list input: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("input contains no regular files")
	}
	archive, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	failed := true
	defer func() {
		_ = archive.Close()
		if failed {
			_ = os.Remove(*output)
		}
	}()
	stamp := time.Unix(*epoch, 0).UTC()
	if *format == "tar.gz" {
		err = writeTarGzip(archive, *input, files, stamp)
	} else {
		err = writeZip(archive, *input, files, stamp)
	}
	if err != nil {
		return fmt.Errorf("write archive: %w", err)
	}
	if err := archive.Sync(); err != nil {
		return fmt.Errorf("sync archive: %w", err)
	}
	if err := archive.Close(); err != nil {
		return fmt.Errorf("close archive: %w", err)
	}
	failed = false
	return nil
}

func regularFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func writeTarGzip(output io.Writer, root string, files []string, stamp time.Time) error {
	gzipWriter, err := gzip.NewWriterLevel(output, gzip.BestCompression)
	if err != nil {
		return err
	}
	gzipWriter.Header.ModTime = stamp
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	for _, path := range files {
		name, mode, err := archiveMetadata(root, path)
		if err != nil {
			return err
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		header := &tar.Header{
			Name: name, Mode: mode, Size: info.Size(), ModTime: stamp,
			AccessTime: stamp, ChangeTime: stamp, Uid: 0, Gid: 0,
			Uname: "", Gname: "", Format: tar.FormatPAX,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if err := copyFile(tarWriter, path); err != nil {
			return err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return err
	}
	return gzipWriter.Close()
}

func writeZip(output io.Writer, root string, files []string, stamp time.Time) error {
	writer := zip.NewWriter(output)
	for _, path := range files {
		name, mode, err := archiveMetadata(root, path)
		if err != nil {
			return err
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = name
		header.Method = zip.Deflate
		header.Modified = stamp
		header.SetMode(fs.FileMode(mode))
		destination, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		if err := copyFile(destination, path); err != nil {
			return err
		}
	}
	return writer.Close()
}

func archiveMetadata(root, path string) (string, int64, error) {
	relative, err := filepath.Rel(filepath.Dir(root), path)
	if err != nil {
		return "", 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, err
	}
	mode := int64(0o644)
	if info.Mode().Perm()&0o111 != 0 {
		mode = 0o755
	}
	return filepath.ToSlash(relative), mode, nil
}

func copyFile(destination io.Writer, path string) error {
	source, err := os.Open(path)
	if err != nil {
		return err
	}
	defer source.Close()
	_, err = io.Copy(destination, source)
	return err
}
