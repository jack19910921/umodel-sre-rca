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
	newSchemaInspector = func(region, ecsRAMRoleName string) (modelsync.SchemaInspector, error) {
		return modelsync.NewCMSWriter(region, ecsRAMRoleName)
	}
	newRelationInspector = func(region, ecsRAMRoleName string) (modelsync.RelationInspector, error) {
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
	inspectSchema := flags.Bool("inspect-schema", false, "read the UModel graph for the SRE domain without writing data")
	inspectRelation := flags.Bool("inspect-relation", false, "verify the configured SRE-to-ECS relation without writing data")
	expireRelationType := flags.String("expire-relation-type", "", "expire exactly one configured endpoint-to-ECS relation type (default is dry-run)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *configPath == "" || !filepath.IsAbs(*configPath) || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: sre-sync --config /absolute/path/to/sre-sync.yaml [--apply | --inspect-schema]")
		return 2
	}
	operationCount := 0
	for _, enabled := range []bool{*apply, *inspectSchema, *inspectRelation} {
		if enabled {
			operationCount++
		}
	}
	if operationCount > 1 {
		fmt.Fprintln(stderr, "--apply, --inspect-schema, and --inspect-relation are mutually exclusive")
		return 2
	}
	if *expireRelationType != "" && (*inspectSchema || *inspectRelation) {
		fmt.Fprintln(stderr, "--expire-relation-type cannot be combined with inspection flags")
		return 2
	}

	cfg, err := loadSyncConfig(*configPath)
	if err != nil {
		fmt.Fprintf(stderr, "load config: %v\n", err)
		return 1
	}
	if *inspectSchema {
		inspector, err := newSchemaInspector(cfg.Region, cfg.ECSRAMRoleName)
		if err != nil {
			fmt.Fprintf(stderr, "create UModel schema inspector: %v\n", err)
			return 1
		}
		result, err := inspector.InspectSchema(context.Background(), cfg.Workspace, []string{"sre"})
		if err != nil {
			fmt.Fprintf(stderr, "inspect UModel schema: %v\n", err)
			return 1
		}
		if err := json.NewEncoder(stdout).Encode(map[string]any{
			"mode":      "schema-inspection",
			"workspace": cfg.Workspace,
			"region":    cfg.Region,
			"domains":   []string{"sre"},
			"result":    result,
		}); err != nil {
			fmt.Fprintf(stderr, "encode result: %v\n", err)
			return 1
		}
		return 0
	}
	if *inspectRelation {
		if len(cfg.Endpoints) != 1 {
			fmt.Fprintln(stderr, "inspect relation: exactly one endpoint must be configured")
			return 1
		}
		inspector, err := newRelationInspector(cfg.Region, cfg.ECSRAMRoleName)
		if err != nil {
			fmt.Fprintf(stderr, "create UModel relation inspector: %v\n", err)
			return 1
		}
		result, err := inspector.InspectRelation(context.Background(), cfg.Workspace, cfg.Endpoints[0], cfg.Relation)
		if err != nil {
			fmt.Fprintf(stderr, "inspect UModel relation: %v\n", err)
			return 1
		}
		if err := json.NewEncoder(stdout).Encode(map[string]any{
			"mode":          "relation-inspection",
			"workspace":     cfg.Workspace,
			"region":        cfg.Region,
			"endpoint_id":   cfg.Endpoints[0].EndpointID,
			"relation_type": cfg.Relation.Type,
			"result":        result,
		}); err != nil {
			fmt.Fprintf(stderr, "encode result: %v\n", err)
			return 1
		}
		return 0
	}
	observedAt := time.Now().UTC()
	plan := modelsync.Plan{}
	operation := "sync"
	if *expireRelationType != "" {
		if len(cfg.Endpoints) != 1 {
			fmt.Fprintln(stderr, "expire relation: exactly one endpoint must be configured")
			return 1
		}
		expiryPlan, err := modelsync.BuildRelationExpirationPlan(cfg.Endpoints[0], *expireRelationType, observedAt)
		if err != nil {
			fmt.Fprintf(stderr, "build relation expiry plan: %v\n", err)
			return 1
		}
		plan = expiryPlan
		operation = "expire-relation"
	} else {
		for _, endpoint := range cfg.Endpoints {
			endpointPlan, err := modelsync.BuildPlan(endpoint, cfg.Relation, observedAt)
			if err != nil {
				fmt.Fprintf(stderr, "build plan: %v\n", err)
				return 1
			}
			plan.Elements = append(plan.Elements, endpointPlan.Elements...)
		}
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
		"operation":        operation,
		"mode":             mode,
		"workspace":        cfg.Workspace,
		"region":           cfg.Region,
		"element_count":    len(plan.Elements),
		"relation_enabled": operation == "sync" && cfg.Relation.Type != "",
		"elements":         plan.Elements,
	}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintf(stderr, "encode result: %v\n", err)
		return 1
	}
	return 0
}
