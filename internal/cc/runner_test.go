package cc

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
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

func TestRunnerSetsFixedWorkingDirectoryAndTimeout(t *testing.T) {
	dir := t.TempDir()
	runner := Runner{Binary: "claude", EvidenceCLI: "/opt/sre-rca/bin/sre-evidence", WorkingDir: dir, Timeout: time.Second, MaxTurns: 3}
	cmd, commandCtx, cancel, err := runner.prepareCommand(context.Background(), "inc-1")
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if cmd.Dir != dir {
		t.Fatalf("working directory=%q want=%q", cmd.Dir, dir)
	}
	if !strings.Contains(strings.Join(cmd.Args, " "), "Bash(/opt/sre-rca/bin/sre-evidence:*)") {
		t.Fatalf("args=%q", cmd.Args)
	}
	if _, ok := commandCtx.Deadline(); !ok {
		t.Fatal("command context is missing timeout deadline")
	}
}

func TestRunnerRejectsRelativeWorkingDirectory(t *testing.T) {
	_, _, cancel, err := (Runner{WorkingDir: "relative", EvidenceCLI: "/opt/sre-rca/bin/sre-evidence"}).prepareCommand(context.Background(), "inc-1")
	if cancel != nil {
		defer cancel()
	}
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("err=%v", err)
	}
}

func TestRunnerRejectsUnsafeIncidentReference(t *testing.T) {
	_, _, cancel, err := (Runner{WorkingDir: t.TempDir(), EvidenceCLI: "/opt/sre-rca/bin/sre-evidence"}).prepareCommand(context.Background(), "https://untrusted.example")
	if cancel != nil {
		defer cancel()
	}
	if err == nil || !strings.Contains(err.Error(), "safe reference") {
		t.Fatalf("err=%v", err)
	}
}

func TestBoundedCombinedOutputStopsAtLimit(t *testing.T) {
	buf := newBoundedBuffer(4)
	if _, err := buf.Write([]byte("abcdef")); err != nil {
		t.Fatal(err)
	}
	if got := buf.Bytes(); string(got) != "abcd" {
		t.Fatalf("output=%q", got)
	}
	if !buf.Truncated() {
		t.Fatal("want truncated output")
	}
}

func TestRunnerReturnsBoundedDiagnosticsOnCancellation(t *testing.T) {
	dir := t.TempDir()
	binary := dir + "/slow-claude"
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho 1234567890 >&2\nsleep 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	runner := Runner{Binary: binary, EvidenceCLI: "/opt/sre-rca/bin/sre-evidence", WorkingDir: dir, Timeout: 10 * time.Millisecond, MaxCombinedOutputBytes: 4}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := runner.Run(ctx, "inc-1")
	if err == nil || !strings.Contains(err.Error(), "RCA skill") {
		t.Fatalf("err=%v", err)
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
