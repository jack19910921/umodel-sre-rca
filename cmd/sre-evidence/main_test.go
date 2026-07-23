package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/jack/umodel-sre-rca/internal/config"
	"github.com/jack/umodel-sre-rca/internal/domain"
)

func TestCLIRejectsNonFixedCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"sql", "select *"}, &out, &errOut, nil); code != 2 {
		t.Fatalf("code=%d stderr=%s", code, errOut.String())
	}
}

func TestCLIRejectsExtraOrMalformedArgumentsBeforeLoadingConfig(t *testing.T) {
	tests := [][]string{
		{"incident", "context", "inc-1", "--extra"},
		{"--config", "relative.yaml", "incident", "context", "inc-1"},
		{"--config", "/tmp/config.yaml", "logs", "query"},
		{"incident", "context", "https://untrusted.example/selector?token=secret"},
	}
	oldLoad := loadConfig
	loadConfig = func(string) (config.Config, error) {
		t.Fatal("config must not load for malformed command")
		return config.Config{}, nil
	}
	t.Cleanup(func() { loadConfig = oldLoad })
	for _, args := range tests {
		var stdout, stderr bytes.Buffer
		if got := run(args, &stdout, &stderr, nil); got != 2 {
			t.Fatalf("args=%q code=%d stderr=%s", args, got, stderr.String())
		}
	}
}

func TestCLIConstructsConfiguredService(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "sre.yaml")
	var loadedPath string
	oldLoad, oldOpen, oldConstruct := loadConfig, openStateDB, newConfiguredService
	loadConfig = func(path string) (config.Config, error) {
		loadedPath = path
		return config.Config{StateDB: filepath.Join(dir, "state.db")}, nil
	}
	openStateDB = func(string) (stateRepository, error) { return fakeRepository{}, nil }
	newConfiguredService = func(config.Config, stateRepository) (commandService, error) { return fakeService{}, nil }
	t.Cleanup(func() {
		loadConfig, openStateDB, newConfiguredService = oldLoad, oldOpen, oldConstruct
	})

	var stdout, stderr bytes.Buffer
	if got := run([]string{"--config", configPath, "incident", "context", "inc-1"}, &stdout, &stderr, nil); got != 0 {
		t.Fatalf("code=%d stderr=%s", got, stderr.String())
	}
	if loadedPath != configPath {
		t.Fatalf("loaded config path=%q want=%q", loadedPath, configPath)
	}
}

type fakeRepository struct{}

func (fakeRepository) Close() error { return nil }
func (fakeRepository) IncidentByID(context.Context, string) (domain.Incident, error) {
	return domain.Incident{}, nil
}
func (fakeRepository) OpenEvidence(context.Context, string) (domain.Evidence, error) {
	return domain.Evidence{}, nil
}

type fakeService struct{}

func (fakeService) Context(context.Context, string) ([]domain.Evidence, error) {
	return []domain.Evidence{{ID: "context-1"}}, nil
}
func (fakeService) Metrics(context.Context, string) ([]domain.Evidence, error) { return nil, nil }
func (fakeService) Logs(context.Context, string) ([]domain.Evidence, error)    { return nil, nil }
func (fakeService) Changes(context.Context, string) ([]domain.Evidence, error) { return nil, nil }
func (fakeService) Open(context.Context, string) (domain.Evidence, error) {
	return domain.Evidence{}, nil
}
