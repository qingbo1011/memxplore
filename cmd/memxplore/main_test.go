package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestVersionJSON(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run([]string{"version", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run returned %d: %s", code, stderr.String())
	}

	var got versionOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if got.Program != "memxplore" || got.Protocol != "v1" || got.StorageSchema != 3 {
		t.Fatalf("unexpected version output: %+v", got)
	}
}

func TestUnknownCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run([]string{"nope"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr %q does not explain the failure", stderr.String())
	}
}
