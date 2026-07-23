package cc

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jack/umodel-sre-rca/internal/domain"
)

// Runner executes the customer-hosted RCA analysis boundary. Its executable
// paths and working directory are immutable deployment values, rather than
// caller-controlled configuration.
type Runner struct {
	Timeout        time.Duration
	MaxTurns       int
	MaxOutputBytes int

	// commandFactory and workingDir exist only to make subprocess behavior
	// testable within this package. Production callers cannot configure a
	// binary, evidence path, or working directory through Runner.
	commandFactory commandFactory
	workingDir     string
}

type commandFactory func(context.Context, string, ...string) *exec.Cmd

const (
	defaultClaudeBinary     = "/usr/bin/claude"
	defaultEvidenceCLIPath  = "/opt/sre-rca/bin/sre-evidence"
	defaultWorkingDir       = "/opt/sre-rca/runtime"
	defaultTimeout          = 5 * time.Minute
	defaultMaxOutputBytes   = 64 << 10
	processGroupWaitTimeout = time.Second
)

var fixedEvidenceForms = [][]string{
	{"incident", "context"},
	{"metrics", "query"},
	{"logs", "query"},
	{"changes", "query"},
}

type evidenceCollection struct {
	Form     string            `json:"form"`
	Evidence []domain.Evidence `json:"evidence"`
}

func (r Runner) Run(ctx context.Context, incidentID string) (domain.RCAResult, error) {
	if !isSafeIncidentID(incidentID) {
		return domain.RCAResult{}, fmt.Errorf("incident id is required and must be a safe reference")
	}

	runCtx, cancel := context.WithTimeout(ctx, r.timeout())
	defer cancel()

	collections, err := r.collectEvidence(runCtx, incidentID)
	if err != nil {
		return domain.RCAResult{}, err
	}
	command, _, commandCancel, err := r.prepareClaudeCommand(runCtx, incidentID, collections)
	if err != nil {
		return domain.RCAResult{}, err
	}
	defer commandCancel()

	stdout := newBoundedBuffer(r.outputLimit())
	stderr := newBoundedBuffer(r.outputLimit())
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if runCtx.Err() != nil {
			err = runCtx.Err()
		}
		return domain.RCAResult{}, fmt.Errorf("run Claude Code RCA skill: %w (%s; %s)", err, stdout.describe("stdout"), stderr.describe("stderr"))
	}

	result, err := ParseResult(stdout.Bytes())
	if err != nil {
		return domain.RCAResult{}, fmt.Errorf("parse Claude Code RCA result: %w (%s; %s)", err, stdout.describe("stdout"), stderr.describe("stderr"))
	}
	return result, nil
}

