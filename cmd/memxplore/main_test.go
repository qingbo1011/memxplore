package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	if got.Program != "memxplore" || got.Version != "0.1.0" || got.Protocol != "v1" || got.StorageSchema != 4 || got.ExportSchema != 1 {
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

func TestTokenCreateReturnsRawTokenOnce(t *testing.T) {
	var stdout, stderr bytes.Buffer
	database := filepath.Join(t.TempDir(), "token.sqlite")
	code := runContext(context.Background(), []string{
		"token", "create", "--db", database, "--id", "token-test", "--principal", "reader",
		"--owners", "owner-a", "--scopes", "memory:read",
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var response struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != "token-test" || !strings.HasPrefix(response.Token, "mx_") || len(response.Token) != 67 {
		t.Fatalf("response=%+v", response)
	}
}

func TestServeRefusesNonLoopbackWithoutToken(t *testing.T) {
	var stdout, stderr bytes.Buffer
	database := filepath.Join(t.TempDir(), "serve.sqlite")
	code := runContext(context.Background(), []string{
		"serve", "--db", database, "--listen", "0.0.0.0:7878",
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "refusing non-loopback") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestMCPCommandServesStdio(t *testing.T) {
	var stdout, stderr bytes.Buffer
	database := filepath.Join(t.TempDir(), "mcp.sqlite")
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}` + "\n")
	code := runContext(context.Background(), []string{"mcp", "--db", database}, input, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "memxplore_recall") || strings.Contains(stdout.String(), "listening") {
		t.Fatalf("stdout=%s", stdout.String())
	}
}

func TestPurgeRequiresExplicitConfirmation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runContext(context.Background(), []string{"purge", "--id", "memory-a"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "irreversible") {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
}

func TestBenchmarkInternalWritesAndVerifiesImmutableRun(t *testing.T) {
	var stdout, stderr bytes.Buffer
	output := t.TempDir()
	code := runContext(context.Background(), []string{
		"benchmark", "internal", "--output", output, "--run-id", "cli-internal-test", "--seed", "17",
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var result struct {
		RunID string `json:"run_id"`
		Path  string `json:"path"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.RunID != "cli-internal-test" || result.Path != filepath.Join(output, result.RunID) {
		t.Fatalf("result=%+v", result)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"eval", "verify", "--run", result.Path}, &stdout, &stderr); code != 0 {
		t.Fatalf("verify code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"valid": true`) {
		t.Fatalf("stdout=%s", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"benchmark", "internal", "--output", output, "--run-id", "cli-internal-test"}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "already exists") {
		t.Fatalf("duplicate code=%d stderr=%s", code, stderr.String())
	}
}

func TestDataExportValidateAndDryRunImport(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.sqlite")
	exportPath := filepath.Join(directory, "subject.json")
	var stdout, stderr bytes.Buffer
	code := runContext(context.Background(), []string{
		"data", "export", "--db", source, "--subject", "subject-a", "--output", exportPath,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("export code=%d stderr=%s", code, stderr.String())
	}
	var metadata struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Path != exportPath || len(metadata.SHA256) != 64 {
		t.Fatalf("export metadata=%+v", metadata)
	}
	info, err := os.Stat(exportPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("export permissions=%o, want 600", info.Mode().Perm())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"data", "validate", "--input", exportPath}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), `"valid": true`) {
		t.Fatalf("validate code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	target := filepath.Join(directory, "target.sqlite")
	if code := run([]string{"data", "import", "--db", target, "--input", exportPath, "--dry-run"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), `"dry_run": true`) {
		t.Fatalf("dry-run code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"data", "export", "--db", source, "--subject", "subject-a", "--output", exportPath}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "file exists") {
		t.Fatalf("overwrite code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestListenIsLoopback(t *testing.T) {
	for _, address := range []string{"127.0.0.1:7878", "[::1]:7878", "localhost:7878"} {
		if !listenIsLoopback(address) {
			t.Fatalf("%s should be loopback", address)
		}
	}
	for _, address := range []string{"0.0.0.0:7878", "[::]:7878", "bad"} {
		if listenIsLoopback(address) {
			t.Fatalf("%s should not be loopback", address)
		}
	}
}

func TestProviderURLIsLoopback(t *testing.T) {
	for _, endpoint := range []string{"http://127.0.0.1:11434/v1", "http://localhost:11434/v1", "http://[::1]:11434/v1"} {
		if !providerURLIsLoopback(endpoint) {
			t.Fatalf("%s should be loopback", endpoint)
		}
	}
	for _, endpoint := range []string{"https://api.example.com/v1", "file:///tmp/provider", "not-a-url"} {
		if providerURLIsLoopback(endpoint) {
			t.Fatalf("%s should not be loopback", endpoint)
		}
	}
}

func TestOllamaNativeURL(t *testing.T) {
	for input, want := range map[string]string{
		"http://127.0.0.1:11434/v1":         "http://127.0.0.1:11434",
		"http://localhost:11434/prefix/v1/": "http://localhost:11434/prefix",
	} {
		got, err := ollamaNativeURL(input)
		if err != nil || got != want {
			t.Fatalf("ollamaNativeURL(%q)=%q, %v, want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"", "http://127.0.0.1:11434", "file:///v1"} {
		if _, err := ollamaNativeURL(input); err == nil {
			t.Fatalf("ollamaNativeURL(%q) succeeded", input)
		}
	}
}
