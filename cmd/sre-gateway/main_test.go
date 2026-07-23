package main

import (
	"testing"

	"github.com/jack/umodel-sre-rca/internal/config"
)

func TestValidateGatewayConfigRejectsMissingCallbackToken(t *testing.T) {
	if err := validateGatewayConfig(config.Config{}); err == nil {
		t.Fatal("validateGatewayConfig() error = nil, want callback token validation")
	}
}