func (r Runner) collectEvidence(ctx context.Context, incidentID string) ([]evidenceCollection, error) {
	collections := make([]evidenceCollection, 0, len(fixedEvidenceForms))
	for _, form := range fixedEvidenceForms {
		stdout := newBoundedBuffer(r.outputLimit())
		stderr := newBoundedBuffer(r.outputLimit())
		command := r.newCommand(ctx, defaultEvidenceCLIPath, append(append([]string{}, form...), incidentID)...)
		configureProcessGroup(command)
		command.Dir = r.runtimeWorkingDir()
		command.Stdout = stdout
		command.Stderr = stderr
		if err := command.Run(); err != nil {
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			return nil, fmt.Errorf("collect %s evidence: %w (%s; %s)", strings.Join(form, " "), err, stdout.describe("stdout"), stderr.describe("stderr"))
		}
		var envelope struct {
			Evidence []domain.Evidence `json:"evidence"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
			return nil, fmt.Errorf("decode %s evidence: %w (%s; %s)", strings.Join(form, " "), err, stdout.describe("stdout"), stderr.describe("stderr"))
		}
		collections = append(collections, evidenceCollection{Form: strings.Join(form, " "), Evidence: envelope.Evidence})
	}
	return collections, nil
}

func (r Runner) prepareClaudeCommand(ctx context.Context, incidentID string, collections []evidenceCollection) (*exec.Cmd, context.Context, context.CancelFunc, error) {
	if !isSafeIncidentID(incidentID) {
		return nil, nil, nil, fmt.Errorf("incident id is required and must be a safe reference")
	}
	prompt, err := buildPrompt(incidentID, collections)
	if err != nil {
		return nil, nil, nil, err
	}
	runCtx, cancel := context.WithTimeout(ctx, r.timeout())
	command := r.newCommand(runCtx, defaultClaudeBinary,
		"-p", prompt,
		"--output-format", "json",
		"--max-turns", strconv.Itoa(r.maxTurns()),
	)
	configureProcessGroup(command)
	command.Dir = r.runtimeWorkingDir()
	return command, runCtx, cancel, nil
}

func buildPrompt(incidentID string, collections []evidenceCollection) (string, error) {
	if !isSafeIncidentID(incidentID) {
		return "", fmt.Errorf("incident id is required and must be a safe reference")
	}
	if len(collections) != len(fixedEvidenceForms) {
		return "", fmt.Errorf("RCA requires all fixed evidence collections")
	}
	for index, collection := range collections {
		if collection.Form != strings.Join(fixedEvidenceForms[index], " ") {
			return "", fmt.Errorf("RCA evidence collection %d is not an allowed fixed form", index)
		}
	}
	payload, err := json.Marshal(struct {
		IncidentID string               `json:"incident_id"`
		Evidence   []evidenceCollection `json:"evidence"`
	}{IncidentID: incidentID, Evidence: collections})
	if err != nil {
		return "", fmt.Errorf("encode fixed RCA evidence: %w", err)
	}
	return "/rca-investigate\nAnalyze only the supplied fixed evidence JSON. Do not request or use any other tools, commands, URLs, APIs, filesystem data, credentials, or network access.\n" + string(payload), nil
}

func (r Runner) newCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	if r.commandFactory != nil {
		return r.commandFactory(ctx, name, args...)
	}
	return exec.CommandContext(ctx, name, args...)
}

func (r Runner) runtimeWorkingDir() string {
	if r.workingDir != "" {
		return r.workingDir
	}
	return defaultWorkingDir
}

func (r Runner) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return defaultTimeout
}

func (r Runner) maxTurns() int {
	if r.MaxTurns > 0 {
		return r.MaxTurns
	}
	return 8
}

func (r Runner) outputLimit() int {
	if r.MaxOutputBytes > 0 {
		return r.MaxOutputBytes
	}
	return defaultMaxOutputBytes
}

func configureProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	command.WaitDelay = processGroupWaitTimeout
}

func isSafeIncidentID(value string) bool {
	if strings.TrimSpace(value) == "" {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' || char == ':' {
			continue
		}
		return false
	}
	return true
}

type boundedBuffer struct {
	buffer    bytes.Buffer
	limit     int
	total     int
	truncated bool
}

func newBoundedBuffer(limit int) *boundedBuffer {
	if limit <= 0 {
		limit = defaultMaxOutputBytes
	}
	return &boundedBuffer{limit: limit}
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	b.total += written
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.truncated = true
		return written, nil
	}
	if len(p) > remaining {
		b.truncated = true
		p = p[:remaining]
	}
	_, err := b.buffer.Write(p)
	return written, err
}

func (b *boundedBuffer) Bytes() []byte { return b.buffer.Bytes() }

func (b *boundedBuffer) Truncated() bool { return b.truncated }

func (b *boundedBuffer) describe(name string) string {
	digest := sha256.Sum256(b.buffer.Bytes())
	return fmt.Sprintf("%s_bytes=%d %s_sha256=%s truncated=%t", name, b.total, name, hex.EncodeToString(digest[:8]), b.truncated)
}

var _ io.Writer = (*boundedBuffer)(nil)

func ParseResult(raw []byte) (domain.RCAResult, error) {
	var result domain.RCAResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return domain.RCAResult{}, fmt.Errorf("decode RCA result: %w", err)
	}
	if result.Summary == "" {
		var envelope struct {
			Result string `json:"result"`
		}
		if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Result != "" {
			if err := json.Unmarshal([]byte(envelope.Result), &result); err != nil {
				return domain.RCAResult{}, fmt.Errorf("decode RCA result envelope: %w", err)
			}
		}
	}
	if err := ValidateResult(result, nil); err != nil {
		return domain.RCAResult{}, err
	}
	return result, nil
}

func ValidateResult(result domain.RCAResult, knownEvidence map[string]bool) error {
	if strings.TrimSpace(result.Summary) == "" || strings.TrimSpace(result.RootCause) == "" {
		return fmt.Errorf("RCA result summary and root_cause are required")
	}
	if result.Confidence < 0 || result.Confidence > 1 {
		return fmt.Errorf("RCA confidence must be within [0,1]")
	}
	if len(result.EvidenceIDs) == 0 {
		return fmt.Errorf("RCA result requires at least one evidence id")
	}
	for _, id := range result.EvidenceIDs {
		if id == "" {
			return fmt.Errorf("RCA evidence id must not be empty")
		}
		if knownEvidence != nil && !knownEvidence[id] {
			return fmt.Errorf("RCA result references unknown evidence %q", id)
		}
	}
	return nil
}
