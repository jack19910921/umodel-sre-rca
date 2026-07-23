package cc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jack/umodel-sre-rca/internal/domain"
)

type Runner struct {
	Binary                 string
	EvidenceCLI            string
	WorkingDir             string
	Timeout                time.Duration
	MaxTurns               int
	MaxCombinedOutputBytes int
}

const (
	defaultEvidenceCLIPath = "/opt/sre-rca/bin/sre-evidence"
	defaultWorkingDir      = "/opt/sre-rca/runtime"
	defaultTimeout         = 5 * time.Minute
	defaultMaxOutputBytes  = 64 << 10
)

func (r Runner) Run(ctx context.Context, incidentID string) (domain.RCAResult, error) {
	command, runCtx, cancel, err := r.prepareCommand(ctx, incidentID)
	if err != nil {
		return domain.RCAResult{}, err
	}
	defer cancel()
	output := newBoundedBuffer(r.MaxCombinedOutputBytes)
	command.Stdout = output
	command.Stderr = output
	err = command.Run()
	if err != nil {
		if runCtx.Err() != nil {
			err = runCtx.Err()
		}
		diagnostic := strings.TrimSpace(string(output.Bytes()))
		if diagnostic == "" {
			return domain.RCAResult{}, fmt.Errorf("run Claude Code RCA skill: %w", err)
		}
		return domain.RCAResult{}, fmt.Errorf("run Claude Code RCA skill: %w; output=%s", err, diagnostic)
	}
	return ParseResult(output.Bytes())
}

func (r Runner) prepareCommand(ctx context.Context, incidentID string) (*exec.Cmd, context.Context, context.CancelFunc, error) {
	if !isSafeIncidentID(incidentID) {
		return nil, nil, nil, fmt.Errorf("incident id is required and must be a safe reference")
	}
	workingDir := r.WorkingDir
	if workingDir == "" {
		workingDir = defaultWorkingDir
	}
	if !filepath.IsAbs(workingDir) {
		return nil, nil, nil, fmt.Errorf("RCA working directory must be absolute")
	}
	evidenceCLI := r.EvidenceCLI
	if evidenceCLI == "" {
		evidenceCLI = defaultEvidenceCLIPath
	}
	if evidenceCLI != defaultEvidenceCLIPath {
		return nil, nil, nil, fmt.Errorf("RCA evidence CLI must be fixed to %q", defaultEvidenceCLIPath)
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	binary := r.Binary
	if binary == "" {
		binary = "claude"
	}
	maxTurns := r.MaxTurns
	if maxTurns < 1 {
		maxTurns = 8
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	prepared := buildCommand(binary, evidenceCLI, incidentID, maxTurns)
	command := exec.CommandContext(runCtx, prepared.Path, prepared.Args[1:]...)
	command.Dir = workingDir
	return command, runCtx, cancel, nil
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

func buildCommand(binary, evidenceCLI, incidentID string, maxTurns int) *exec.Cmd {
	return exec.Command(binary,
		"-p", "/rca-investigate "+incidentID,
		"--output-format", "json",
		"--max-turns", strconv.Itoa(maxTurns),
		"--allowedTools", "Bash("+evidenceCLI+":*)",
	)
}

type boundedBuffer struct {
	buffer    bytes.Buffer
	limit     int
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
