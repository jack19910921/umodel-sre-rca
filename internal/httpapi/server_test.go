package httpapi

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jack/umodel-sre-rca/internal/domain"
	"github.com/jack/umodel-sre-rca/internal/store"
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

func TestCloudMonitorOccurredCreatesOneIncidentAndJob(t *testing.T) {
	repo := newTestRepo(t)
	srv := NewGateway("test-token", "", repo)
	body := []byte(`{
		"type":"ALERT", "status":"OCCURRED", "workspace":"ws", "ruleId":"rule-1",
		"time":"2026-07-22T15:14:40Z",
		"resource":{"entity":{"entity_id":"i-demo"}}, "data":{"currentValue":52.867}
	}`)
	for range 2 {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/inbound/cloudmonitor?token=test-token", bytes.NewReader(body)))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
		}
	}
	job, ok, err := repo.ClaimNext(context.Background(), "worker-a", time.Now().UTC())
	if err != nil || !ok || job.IncidentID == "" {
		t.Fatalf("job=%#v ok=%v err=%v", job, ok, err)
	}
	if _, ok, err := repo.ClaimNext(context.Background(), "worker-b", time.Now().UTC()); err != nil || ok {
		t.Fatalf("duplicate callback queued another job: ok=%v err=%v", ok, err)
	}
}

func TestCloudMonitorRecoveredMarksActiveIncidentRecovered(t *testing.T) {
	repo := newTestRepo(t)
	srv := NewGateway("test-token", "", repo)
	incidentID := ""
	for index, status := range []string{"OCCURRED", "RECOVERED"} {
		rec := httptest.NewRecorder()
		body := []byte(`{
			"type":"ALERT", "status":"` + status + `", "workspace":"ws", "ruleId":"rule-1",
			"time":"2026-07-22T15:14:40Z", "resource":{"entity":{"entity_id":"i-demo"}}
		}`)
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/inbound/cloudmonitor?token=test-token", bytes.NewReader(body)))
		if rec.Code != http.StatusAccepted {
			t.Fatalf("status=%s code=%d body=%s", status, rec.Code, rec.Body.String())
		}
		if index == 0 {
			job, ok, err := repo.ClaimNext(context.Background(), "worker-a", time.Now().UTC())
			if err != nil || !ok {
				t.Fatalf("claim trigger job: job=%#v ok=%v err=%v", job, ok, err)
			}
			incidentID = job.IncidentID
			incident, err := repo.IncidentByID(context.Background(), job.IncidentID)
			if err != nil {
				t.Fatal(err)
			}
			if incident.State != "RECEIVED" {
				t.Fatalf("initial state=%s", incident.State)
			}
		}
	}
	incident, err := repo.IncidentByID(context.Background(), incidentID)
	if err != nil {
		t.Fatal(err)
	}
	if incident.State != "RECOVERED" {
		t.Fatalf("state=%s", incident.State)
	}
}

func TestCloudMonitorRecoveryReturnsFailureForPersistenceError(t *testing.T) {
	srv := NewGateway("test-token", "", failingRecoveryRepo{})
	rec := httptest.NewRecorder()
	body := []byte(`{
		"type":"ALERT", "status":"RECOVERED", "workspace":"ws", "ruleId":"rule-1",
		"time":"2026-07-22T15:14:40Z", "resource":{"entity":{"entity_id":"i-demo"}}
	}`)
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/inbound/cloudmonitor?token=test-token", bytes.NewReader(body)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
}

type failingRecoveryRepo struct{}

func (failingRecoveryRepo) CreateOrGetIncident(context.Context, domain.Incident) (domain.Incident, bool, error) {
	return domain.Incident{}, false, errors.New("not called")
}

func (failingRecoveryRepo) Enqueue(context.Context, string, time.Time) error {
	return errors.New("not called")
}

func (failingRecoveryRepo) MarkRecovered(context.Context, string, time.Time) error {
	return errors.New("database unavailable")
}

func newTestRepo(t *testing.T) *store.SQLiteRepository {
	t.Helper()
	repo, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}
