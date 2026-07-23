package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	ListenAddr           string `yaml:"listen_addr"`
	CallbackToken        string `yaml:"callback_token"`
	CallbackCaptureDir   string `yaml:"callback_capture_dir"`
	StateDB              string `yaml:"state_db"`
	EvidenceBindingsPath string `yaml:"evidence_bindings_path"`
	IncidentBindingsPath string `yaml:"incident_bindings_path"`
	Aliyun               struct {
		Profile     string `yaml:"profile"`
		Workspace   string `yaml:"workspace"`
		Region      string `yaml:"region"`
		SLSProject  string `yaml:"sls_project"`
		SLSLogstore string `yaml:"sls_logstore"`
	} `yaml:"aliyun"`
	Feishu struct {
		AppID     string `yaml:"app_id"`
		AppSecret string `yaml:"app_secret"`
		ChatID    string `yaml:"chat_id"`
	} `yaml:"feishu"`
	Worker struct {
		Enabled        bool `yaml:"enabled"`
		MaxTurns       int  `yaml:"max_turns"`
		TimeoutSeconds int  `yaml:"timeout_seconds"`
		PollSeconds    int  `yaml:"poll_seconds"`
	} `yaml:"worker"`
}

func Load(path string) (Config, error) {
	var cfg Config
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := yaml.Unmarshal([]byte(os.ExpandEnv(string(raw))), &cfg); err != nil {
		return cfg, err
	}
	if cfg.CallbackToken == "" {
		return cfg, errors.New("callback_token is required")
	}
	if cfg.StateDB == "" || cfg.Worker.MaxTurns < 1 {
		return cfg, errors.New("state_db and worker.max_turns are required")
	}
	if cfg.Worker.Enabled {
		if cfg.Aliyun.Profile == "" || cfg.Aliyun.Workspace == "" || cfg.Aliyun.Region == "" || cfg.Aliyun.SLSProject == "" || cfg.Aliyun.SLSLogstore == "" || cfg.EvidenceBindingsPath == "" || cfg.IncidentBindingsPath == "" {
			return cfg, errors.New("worker.enabled requires aliyun profile, workspace, region, SLS project/logstore, evidence_bindings_path, and incident_bindings_path")
		}
		if cfg.Worker.PollSeconds < 1 {
			return cfg, errors.New("worker.enabled requires worker.poll_seconds greater than zero")
		}
		if cfg.Feishu.AppID == "" || cfg.Feishu.AppSecret == "" || cfg.Feishu.ChatID == "" {
			return cfg, errors.New("worker.enabled requires feishu app_id, app_secret, and chat_id")
		}
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = "127.0.0.1:8080"
	}
	if cfg.Worker.TimeoutSeconds < 1 {
		cfg.Worker.TimeoutSeconds = 300
	}
	return cfg, nil
}

type IncidentBinding struct {
	Workspace  string            `yaml:"workspace"`
	RuleID     string            `yaml:"rule_id"`
	ResourceID string            `yaml:"resource_id"`
	Selectors  map[string]string `yaml:"selectors"`
}

type incidentBindingsFile struct {
	IncidentBindings []IncidentBinding `yaml:"incident_bindings"`
}

func LoadIncidentBindings(path string) ([]IncidentBinding, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file incidentBindingsFile
	if err := yaml.Unmarshal([]byte(os.ExpandEnv(string(raw))), &file); err != nil {
		return nil, fmt.Errorf("parse incident bindings: %w", err)
	}
	if err := ValidateIncidentBindings(file.IncidentBindings); err != nil {
		return nil, err
	}
	return file.IncidentBindings, nil
}

func ValidateIncidentBindings(bindings []IncidentBinding) error {
	if len(bindings) == 0 {
		return errors.New("at least one incident binding is required")
	}
	seen := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		if strings.TrimSpace(binding.Workspace) == "" || strings.TrimSpace(binding.RuleID) == "" || strings.TrimSpace(binding.ResourceID) == "" {
			return errors.New("incident binding workspace, rule_id, and resource_id are required")
		}
		key := incidentBindingKey(binding)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate incident binding %q", key)
		}
		seen[key] = struct{}{}
		if len(binding.Selectors) == 0 {
			return fmt.Errorf("incident binding %q selectors are required", key)
		}
		for name, value := range binding.Selectors {
			if strings.TrimSpace(name) == "" || strings.TrimSpace(value) == "" {
				return fmt.Errorf("incident binding %q contains empty selector", key)
			}
		}
	}
	return nil
}

func IncidentBindingKey(binding IncidentBinding) string {
	return incidentBindingKey(binding)
}

func incidentBindingKey(binding IncidentBinding) string {
	return binding.Workspace + "\x00" + binding.RuleID + "\x00" + binding.ResourceID
}
