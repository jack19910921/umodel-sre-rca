package cc

import (
	"os"
	"strings"
	"testing"
)

func TestRunnerAllowsOnlyEvidenceCLI(t *testing.T) {
	cmd := buildCommand("claude", "/opt/sre-rca/bin/sre-evidence", "inc-1", 8)
	if !strings.Contains(strings.Join(cmd.Args, " "), "Bash(/opt/sre-rca/bin/sre-evidence:*)") {
		t.Fatalf("args=%q", cmd.Args)
	}
	if strings.Contains(strings.Join(cmd.Args, " "), "dangerously-skip-permissions") {
		t.Fatalf("unsafe args=%q", cmd.Args)
	}
}

func TestParseResultRejectsMissingEvidence(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/rca-result-invalid.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseResult(raw); err == nil {
		t.Fatal("wanted validation error")
	}
}

func TestParseResultAcceptsValidEvidence(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/rca-result-valid.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseResult(raw)
	if err != nil || got.Confidence != 0.93 || len(got.EvidenceIDs) != 2 {
		t.Fatalf("got=%#v err=%v", got, err)
	}
}
