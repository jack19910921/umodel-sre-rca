package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
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

func TestNewGatewayRemainsStronglyTypedAndNotifierIsExplicit(t *testing.T) {
	var _ func(string, string, ...incidentRepository) http.Handler = NewGateway
	repo := newTestRepo(t)
	srv := NewGatewayWithNotifier("test-token", "", repo, &fakeRecoveryNotifier{})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
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

func TestCloudMonitorRecoveryNotifiesExistingCard(t *testing.T) {
	repo := newTestRepo(t)
	notifier := &fakeRecoveryNotifier{}
	srv := NewGatewayWithNotifier("test-token", "", repo, notifier)
	incident, _, err := repo.CreateOrGetIncident(context.Background(), domain.NewIncident("ws", "rule-1", "i-demo", time.Unix(100, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetFeishuMessageID(context.Background(), incident.ID, "om-demo", time.Unix(101, 0)); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{
		"type":"ALERT", "status":"RECOVERED", "workspace":"ws", "ruleId":"rule-1",
		"time":"2026-07-22T15:14:40Z", "resource":{"entity":{"entity_id":"i-demo"}}
	}`)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/inbound/cloudmonitor?token=test-token", bytes.NewReader(body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if notifier.updates != 1 {
		t.Fatalf("updates=%d", notifier.updates)
	}
}

func TestCloudMonitorRecoveryClosesCompletedIncidentAndUpdatesCard(t *testing.T) {
	repo := newTestRepo(t)
	notifier := &fakeRecoveryNotifier{}
	srv := NewGatewayWithNotifier("test-token", "", repo, notifier)
	incident, _, err := repo.CreateOrGetIncident(context.Background(), domain.NewIncident("ws", "rule-1", "i-demo", time.Unix(10, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetFeishuMessageID(context.Background(), incident.ID, "om-demo", time.Unix(11, 0)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Complete(context.Background(), incident.ID, domain.RCAResult{Summary: "RCA complete"}, time.Unix(12, 0)); err != nil {
		t.Fatal(err)
	}

	body := []byte(`{"type":"ALERT","status":"RECOVERED","workspace":"ws","ruleId":"rule-1","timestamp":20000,"resource":{"entity":{"entity_id":"i-demo"}}}`)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/inbound/cloudmonitor?token=test-token", bytes.NewReader(body)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	got, err := repo.IncidentByID(context.Background(), incident.ID)
	if err != nil || got.State != domain.IncidentRecovered {
		t.Fatalf("incident=%#v err=%v", got, err)
	}
	if notifier.updates != 1 || len(notifier.states) != 1 || notifier.states[0] != domain.IncidentRecovered {
		t.Fatalf("updates=%d states=%v", notifier.updates, notifier.states)
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

func TestCloudMonitorRetriesRecoveryCardForDuplicateCallback(t *testing.T) {
	repo := newTestRepo(t)
	notifier := &flakyRecoveryNotifier{failures: 2}
	srv := NewGatewayWithNotifier("test-token", "", repo, notifier)
	incident, _, err := repo.CreateOrGetIncident(context.Background(), domain.NewIncident("ws", "rule-1", "i-demo", time.Unix(100, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Enqueue(context.Background(), incident.ID, time.Unix(101, 0)); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetFeishuMessageID(context.Background(), incident.ID, "om-demo", time.Unix(101, 0)); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{
		"type":"ALERT", "status":"RECOVERED", "workspace":"ws", "ruleId":"rule-1",
		"time":"2026-07-22T15:14:40Z", "resource":{"entity":{"entity_id":"i-demo"}}
	}`)
	for attempt, want := range []int{http.StatusInternalServerError, http.StatusInternalServerError, http.StatusAccepted} {
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/inbound/cloudmonitor?token=test-token", bytes.NewReader(body)))
		if rec.Code != want {
			t.Fatalf("attempt=%d code=%d want=%d body=%s", attempt+1, rec.Code, want, rec.Body.String())
		}
	}
	if notifier.updates != 3 {
		t.Fatalf("updates=%d", notifier.updates)
	}
	got, err := repo.IncidentByID(context.Background(), incident.ID)
	if err != nil || got.State != domain.IncidentRecovered {
		t.Fatalf("incident=%#v err=%v", got, err)
	}
	if _, ok, err := repo.ClaimNext(context.Background(), "worker-a", time.Now().UTC()); err != nil || ok {
		t.Fatalf("duplicate recovery created a runnable job: ok=%v err=%v", ok, err)
	}
}

func TestCloudMonitorOldRecoveryDoesNotCloseNewerGeneration(t *testing.T) {
	for _, terminal := range []string{domain.IncidentReceived, domain.IncidentCompleted, domain.IncidentFailed} {
		t.Run(terminal, func(t *testing.T) {
			repo := newTestRepo(t)
			notifier := &flakyRecoveryNotifier{failures: 1}
			srv := NewGatewayWithNotifier("test-token", "", repo, notifier)
			first, _, err := repo.CreateOrGetIncident(context.Background(), domain.NewIncident("ws", "rule-1", "i-demo", time.Unix(10, 0)))
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.SetFeishuMessageID(context.Background(), first.ID, "om-first", time.Unix(11, 0)); err != nil {
				t.Fatal(err)
			}
			switch terminal {
			case domain.IncidentCompleted:
				if err := repo.Complete(context.Background(), first.ID, domain.RCAResult{Summary: "done"}, time.Unix(12, 0)); err != nil {
					t.Fatal(err)
				}
			case domain.IncidentFailed:
				if err := repo.Fail(context.Background(), first.ID, time.Unix(12, 0)); err != nil {
					t.Fatal(err)
				}
			}

			serve := func(status string, timestamp int64) *httptest.ResponseRecorder {
				rec := httptest.NewRecorder()
				body := []byte(`{"type":"ALERT","status":"` + status + `","workspace":"ws","ruleId":"rule-1","timestamp":` + strconv.FormatInt(timestamp, 10) + `,"resource":{"entity":{"entity_id":"i-demo"}}}`)
				srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/inbound/cloudmonitor?token=test-token", bytes.NewReader(body)))
				return rec
			}
			if rec := serve("RECOVERED", 20_000); rec.Code != http.StatusInternalServerError {
				t.Fatalf("first recovery code=%d body=%s", rec.Code, rec.Body.String())
			}
			rec := serve("OCCURRED", 30_000)
			if rec.Code != http.StatusAccepted {
				t.Fatalf("new occurrence code=%d body=%s", rec.Code, rec.Body.String())
			}
			var response struct {
				IncidentID string `json:"incident_id"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.IncidentID == "" {
				t.Fatalf("new occurrence response=%s err=%v", rec.Body.String(), err)
			}
			second, err := repo.IncidentByID(context.Background(), response.IncidentID)
			if err != nil || second.State != domain.IncidentReceived {
				t.Fatalf("new generation=%#v err=%v", second, err)
			}
			if rec := serve("RECOVERED", 20_000); rec.Code != http.StatusAccepted {
				t.Fatalf("replayed recovery code=%d body=%s", rec.Code, rec.Body.String())
			}
			first, err = repo.IncidentByID(context.Background(), first.ID)
			if err != nil || first.State != domain.IncidentRecovered {
				t.Fatalf("first=%#v err=%v", first, err)
			}
			second, err = repo.IncidentByID(context.Background(), second.ID)
			if err != nil || second.State != domain.IncidentReceived {
				t.Fatalf("second=%#v err=%v", second, err)
			}
			job, ok, err := repo.ClaimNext(context.Background(), "worker-b", time.Unix(31, 0))
			if err != nil || !ok || job.IncidentID != second.ID {
				t.Fatalf("job=%#v ok=%v err=%v", job, ok, err)
			}
		})
	}
}

func TestCloudMonitorDuplicateRecoveryUpdatesLatestRecoveredGenerationOnly(t *testing.T) {
	repo := newTestRepo(t)
	notifier := &recordingRecoveryNotifier{}
	srv := NewGatewayWithNotifier("test-token", "", repo, notifier)
	first, _, err := repo.CreateOrGetIncident(context.Background(), domain.NewIncident("ws", "rule-1", "i-demo", time.Unix(10, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SetFeishuMessageID(context.Background(), first.ID, "om-first", time.Unix(11, 0)); err != nil {
		t.Fatal(err)
	}
	if err := repo.Complete(context.Background(), first.ID, domain.RCAResult{Summary: "done"}, time.Unix(12, 0)); err != nil {
		t.Fatal(err)
	}
	serve := func(status string, timestamp int64) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		body := []byte(`{"type":"ALERT","status":"` + status + `","workspace":"ws","ruleId":"rule-1","timestamp":` + strconv.FormatInt(timestamp, 10) + `,"resource":{"entity":{"entity_id":"i-demo"}}}`)
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/inbound/cloudmonitor?token=test-token", bytes.NewReader(body)))
		return rec
	}
	rec := serve("OCCURRED", 30_000)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("occurred code=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		IncidentID string `json:"incident_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.IncidentID == "" {
		t.Fatalf("occurred response=%s err=%v", rec.Body.String(), err)
	}
	second, err := repo.IncidentByID(context.Background(), response.IncidentID)
	if err != nil || second.State != domain.IncidentReceived {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	if err := repo.SetFeishuMessageID(context.Background(), second.ID, "om-second", time.Unix(31, 0)); err != nil {
		t.Fatal(err)
	}
	if rec := serve("RECOVERED", 40_000); rec.Code != http.StatusAccepted {
		t.Fatalf("first recovery code=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := serve("RECOVERED", 40_000); rec.Code != http.StatusAccepted {
		t.Fatalf("duplicate recovery code=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(notifier.incidentIDs) != 2 || notifier.incidentIDs[0] != second.ID || notifier.incidentIDs[1] != second.ID {
		t.Fatalf("updated incident IDs=%v want=%s", notifier.incidentIDs, second.ID)
	}
	first, err = repo.IncidentByID(context.Background(), first.ID)
	if err != nil || first.State != domain.IncidentCompleted {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	if _, ok, err := repo.ClaimNext(context.Background(), "worker-a", time.Unix(41, 0)); err != nil || ok {
		t.Fatalf("duplicate recovery left a runnable job: ok=%v err=%v", ok, err)
	}
}

type failingRecoveryRepo struct{}

func (failingRecoveryRepo) CreateOrGetIncident(context.Context, domain.Incident) (domain.Incident, bool, error) {
	return domain.Incident{}, false, errors.New("not called")
}

func (failingRecoveryRepo) Enqueue(context.Context, string, time.Time) error {
	return errors.New("not called")
}

func (failingRecoveryRepo) Recover(context.Context, string, time.Time) (domain.Incident, bool, error) {
	return domain.Incident{}, false, errors.New("database unavailable")
}

func (failingRecoveryRepo) RecoveredIncidentByKey(context.Context, string, time.Time) (domain.Incident, bool, error) {
	return domain.Incident{}, false, errors.New("not called")
}

type fakeRecoveryNotifier struct {
	updates int
	states  []string
}

func (f *fakeRecoveryNotifier) UpdateIncidentCard(_ context.Context, _ string, incident domain.Incident, _ domain.RCAResult) error {
	f.updates++
	f.states = append(f.states, incident.State)
	return nil
}

type flakyRecoveryNotifier struct {
	updates  int
	failures int
}

type recordingRecoveryNotifier struct {
	failures    int
	incidentIDs []string
}

func (f *recordingRecoveryNotifier) UpdateIncidentCard(_ context.Context, _ string, incident domain.Incident, _ domain.RCAResult) error {
	f.incidentIDs = append(f.incidentIDs, incident.ID)
	if f.failures > 0 {
		f.failures--
		return errors.New("notifier unavailable")
	}
	return nil
}

func (f *flakyRecoveryNotifier) UpdateIncidentCard(context.Context, string, domain.Incident, domain.RCAResult) error {
	f.updates++
	if f.failures > 0 {
		f.failures--
		return errors.New("notifier unavailable")
	}
	return nil
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
