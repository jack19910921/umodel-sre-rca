// sre-sync projects a small, customer-owned SRE endpoint model into UModel.
// It is intentionally administrator-triggered and defaults to dry-run mode.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jack/umodel-sre-rca/internal/modelsync"
)

var (
	loadSyncConfig = modelsync.LoadConfig
	newWriter      = func(region, ecsRAMRoleName string) (modelsync.Writer, error) {
		return modelsync.NewCMSWriter(region, ecsRAMRoleName)
	}
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("sre-sync", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "absolute path to the SRE sync YAML file")
	apply := flags.Bool("apply", false, "write the plan to UModel (default is dry-run)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *configPath == "" || !filepath.IsAbs(*configPath) || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: sre-sync --config /absolute/path/to/sre-sync.yaml [--apply]")
		return 2
	}

	cfg, err := loadSyncConfig(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}
	plan := modelsync.Plan{}
	observedAt := time.Now().UTC()
	for _, endpoint := range cfg.Endpoints {
		endpointPlan, err := modelsync.BuildPlan(endpoint, cfg.Relation, observedAt)
		if err != nil {
			fmt.Fprintf(stderr, "build plan: %v\n", err)
			return 1
		}
		plan.Elements = append(plan.Elements, endpointPlan.Elements...)
	}

	mode := "dry-run"
	if *apply {
		writer, err := newWriter(cfg.Region, cfg.ECSRAMRoleName)
		if err != nil {
			fmt.Fprintf(stderr, "create UModel writer: %v\n", err)
			return 1
		}
		if err := writer.Upsert(context.Background(), cfg.Workspace, plan); err != nil {
			fmt.Fprintf(stderr, "upsert UModel data: %v\n", err)
			return 1
		}
		mode = "applied"
	}

	result := map[string]any{
		"mode":             mode,
		"workspace":        cfg.Workspace,
		"region":           cfg.Region,
		"element_count":    len(plan.Elements),
		"relation_enabled": cfg.Relation.Type != "",
		"elements":         plan.Elements,
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintf(stderr, "encode result: %v\n", err)
		return 1
	}
	return 0
}
