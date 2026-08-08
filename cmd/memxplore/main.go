package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/qingbo1011/memxplore/internal/buildinfo"
)

type versionOutput struct {
	Program       string `json:"program"`
	Version       string `json:"version"`
	Protocol      string `json:"protocol_version"`
	StorageSchema int    `json:"storage_schema_version"`
	ExportSchema  int    `json:"export_schema_version"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		printUsage(stdout)
		return 0
	}

	switch args[0] {
	case "version":
		return printVersion(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "memxplore: unknown command %q\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func printVersion(args []string, stdout, stderr io.Writer) int {
	output := versionOutput{
		Program:       "memxplore",
		Version:       buildinfo.Version,
		Protocol:      buildinfo.ProtocolVersion,
		StorageSchema: buildinfo.StorageSchemaVersion,
		ExportSchema:  buildinfo.ExportSchemaVersion,
	}

	if len(args) == 0 {
		fmt.Fprintf(stdout, "%s %s (protocol %s, storage schema %d, export schema %d)\n",
			output.Program, output.Version, output.Protocol, output.StorageSchema, output.ExportSchema)
		return 0
	}
	if len(args) == 1 && args[0] == "--json" {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(output); err != nil {
			fmt.Fprintf(stderr, "memxplore: encode version: %v\n", err)
			return 1
		}
		return 0
	}

	fmt.Fprintln(stderr, "usage: memxplore version [--json]")
	return 2
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "MemXplore - executable agent-memory reference implementation and research workbench")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  memxplore <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	fmt.Fprintln(w, "  version   Print program and schema versions")
	fmt.Fprintln(w, "  help      Show this help")
}
