package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jack/umodel-sre-rca/internal/config"
	"github.com/jack/umodel-sre-rca/internal/domain"
	"github.com/jack/umodel-sre-rca/internal/store"
)

type recordingRecoveryNotifier struct {
	updates int
	result  domain.RCAResult
}

func (n *recordingRecoveryNotifier) UpdateIncidentCard(_ context.Context, _ string, _ domain.Incident, result domain.RCAResult) error {
	n.updates++
	n.result = result
	return nil
}

func TestValidateGatewayConfigRejectsMissingCallbackToken(t *testing.T) {
	if err := validateGatewayConfig(config.Config{}); err == nil {
		t.Fatal("validateGatewayConfig() error = nil, want callback token validation")
	}
}

func TestValidateGatewayConfigRejectsMissingFeishuRecoveryCredentials(t *testing.T) {
	cfg := config.Config{CallbackToken: "callback-token"}
	cfg.Feishu.AppID = "app-id"
	if err := validateGatewayConfig(cfg); err == nil {
		t.Fatal("validateGatewayConfig() error = nil, want Feishu recovery credential validation")
	}
}

func TestValidateGatewayConfigAllowsCaptureWithoutFeishuCredentials(t *testing.T) {
	if err := validateGatewayConfig(config.Config{CallbackToken: "callback-token", CallbackCaptureDir: "/var/lib/sre-rca/callback-fixtures"}); err != nil {
		t.Fatalf("validateGatewayConfig() error = %v", err)
	}
}

func TestNewGatewayHandlerUpdatesRecoveryCard(t *testing.T) {
	repo, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	incident, _, err := repo.CreateOrGetIncident(context.Background(), domain.NewIncident("ws", "rule-1", "i-demo", time.Unix(10, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetFeishuMessageID(context.Background(), incident.ID, "om-demo", time.Unix(11, 0)); err != nil {
		t.Fatal(err)
	}
	notifier := &recordingRecoveryNotifier{}
	handler := newGatewayHandler(config.Config{CallbackToken: "callback-token"}, repo, notifier)
	body := []byte(`{"type":"ALERT","status":"RECOVERED","workspace":"ws","ruleId":"rule-1","timestamp":20000,"resource":{"entity":{"entity_id":"i-demo"}}}`)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/inbound/cloudmonitor?token=callback-token", bytes.NewReader(body)))
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if notifier.updates != 1 {
		t.Fatalf("recovery card updates=%d, want 1", notifier.updates)
	}
	if notifier.result.Summary != "CloudMonitor 告警已恢复。" {
		t.Fatalf("recovery summary=%q", notifier.result.Summary)
	}
}

func TestNewGatewayServerSetsBoundedTimeouts(t *testing.T) {
	server := newGatewayServer("127.0.0.1:8080", http.NewServeMux())
	if server.ReadHeaderTimeout <= 0 || server.ReadTimeout <= 0 || server.IdleTimeout <= 0 {
		t.Fatalf("gateway server timeouts: header=%s read=%s idle=%s", server.ReadHeaderTimeout, server.ReadTimeout, server.IdleTimeout)
	}
}
