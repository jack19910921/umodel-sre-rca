package modelsync

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is intentionally separate from the RCA worker configuration. It is
// only for the administrator-triggered static SRE model synchronizer.
type Config struct {
	Workspace      string     `yaml:"workspace"`
	Region         string     `yaml:"region"`
	ECSRAMRoleName string     `yaml:"ecs_ram_role_name"`
	Relation       Relation   `yaml:"relation"`
	Endpoints      []Endpoint `yaml:"endpoints"`
}

func LoadConfig(path string) (Config, error) {
	var cfg Config
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("parse SRE sync config: %w", err)
	}
	cfg.Workspace = strings.TrimSpace(cfg.Workspace)
	cfg.Region = strings.TrimSpace(cfg.Region)
	cfg.ECSRAMRoleName = strings.TrimSpace(cfg.ECSRAMRoleName)
	if !workspacePattern.MatchString(cfg.Workspace) {
		return cfg, fmt.Errorf("workspace is required and must be safe")
	}
	if cfg.Region == "" {
		return cfg, fmt.Errorf("region is required")
	}
	if cfg.ECSRAMRoleName == "" {
		return cfg, fmt.Errorf("ecs_ram_role_name is required")
	}
	if len(cfg.Endpoints) == 0 {
		return cfg, fmt.Errorf("at least one endpoint is required")
	}
	for index, endpoint := range cfg.Endpoints {
		if _, err := BuildPlan(endpoint, cfg.Relation, time.Now()); err != nil {
			return cfg, fmt.Errorf("endpoint %d: %w", index, err)
		}
	}
	return cfg, nil
}
