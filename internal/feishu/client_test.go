package feishu

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jack/umodel-sre-rca/internal/domain"
)

func TestCreateIncidentCardReturnsMessageID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/v3/tenant_access_token/internal":
			_, _ = io.WriteString(w, `{"code":0,"tenant_access_token":"tenant-token","expire":7200}`)
		case "/im/v1/messages":
			if got := r.Header.Get("Authorization"); got != "Bearer tenant-token" {
				t.Fatalf("authorization=%q", got)
			}
			_, _ = io.WriteString(w, `{"code":0,"data":{"message_id":"om_demo"}}`)
		default:
			t.Fatalf("path=%s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "app-id", "app-secret", "chat-id", server.Client())
	id, err := client.CreateIncidentCard(context.Background(), domain.Incident{ID: "inc-1", State: domain.IncidentReceived})
	if err != nil || id != "om_demo" {
		t.Fatalf("id=%q err=%v", id, err)
	}
}

func TestRenderCardDoesNotIncludeOversizedOrSensitiveContent(t *testing.T) {
	card, err := RenderIncidentCard(domain.Incident{ID: "inc-1", State: domain.IncidentCompleted}, domain.RCAResult{Summary: strings.Repeat("x", 40_000), Confidence: 0.9, RootCause: "security group", EvidenceIDs: []string{"ev-1"}})
	if err == nil || card != nil {
		t.Fatalf("card=%v err=%v", card, err)
	}
}
