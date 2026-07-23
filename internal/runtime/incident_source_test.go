package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jack/umodel-sre-rca/internal/config"
	"github.com/jack/umodel-sre-rca/internal/domain"
)

type fakeIncidentRepository struct{ incident domain.Incident }

func (r fakeIncidentRepository) IncidentByID(context.Context, string) (domain.Incident, error) {
	return r.incident, nil
}

func TestIncidentSourceRejectsUnmappedCloudMonitorRule(t *testing.T) {
	source, err := NewIncidentSource(fakeIncidentRepository{incident: domain.Incident{
		ID: "incident-1", Workspace: "default-cms-demo", RuleID: "unreviewed-rule", ResourceID: "i-demo",
		AlertAt: time.Date(2026, time.July, 23, 8, 0, 0, 0, time.UTC),
	}}, []config.IncidentBinding{{
		Workspace: "default-cms-demo", RuleID: "reviewed-rule", ResourceID: "i-demo",
		Selectors: map[string]string{"instance_id": "i-demo"},
	}})
	if err != nil {
		t.Fatal(err)
	}

	_, err = source.ResolveIncident(context.Background(), "incident-1")
	if err == nil || !strings.Contains(err.Error(), "no incident binding") {
		t.Fatalf("ResolveIncident() error = %v, want no incident binding", err)
	}
}

func TestIncidentSourceUsesReviewedBindingOnly(t *testing.T) {
	alertAt := time.Date(2026, time.July, 23, 8, 0, 0, 0, time.UTC)
	source, err := NewIncidentSource(fakeIncidentRepository{incident: domain.Incident{
		ID: "incident-1", Workspace: "default-cms-demo", RuleID: "reviewed-rule", ResourceID: "i-demo", AlertAt: alertAt,
	}}, []config.IncidentBinding{{
		Workspace: "default-cms-demo", RuleID: "reviewed-rule", ResourceID: "i-demo",
		Selectors: map[string]string{
			"endpoint_id":       "blog-http",
			"probe_task_id":     "probe-reviewed",
			"instance_id":       "i-demo",
			"region_id":         "cn-hangzhou",
			"account_id":        "123456789",
			"security_group_id": "sg-reviewed",
		},
	}})
	if err != nil {
		t.Fatal(err)
	}

	got, err := source.ResolveIncident(context.Background(), "incident-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Selectors["endpoint_id"] != "blog-http" || got.Selectors["security_group_id"] != "sg-reviewed" {
		t.Fatalf("selectors = %#v, want reviewed binding values", got.Selectors)
	}
	if got.Window.End != alertAt || got.Window.Start != alertAt.Add(-30*time.Minute) {
		t.Fatalf("window = %#v, want fixed 30-minute window ending at alert", got.Window)
	}
}
