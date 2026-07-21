package inbound

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCapturePreservesKeysAndRedactsSensitiveValues(t *testing.T) {
	dir := t.TempDir()
	capture := NewCapture(dir)
	id, err := capture.Save(context.Background(), []byte(`{"ruleId":"rule-1","resourceId":"i-demo","accessKeySecret":"do-not-store","nested":{"token":"private","state":"ALERT"}}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dir + "/" + id + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture map[string]any
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	nested := fixture["nested"].(map[string]any)
	if fixture["ruleId"] != "rule-1" || fixture["accessKeySecret"] != "[REDACTED]" || nested["token"] != "[REDACTED]" || strings.Contains(string(raw), "do-not-store") {
		t.Fatalf("fixture=%s", raw)
	}
}

func TestCaptureRejectsNonJSON(t *testing.T) {
	_, err := NewCapture(t.TempDir()).Save(context.Background(), []byte("not json"))
	if err == nil || !strings.Contains(err.Error(), "valid JSON") {
		t.Fatalf("err=%v", err)
	}
}
