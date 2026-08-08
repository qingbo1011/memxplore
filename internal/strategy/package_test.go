package strategy

import (
	"encoding/json"
	"testing"
)

func testPackage() Package {
	return Package{
		ID: "formation.factual.generator-free", Version: "1.0.0", Implementation: "builtin",
		Fidelity: FidelityBaseline, Parameters: json.RawMessage(`{"threshold": 0.5, "mode":"exact"}`),
		Capabilities: []string{"factual", "formation"}, Repair: RepairPolicy{Strict: true},
		PaperSources: []string{"section-5", "section-3"},
	}
}

func TestHashCanonicalizesJSONAndSetFields(t *testing.T) {
	first := testPackage()
	second := testPackage()
	second.Parameters = json.RawMessage("{\n  \"mode\": \"exact\", \"threshold\": 0.5 }")
	second.Capabilities = []string{"formation", "factual", "formation"}
	second.PaperSources = []string{"section-3", "section-5"}
	firstHash, err := first.Hash()
	if err != nil {
		t.Fatal(err)
	}
	secondHash, err := second.Hash()
	if err != nil {
		t.Fatal(err)
	}
	if firstHash != secondHash {
		t.Fatalf("canonical hashes differ: %s != %s", firstHash, secondHash)
	}
}

func TestExperimentHashBindsRuntimeAndFixtures(t *testing.T) {
	pkg := testPackage()
	one, err := ExperimentHash(pkg, "fake", "model-a", []string{"b", "a"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := ExperimentHash(pkg, "fake", "model-a", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	three, err := ExperimentHash(pkg, "fake", "model-b", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if one != two || one == three {
		t.Fatalf("unexpected experiment hashes: %q %q %q", one, two, three)
	}
}
