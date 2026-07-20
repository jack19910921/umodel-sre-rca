package cc

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/jack/umodel-sre-rca/internal/domain"
)

type Runner struct {
	Binary      string
	EvidenceCLI string
	MaxTurns    int
}

func (r Runner) Run(ctx context.Context, incidentID string) (domain.RCAResult, error) {
	if incidentID == "" {
		return domain.RCAResult{}, fmt.Errorf("incident id is required")
	}
	binary := r.Binary
	if binary == "" {
		binary = "claude"
	}
	evidenceCLI := r.EvidenceCLI
	if evidenceCLI == "" {
		evidenceCLI = "/opt/sre-rca/bin/sre-evidence"
	}
	maxTurns := r.MaxTurns
	if maxTurns < 1 {
		maxTurns = 8
	}
	prepared := buildCommand(binary, evidenceCLI, incidentID, maxTurns)
	command := exec.CommandContext(ctx, prepared.Path, prepared.Args[1:]...)
	output, err := command.Output()
	if err != nil {
		return domain.RCAResult{}, fmt.Errorf("run Claude Code RCA skill: %w", err)
	}
	return ParseResult(output)
}

func buildCommand(binary, evidenceCLI, incidentID string, maxTurns int) *exec.Cmd {
	return exec.Command(binary,
		"-p", "/rca-investigate "+incidentID,
		"--output-format", "json",
		"--max-turns", strconv.Itoa(maxTurns),
		"--allowedTools", "Bash("+evidenceCLI+":*)",
	)
}

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
