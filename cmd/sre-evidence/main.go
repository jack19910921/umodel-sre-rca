package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/jack/umodel-sre-rca/internal/domain"
)

type commandService interface {
	Context(context.Context, string) ([]domain.Evidence, error)
	Metrics(context.Context, string) ([]domain.Evidence, error)
	Logs(context.Context, string) ([]domain.Evidence, error)
	Changes(context.Context, string) ([]domain.Evidence, error)
	Open(context.Context, string) (domain.Evidence, error)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, nil))
}

func run(args []string, stdout, stderr io.Writer, service commandService) int {
	if !isFixedCommand(args) {
		fmt.Fprintln(stderr, "usage: sre-evidence {incident context|metrics query|logs query|changes query|evidence open} <id>")
		return 2
	}
	if service == nil {
		fmt.Fprintln(stderr, "evidence service is not configured")
		return 1
	}

	var items []domain.Evidence
	var err error
	incidentID := args[len(args)-1]
	switch args[0] {
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
	if len(args) != 3 || args[2] == "" {
		return false
	}
	return (args[0] == "incident" && args[1] == "context") ||
		(args[0] == "metrics" && args[1] == "query") ||
		(args[0] == "logs" && args[1] == "query") ||
		(args[0] == "changes" && args[1] == "query") ||
		(args[0] == "evidence" && args[1] == "open")
}
