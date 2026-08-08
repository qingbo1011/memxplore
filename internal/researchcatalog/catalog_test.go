package researchcatalog_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

type catalog struct {
	SchemaVersion int `json:"schema_version"`
	Snapshot      struct {
		ID           string `json:"id"`
		PublishedAt  string `json:"published_at"`
		ReviewStatus string `json:"review_status"`
	} `json:"snapshot"`
	Entries []entry `json:"entries"`
}

type entry struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	Implementation struct {
		Type     string `json:"type"`
		Fidelity string `json:"fidelity"`
	} `json:"implementation"`
	Upstream struct {
		Commit             *string `json:"commit"`
		License            *string `json:"license"`
		VerificationStatus string  `json:"verification_status"`
	} `json:"upstream"`
}

func TestCatalogPolicy(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source location")
	}
	path := filepath.Join(filepath.Dir(filename), "..", "..", "research", "catalog.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}

	var got catalog
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	if got.SchemaVersion != 1 {
		t.Fatalf("schema version = %d, want 1", got.SchemaVersion)
	}
	if got.Snapshot.ReviewStatus != "reviewed" {
		t.Fatalf("snapshot %q is not reviewed", got.Snapshot.ID)
	}
	if _, err := time.Parse(time.RFC3339, got.Snapshot.PublishedAt); err != nil {
		t.Fatalf("snapshot published_at: %v", err)
	}

	seen := make(map[string]struct{}, len(got.Entries))
	for _, item := range got.Entries {
		if item.ID == "" {
			t.Fatal("catalog entry has empty id")
		}
		if _, exists := seen[item.ID]; exists {
			t.Fatalf("duplicate catalog id %q", item.ID)
		}
		seen[item.ID] = struct{}{}

		if item.Implementation.Type == "reproduction" {
			if item.Implementation.Fidelity != "result-verified" {
				t.Errorf("reproduction %q is not result-verified", item.ID)
			}
			if item.Upstream.Commit == nil || item.Upstream.License == nil || item.Upstream.VerificationStatus != "verified" {
				t.Errorf("reproduction %q lacks verified upstream metadata", item.ID)
			}
		}
		if item.Status == "validated" && item.Implementation.Fidelity == "none" {
			t.Errorf("validated entry %q has no fidelity", item.ID)
		}
	}
	if len(seen) == 0 {
		t.Fatal("catalog is empty")
	}
}
