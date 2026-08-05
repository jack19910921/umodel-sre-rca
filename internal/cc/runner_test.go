package cc

import (
	"context"
	"encoding/base64"
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

	"github.com/jack/umodel-sre-rca/internal/domain"
)

func TestRunnerBuildsToolFreeFixedCommandWithMinimalEnvironment(t *testing.T) {
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
	if !containsArgumentPair(cmd.Args, "--tools", "") || !containsArgument(cmd.Args, "--no-session-persistence") {
		t.Fatalf("Claude command must disable tools and session persistence: %q", cmd.Args)
	}
	if strings.Contains(args, "allowedTools") || strings.Contains(args, "Bash(") || strings.Contains(strings.ToLower(args), "mcp") {
		t.Fatalf("unsafe command args=%q", cmd.Args)
	}
	if got, want := cmd.Env, minimalChildEnvironment(); !equalStrings(got, want) {
		t.Fatalf("Claude env=%q want=%q", got, want)
	}
	for _, form := range fixedEvidenceForms {
		if !strings.Contains(args, strings.Join(form, " ")) {
			t.Fatalf("missing fixed evidence form %q in prompt=%q", form, args)
		}
	}
}

func TestRunnerGivesFixedEvidenceChildrenTheMinimalEnvironment(t *testing.T) {
	runner := Runner{}
	cmd, err := runner.prepareEvidenceCommand(context.Background(), []string{"incident", "context"}, "inc-1")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != defaultEvidenceCLIPath {
		t.Fatalf("path=%q want=%q", cmd.Path, defaultEvidenceCLIPath)
	}
	if got, want := cmd.Env, minimalChildEnvironment(); !equalStrings(got, want) {
		t.Fatalf("evidence env=%q want=%q", got, want)
	}
}

func containsArgument(args []string, expected string) bool {
	for _, arg := range args {
		if arg == expected {
			return true
		}
	}
	return false
}

func containsArgumentPair(args []string, first, second string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == first && args[index+1] == second {
			return true
		}
	}
	return false
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
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
	_, err := (Runner{}).Run(context.Background(), "inc-1;touch /tmp/pwned", fixedCollections())
	if err == nil || !strings.Contains(err.Error(), "safe reference") {
		t.Fatalf("err=%v", err)
	}
}

func TestRunnerKeepsStderrOutOfValidJSON(t *testing.T) {
	runner := Runner{commandFactory: helperCommandFactory("valid-with-stderr", ""), workingDir: t.TempDir()}
	result, err := runner.Run(context.Background(), "inc-1", fixedCollections())
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "有效 RCA" {
		t.Fatalf("result=%#v", result)
	}
}

func TestRunnerUsesCallerEvidenceWithoutRequerying(t *testing.T) {
	var commands []string
	runner := Runner{
		commandFactory: func(ctx context.Context, name string, _ ...string) *exec.Cmd {
			commands = append(commands, name)
			return exec.CommandContext(ctx, "/bin/echo", `{"summary":"有效 RCA","confidence":0.9,"root_cause":"测试根因","evidence_ids":["ev-context"],"next_actions":["检查配置"]}`)
		},
		workingDir: t.TempDir(),
	}
	collections := fixedCollections()
	collections[0].Evidence = []domain.Evidence{{ID: "ev-context"}}

	result, err := runner.Run(context.Background(), "inc-1", collections)
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "有效 RCA" {
		t.Fatalf("result=%#v", result)
	}
	if got, want := commands, []string{defaultClaudeBinary}; !equalStrings(got, want) {
		t.Fatalf("commands=%q want=%q", got, want)
	}
}

