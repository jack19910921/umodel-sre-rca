package worker

import (
	"context"
	"testing"
	"time"

	"github.com/jack/umodel-sre-rca/internal/domain"
	"github.com/jack/umodel-sre-rca/internal/store"
)

type fakeCards struct{ updates int }

func (f *fakeCards) CreateIncidentCard(context.Context, domain.Incident) (string, error) {
	return "om-demo", nil
}
func (f *fakeCards) UpdateIncidentCard(context.Context, string, domain.Incident, domain.RCAResult) error {
	f.updates++
	return nil
}

type fakeEvidence struct{}

func (fakeEvidence) Context(context.Context, string) ([]domain.Evidence, error) {
	return []domain.Evidence{{ID: "ev-context"}}, nil
}
func (fakeEvidence) Metrics(context.Context, string) ([]domain.Evidence, error) {
	return []domain.Evidence{{ID: "ev-metric"}}, nil
}
func (fakeEvidence) Logs(context.Context, string) ([]domain.Evidence, error) {
	return []domain.Evidence{{ID: "ev-log"}}, nil
}
func (fakeEvidence) Changes(context.Context, string) ([]domain.Evidence, error) {
	return []domain.Evidence{{ID: "ev-change"}}, nil
}

type fakeRunner struct{ result domain.RCAResult }

func (f fakeRunner) Run(context.Context, string) (domain.RCAResult, error) { return f.result, nil }

func newWorker(t *testing.T, result domain.RCAResult) (*Worker, *store.SQLiteRepository, *fakeCards) {
	t.Helper()
	repo, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	cards := &fakeCards{}
	return New(repo, cards, fakeEvidence{}, fakeRunner{result: result}, "worker-a", func() time.Time { return time.Unix(200, 0) }), repo, cards
}

func enqueueFixtureIncident(t *testing.T, repo *store.SQLiteRepository) domain.Incident {
	t.Helper()
	incident, _, err := repo.CreateOrGetIncident(context.Background(), domain.NewIncident("ws", "rule-1", "i-demo", time.Unix(100, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Enqueue(context.Background(), incident.ID, time.Unix(101, 0)); err != nil {
		t.Fatal(err)
	}
	return incident
}

func TestWorkerCompletesAndUpdatesOneCard(t *testing.T) {
	result := domain.RCAResult{Summary: "security group removed TCP/80", RootCause: "security group", Confidence: 0.9, EvidenceIDs: []string{"ev-context", "ev-change"}}
	w, repo, cards := newWorker(t, result)
	incident := enqueueFixtureIncident(t, repo)
	if err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := repo.IncidentByID(context.Background(), incident.ID)
	if err != nil || got.State != domain.IncidentCompleted || cards.updates != 2 {
		t.Fatalf("incident=%#v updates=%d err=%v", got, cards.updates, err)
	}
}

func TestWorkerRetriesPendingAuditAfter120Seconds(t *testing.T) {
	result := domain.RCAResult{Summary: "audit event pending", RootCause: "security group", Confidence: 0.5, EvidenceIDs: []string{"ev-context"}, PendingAudit: true}
	w, repo, _ := newWorker(t, result)
	incident := enqueueFixtureIncident(t, repo)
	if err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := repo.IncidentByID(context.Background(), incident.ID)
	if err != nil || got.State != domain.IncidentAwaitingAuditEvent {
		t.Fatalf("incident=%#v err=%v", got, err)
	}
	job, ok, err := repo.ClaimNext(context.Background(), "worker-b", time.Unix(320, 0))
	if err != nil || !ok || job.IncidentID != incident.ID {
		t.Fatalf("job=%#v ok=%v err=%v", job, ok, err)
	}
}

func TestWorkerDoesNotOverwriteRecoveredIncident(t *testing.T) {
	w, repo, cards := newWorker(t, validResult())
	incident := enqueueFixtureIncident(t, repo)
	mustRecover(t, repo, incident)
	if err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := mustIncidentByID(t, repo, incident.ID); got.State != domain.IncidentRecovered {
		t.Fatalf("state=%s", got.State)
	}
	if cards.updates != 0 {
		t.Fatalf("updates=%d", cards.updates)
	}
}

func validResult() domain.RCAResult {
	return domain.RCAResult{Summary: "security group removed TCP/80", RootCause: "security group", Confidence: 0.9, EvidenceIDs: []string{"ev-context", "ev-change"}}
}

func mustRecover(t *testing.T, repo *store.SQLiteRepository, incident domain.Incident) {
	t.Helper()
	if _, recovered, err := repo.Recover(context.Background(), incident.Key, time.Unix(200, 0)); err != nil || !recovered {
		t.Fatalf("recovered=%v err=%v", recovered, err)
	}
}

func mustIncidentByID(t *testing.T, repo *store.SQLiteRepository, incidentID string) domain.Incident {
	t.Helper()
	incident, err := repo.IncidentByID(context.Background(), incidentID)
	if err != nil {
		t.Fatal(err)
	}
	return incident
}
