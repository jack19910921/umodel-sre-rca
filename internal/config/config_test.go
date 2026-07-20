package config

import (
	"strings"
	"testing"
)

func TestLoadRejectsMissingCallbackToken(t *testing.T) {
	_, err := Load("../../testdata/invalid-config.yaml")
	if err == nil || !strings.Contains(err.Error(), "callback_token") {
		t.Fatalf("Load() error = %v, want callback_token validation", err)
	}
}

func TestLoadAcceptsEcsRAMRoleProfile(t *testing.T) {
	cfg, err := Load("../../testdata/valid-config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Aliyun.Profile != "sre-ecs-role" || cfg.Worker.MaxTurns != 8 {
		t.Fatalf("unexpected config: %#v", cfg)
	}
}