func TestRunnerFailureDoesNotExposeRawOutput(t *testing.T) {
	runner := Runner{commandFactory: helperCommandFactory("failing", ""), workingDir: t.TempDir()}
	_, err := runner.Run(context.Background(), "inc-1", fixedCollections())
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
	_, err := runner.Run(context.Background(), "inc-1", fixedCollections())
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
	if _, err := ParseResult(raw); err == nil || !strings.Contains(err.Error(), "evidence") {
		t.Fatalf("err=%v, want missing evidence validation", err)
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

func TestParseResultRejectsClaudeJSONEnvelopeWithFencedResult(t *testing.T) {
	raw := []byte("{\"result\":\"```json\\n{\\\"summary\\\":\\\"valid RCA\\\",\\\"confidence\\\":0.9,\\\"root_cause\\\":\\\"test\\\",\\\"evidence_ids\\\":[\\\"ev-context\\\"],\\\"next_actions\\\":[\\\"review\\\"]}\\n```\"}")

	if _, err := ParseResult(raw); err == nil {
		t.Fatal("ParseResult() error = nil, want fenced JSON rejection")
	}
}

func TestValidateResultRejectsEnglishCustomerFacingFields(t *testing.T) {
	err := ValidateResult(domain.RCAResult{
		Summary:     "security group removed TCP/80",
		RootCause:   "security group rule revoked",
		Confidence:  0.9,
		EvidenceIDs: []string{"ev-context"},
		NextActions: []string{"restore TCP/80"},
	}, nil)
	if err == nil {
		t.Fatal("ValidateResult() error = nil, want non-Chinese customer-facing fields rejection")
	}
}

func TestBuildPromptExplicitlyRestrictsEvidenceIDsToSuppliedEvidence(t *testing.T) {
	collections := fixedCollections()
	collections[0].Evidence = []domain.Evidence{{ID: "ev-context"}}
	prompt, err := buildPrompt("inc-1", collections)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "must be a non-empty subset of the exact IDs in ALLOWED_EVIDENCE_IDS") {
		t.Fatalf("prompt does not constrain evidence IDs: %q", prompt)
	}
	if !strings.Contains(prompt, `"ev-context"`) {
		t.Fatalf("prompt does not list the supplied evidence ID: %q", prompt)
	}
}

func TestBuildPromptRequiresSimplifiedChineseRCAValues(t *testing.T) {
	prompt, err := buildPrompt("inc-1", fixedCollections())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "Use Simplified Chinese for every human-readable RCA value") {
		t.Fatalf("prompt does not require Simplified Chinese: %q", prompt)
	}
}

func helperCommandFactory(mode, pidFile string) commandFactory {
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if name != defaultClaudeBinary {
			return exec.CommandContext(ctx, "/bin/echo", `{"evidence":[{"id":"ev-context","type":"context","observed_at":"2026-01-01T00:00:00Z","source":"test","query_ref":"test","summary":"safe","raw_ref":""}]}`)
		}
		return exec.CommandContext(ctx, os.Args[0], "-test.run=TestRunnerHelperProcess/"+mode+"/"+base64.RawURLEncoding.EncodeToString([]byte(pidFile)))
	}
}

func fixedCollections() []EvidenceCollection {
	collections := make([]EvidenceCollection, 0, len(fixedEvidenceForms))
	for _, form := range fixedEvidenceForms {
		collections = append(collections, EvidenceCollection{Form: strings.Join(form, " ")})
	}
	return collections
}

func TestRunnerHelperProcess(t *testing.T) {
	mode, pidFile, ok := helperMode(os.Args)
	if !ok {
		return
	}
	switch mode {
	case "valid-with-stderr":
		_, _ = os.Stderr.WriteString("benign diagnostics\n")
		_, _ = os.Stdout.WriteString(`{"summary":"有效 RCA","confidence":0.9,"root_cause":"测试根因","evidence_ids":["ev-context"],"next_actions":["检查配置"]}`)
	case "failing":
		_, _ = os.Stdout.WriteString("SECRET_STDOUT")
		_, _ = os.Stderr.WriteString("SECRET_STDERR")
		os.Exit(1)
	case "spawn-child":
		if pidFile == "" {
			os.Exit(2)
		}
		child := exec.Command(os.Args[0], "-test.run=TestRunnerHelperProcess/block/")
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

func helperMode(args []string) (string, string, bool) {
	const prefix = "-test.run=TestRunnerHelperProcess/"
	for _, arg := range args {
		if !strings.HasPrefix(arg, prefix) {
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(arg, prefix), "/", 2)
		if len(parts) != 2 || parts[0] == "" {
			return "", "", false
		}
		pidFile, err := base64.RawURLEncoding.DecodeString(parts[1])
		if err != nil {
			return "", "", false
		}
		return parts[0], string(pidFile), true
	}
	return "", "", false
}
