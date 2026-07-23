package cc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunnerBuildsFixedCommandWithoutBash(t *testing.T) {
	runner := Runner{}
	cmd, _, cancel, err := runner.prepareClaudeCommand(context.Background(), "inc-1", fixedCollections())
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if cmd.Path != defaultClaudeBinary {
		t.Fatalf("path=%q want=%q", cmd.Path, defaultClaudeBinary)
	}
	if cmd.Dir != defaultWorkingDir {
		t.Fatalf("dir=%q want=%q", cmd.Dir, defaultWorkingDir)
	}
	args := strings.Join(cmd.Args, " ")
	if strings.Contains(args, "allowedTools") || strings.Contains(args, "Bash(") {
		t.Fatalf("unsafe command args=%q", cmd.Args)
	}
	for _, form := range fixedEvidenceForms {
		if !strings.Contains(args, strings.Join(form, " ")) {
			t.Fatalf("missing fixed evidence form %q in prompt=%q", form, args)
		}
	}
}

func TestRunnerProductionCommandCannotBeRedirected(t *testing.T) {
	runner := Runner{}
	cmd, _, cancel, err := runner.prepareClaudeCommand(context.Background(), "inc-1", fixedCollections())
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	if cmd.Path != defaultClaudeBinary || cmd.Dir != defaultWorkingDir {
		t.Fatalf("command=%#v", cmd)
	}
}

func TestRunnerRejectsUnsafeIncidentReference(t *testing.T) {
	_, err := (Runner{}).Run(context.Background(), "inc-1;touch /tmp/pwned")
	if err == nil || !strings.Contains(err.Error(), "safe reference") {
		t.Fatalf("err=%v", err)
	}
}

func TestRunnerKeepsStderrOutOfValidJSON(t *testing.T) {
	runner := Runner{commandFactory: helperCommandFactory("valid-with-stderr", ""), workingDir: t.TempDir()}
	result, err := runner.Run(context.Background(), "inc-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "valid RCA" {
		t.Fatalf("result=%#v", result)
	}
}

func TestRunnerFailureDoesNotExposeRawOutput(t *testing.T) {
	runner := Runner{commandFactory: helperCommandFactory("failing", ""), workingDir: t.TempDir()}
	_, err := runner.Run(context.Background(), "inc-1")
	if err == nil {
		t.Fatal("expected error")
	}
	message := err.Error()
	if strings.Contains(message, "SECRET_STDOUT") || strings.Contains(message, "SECRET_STDERR") {
		t.Fatalf("raw model output leaked: %q", message)
	}
	if !strings.Contains(message, "stdout_bytes=") || !strings.Contains(message, "stderr_bytes=") {
		t.Fatalf("missing bounded diagnostics: %q", message)
	}
}

func TestRunnerTimeoutKillsProcessGroupDescendants(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	runner := Runner{
		Timeout:        2 * time.Second,
		commandFactory: helperCommandFactory("spawn-child", pidFile),
		workingDir:     t.TempDir(),
	}
	_, err := runner.Run(context.Background(), "inc-1")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v", err)
	}
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	var pid int
	if _, err := fmt.Sscanf(string(pidBytes), "%d", &pid); err != nil {
		t.Fatalf("parse child pid: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		err = syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("descendant pid %d survived process-group cancellation: %v", pid, err)
		}
		time.Sleep(10 * time.Millisecond)
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

func helperCommandFactory(mode, pidFile string) commandFactory {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name != defaultClaudeBinary {
			return exec.CommandContext(ctx, "/bin/echo", `{"evidence":[{"id":"ev-context","type":"context","observed_at":"2026-01-01T00:00:00Z","source":"test","query_ref":"test","summary":"safe","raw_ref":""}]}`)
		}
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=TestRunnerHelperProcess")
		command.Env = append(os.Environ(), "GO_WANT_RCA_HELPER=1", "RCA_HELPER_MODE="+mode, "RCA_HELPER_PID_FILE="+pidFile)
		return command
	}
}

func fixedCollections() []evidenceCollection {
	collections := make([]evidenceCollection, 0, len(fixedEvidenceForms))
	for _, form := range fixedEvidenceForms {
		collections = append(collections, evidenceCollection{Form: strings.Join(form, " ")})
	}
	return collections
}

func TestRunnerHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_RCA_HELPER") != "1" {
		return
	}
	mode := os.Getenv("RCA_HELPER_MODE")
	switch mode {
	case "valid-with-stderr":
		_, _ = os.Stderr.WriteString("benign diagnostics\n")
		_, _ = os.Stdout.WriteString(`{"summary":"valid RCA","confidence":0.9,"root_cause":"test","evidence_ids":["ev-context"],"next_actions":["review"]}`)
	case "failing":
		_, _ = os.Stdout.WriteString("SECRET_STDOUT")
		_, _ = os.Stderr.WriteString("SECRET_STDERR")
		os.Exit(1)
	case "spawn-child":
		pidFile := os.Getenv("RCA_HELPER_PID_FILE")
		if pidFile == "" {
			os.Exit(2)
		}
		child := exec.Command(os.Args[0], "-test.run=TestRunnerHelperProcess")
		child.Env = append(os.Environ(), "GO_WANT_RCA_HELPER=1", "RCA_HELPER_MODE=block")
		if err := child.Start(); err != nil {
			os.Exit(3)
		}
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(child.Process.Pid)), 0600); err != nil {
			os.Exit(4)
		}
		for {
			time.Sleep(time.Hour)
		}
	case "block":
		for {
			time.Sleep(time.Hour)
		}
	default:
		os.Exit(5)
	}
	os.Exit(0)
}
