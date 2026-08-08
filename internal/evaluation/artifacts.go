package evaluation

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var ErrRunExists = errors.New("evaluation run already exists")

// WriteRun creates one immutable run directory and never overwrites an existing artifact.
func WriteRun(root string, run Run) (path string, finalErr error) {
	if err := run.Validate(); err != nil {
		return "", err
	}
	if root == "" {
		return "", fmt.Errorf("run output root is required")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return "", fmt.Errorf("create run root: %w", err)
	}
	directory := filepath.Join(root, run.Manifest.RunID)
	if err := os.Mkdir(directory, 0o750); err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("%w: %s", ErrRunExists, run.Manifest.RunID)
		}
		return "", fmt.Errorf("create run directory: %w", err)
	}
	defer func() {
		if finalErr != nil {
			_ = os.RemoveAll(directory)
		}
	}()
	artifacts := []struct {
		name string
		data []byte
	}{
		{name: "predictions.jsonl", data: marshalJSONLines(run.Predictions)},
		{name: "metrics.json", data: marshalIndented(run.Metrics)},
		{name: "traces.jsonl", data: marshalJSONLines(run.Traces)},
	}
	var report bytes.Buffer
	if err := RenderReport(&report, run); err != nil {
		return "", fmt.Errorf("render report: %w", err)
	}
	artifacts = append(artifacts, struct {
		name string
		data []byte
	}{name: "report.html", data: report.Bytes()})
	run.Manifest.ArtifactSHA256 = make(map[string]string, len(artifacts))
	for _, artifact := range artifacts {
		if artifact.data == nil {
			return "", fmt.Errorf("marshal %s", artifact.name)
		}
		digest := sha256.Sum256(artifact.data)
		run.Manifest.ArtifactSHA256[artifact.name] = hex.EncodeToString(digest[:])
		if err := writeExclusive(filepath.Join(directory, artifact.name), artifact.data); err != nil {
			return "", err
		}
	}
	manifest := marshalIndented(run.Manifest)
	if manifest == nil {
		return "", fmt.Errorf("marshal manifest")
	}
	if err := writeExclusive(filepath.Join(directory, "manifest.json"), manifest); err != nil {
		return "", err
	}
	return directory, nil
}

// VerifyRun checks the manifest and every recorded artifact digest.
func VerifyRun(directory string) error {
	data, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	for name, expected := range manifest.ArtifactSHA256 {
		artifact, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return fmt.Errorf("read artifact %s: %w", name, err)
		}
		digest := sha256.Sum256(artifact)
		if actual := hex.EncodeToString(digest[:]); actual != expected {
			return fmt.Errorf("artifact %s digest %s does not match manifest %s", name, actual, expected)
		}
	}
	return nil
}

func writeExclusive(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o440)
	if err != nil {
		return fmt.Errorf("create artifact %s: %w", filepath.Base(path), err)
	}
	failed := true
	defer func() {
		_ = file.Close()
		if failed {
			_ = os.Remove(path)
		}
	}()
	writer := bufio.NewWriter(file)
	if _, err := writer.Write(data); err != nil {
		return fmt.Errorf("write artifact %s: %w", filepath.Base(path), err)
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush artifact %s: %w", filepath.Base(path), err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync artifact %s: %w", filepath.Base(path), err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close artifact %s: %w", filepath.Base(path), err)
	}
	failed = false
	return nil
}

func marshalIndented(value any) []byte {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil
	}
	return append(data, '\n')
}

func marshalJSONLines[T any](values []T) []byte {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	for _, value := range values {
		if err := encoder.Encode(value); err != nil {
			return nil
		}
	}
	return output.Bytes()
}

// SHA256Reader consumes a stream and returns its lowercase digest and byte count.
func SHA256Reader(reader io.Reader) (string, int64, error) {
	hash := sha256.New()
	count, err := io.Copy(hash, reader)
	if err != nil {
		return "", count, err
	}
	return hex.EncodeToString(hash.Sum(nil)), count, nil
}
