package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jack/umodel-sre-rca/internal/config"
	"github.com/jack/umodel-sre-rca/internal/domain"
	"github.com/jack/umodel-sre-rca/internal/runtime"
	"github.com/jack/umodel-sre-rca/internal/store"
)

const defaultConfigPath = "/etc/sre-rca/sre.yaml"

type commandService interface {
	Context(context.Context, string) ([]domain.Evidence, error)
	Metrics(context.Context, string) ([]domain.Evidence, error)
	Logs(context.Context, string) ([]domain.Evidence, error)
	Changes(context.Context, string) ([]domain.Evidence, error)
	Open(context.Context, string) (domain.Evidence, error)
}

// stateRepository is the deliberately small local persistence boundary needed
// to construct the customer-hosted evidence service. It does not expose raw
// SQL or any remote credential/configuration surface to the CLI.
type stateRepository interface {
	Close() error
	IncidentByID(context.Context, string) (domain.Incident, error)
	OpenEvidence(context.Context, string) (domain.Evidence, error)
}

var (
	loadConfig           = config.Load
	openStateDB          = func(path string) (stateRepository, error) { return store.Open(path) }
	newConfiguredService = func(cfg config.Config, repo stateRepository) (commandService, error) {
		return runtime.NewEvidenceService(cfg, repo)
	}
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, nil))
}

func run(args []string, stdout, stderr io.Writer, service commandService) int {
	configPath, command, ok := parseCommand(args)
	if !ok {
		fmt.Fprintln(stderr, "usage: sre-evidence {incident context|metrics query|logs query|changes query|evidence open} <id>")
		return 2
	}
	if service == nil {
		cfg, err := loadConfig(configPath)
		if err != nil {
			fmt.Fprintf(stderr, "load evidence config: %v\n", err)
			return 1
		}
		repo, err := openStateDB(cfg.StateDB)
		if err != nil {
			fmt.Fprintf(stderr, "open evidence state DB: %v\n", err)
			return 1
		}
		defer repo.Close()
		service, err = newConfiguredService(cfg, repo)
		if err != nil {
			fmt.Fprintf(stderr, "compose evidence service: %v\n", err)
			return 1
		}
	}

	var items []domain.Evidence
	var err error
	incidentID := command[len(command)-1]
	switch command[0] {
	case "incident":
		items, err = service.Context(context.Background(), incidentID)
	case "metrics":
		items, err = service.Metrics(context.Background(), incidentID)
	case "logs":
		items, err = service.Logs(context.Background(), incidentID)
	case "changes":
		items, err = service.Changes(context.Background(), incidentID)
	case "evidence":
		item, openErr := service.Open(context.Background(), incidentID)
		items, err = []domain.Evidence{item}, openErr
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(map[string]any{"evidence": items}); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func isFixedCommand(args []string) bool {
	if len(args) != 3 || !isSafeReference(args[2]) {
		return false
	}
	return (args[0] == "incident" && args[1] == "context") ||
		(args[0] == "metrics" && args[1] == "query") ||
		(args[0] == "logs" && args[1] == "query") ||
		(args[0] == "changes" && args[1] == "query") ||
		(args[0] == "evidence" && args[1] == "open")
}

func isSafeReference(value string) bool {
	if value == "" {
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

func parseCommand(args []string) (string, []string, bool) {
	configPath := defaultConfigPath
	command := args
	if len(command) > 0 && command[0] == "--config" {
		if len(command) < 3 || !filepath.IsAbs(command[1]) {
			return "", nil, false
		}
		configPath = command[1]
		command = command[2:]
	}
	if !isFixedCommand(command) {
		return "", nil, false
	}
	return configPath, command, true
}
