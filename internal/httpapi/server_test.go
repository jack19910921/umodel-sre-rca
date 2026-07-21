package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestCaptureRejectsWrongToken(t *testing.T) {
	srv := NewGateway("test-token", t.TempDir())
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/inbound/cloudmonitor/capture?token=wrong", bytes.NewBufferString(`{"state":"ALERT"}`)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d", rec.Code)
	}
}

func TestCaptureWritesRedactedFixture(t *testing.T) {
	dir := t.TempDir()
	srv := NewGateway("test-token", dir)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/inbound/cloudmonitor/capture?token=test-token", bytes.NewBufferString(`{"ruleId":"rule-1","secret":"hidden"}`)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%v err=%v", entries, err)
	}
}
