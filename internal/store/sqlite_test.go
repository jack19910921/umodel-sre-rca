package store

import (
	"context"
	"testing"
	"time"

	"github.com/jack/umodel-sre-rca/internal/domain"
)

func newTestRepo(t *testing.T) *SQLiteRepository {
	t.Helper()
	repo, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo
}

func TestCreateOrGetIncidentDeduplicatesActiveAlert(t *testing.T) {
	repo := newTestRepo(t)
	in := domain.NewIncident("ws", "rule-1", "i-demo", time.Unix(100, 0))
	first, created, err := repo.CreateOrGetIncident(context.Background(), in)
	if err != nil || !created {
		t.Fatalf("first=%#v created=%v err=%v", first, created, err)
	}
	second, created, err := repo.CreateOrGetIncident(context.Background(), in)
	if err != nil || created || first.ID != second.ID {
		t.Fatalf("second=%#v created=%v err=%v", second, created, err)
	}
}

func TestClaimNextClaimsQueuedJobOnce(t *testing.T) {
	repo := newTestRepo(t)
	incident, _, err := repo.CreateOrGetIncident(context.Background(), domain.NewIncident("ws", "rule-1", "i-demo", time.Unix(100, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Enqueue(context.Background(), incident.ID, time.Unix(101, 0)); err != nil {
		t.Fatal(err)
	}
	job, ok, err := repo.ClaimNext(context.Background(), "worker-a", time.Unix(102, 0))
	if err != nil || !ok || job.Status != domain.JobRunning {
		t.Fatalf("job=%#v ok=%v err=%v", job, ok, err)
	}
	_, ok, err = repo.ClaimNext(context.Background(), "worker-b", time.Unix(103, 0))
	if err != nil || ok {
		t.Fatalf("second claim ok=%v err=%v", ok, err)
	}
}

func TestClaimNextReclaimsExpiredLeaseButNotRecoveredCancelledJob(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	incident, _, err := repo.CreateOrGetIncident(ctx, domain.NewIncident("ws", "rule-1", "i-expired", time.Unix(100, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Enqueue(ctx, incident.ID, time.Unix(101, 0)); err != nil {
		t.Fatal(err)
	}
	first, ok, err := repo.ClaimNext(ctx, "worker-a", time.Unix(102, 0))
	if err != nil || !ok {
		t.Fatalf("first=%#v ok=%v err=%v", first, ok, err)
	}
	reclaimed, ok, err := repo.ClaimNext(ctx, "worker-b", time.Unix(403, 0))
	if err != nil || !ok || reclaimed.ID != first.ID || reclaimed.WorkerID != "worker-b" || reclaimed.Attempt != 2 {
		t.Fatalf("reclaimed=%#v ok=%v err=%v", reclaimed, ok, err)
	}

	if _, recovered, err := repo.Recover(ctx, incident.Key, time.Unix(404, 0)); err != nil || !recovered {
		t.Fatalf("recovered=%v err=%v", recovered, err)
	}
	if _, ok, err := repo.ClaimNext(ctx, "worker-c", time.Unix(1000, 0)); err != nil || ok {
		t.Fatalf("cancelled recovered job was reclaimed: ok=%v err=%v", ok, err)
	}
}

func TestCompletePersistsResultAndClosesIncident(t *testing.T) {
	repo := newTestRepo(t)
	incident, _, err := repo.CreateOrGetIncident(context.Background(), domain.NewIncident("ws", "rule-1", "i-demo", time.Unix(100, 0)))
	if err != nil {
		t.Fatal(err)
	}
	result := domain.RCAResult{
		Summary:     "TCP/80 was removed from the security group",
		Confidence:  0.9,
		RootCause:   "security group change",
		EvidenceIDs: []string{"change-1"},
	}
	if err := repo.Complete(context.Background(), incident.ID, result, time.Unix(110, 0)); err != nil {
		t.Fatal(err)
	}
	got, err := repo.IncidentByID(context.Background(), incident.ID)
	if err != nil || got.State != domain.IncidentCompleted {
		t.Fatalf("incident=%#v err=%v", got, err)
	}
	if count := countEvidenceSnapshots(t, repo, incident.ID); count != 1 {
		t.Fatalf("snapshots=%d, want 1", count)
	}
}

func TestScheduleAuditRetryMarksIncidentAndQueuesJob(t *testing.T) {
	repo := newTestRepo(t)
	incident, _, err := repo.CreateOrGetIncident(context.Background(), domain.NewIncident("ws", "rule-1", "i-demo", time.Unix(100, 0)))
	if err != nil {
		t.Fatal(err)
	}
	retryAt := time.Unix(220, 0)
	if err := repo.ScheduleAuditRetry(context.Background(), incident.ID, retryAt); err != nil {
		t.Fatal(err)
	}
	got, err := repo.IncidentByID(context.Background(), incident.ID)
	if err != nil || got.State != domain.IncidentAwaitingAuditEvent {
		t.Fatalf("incident=%#v err=%v", got, err)
	}
	job, ok, err := repo.ClaimNext(context.Background(), "worker-a", retryAt)
	if err != nil || !ok || job.IncidentID != incident.ID {
		t.Fatalf("job=%#v ok=%v err=%v", job, ok, err)
	}
}

func TestRecoverClosesActiveIncident(t *testing.T) {
	repo := newTestRepo(t)
	incident, _, err := repo.CreateOrGetIncident(context.Background(), domain.NewIncident("ws", "rule-1", "i-demo", time.Unix(100, 0)))
	if err != nil {
		t.Fatal(err)
	}
	got, recovered, err := repo.Recover(context.Background(), incident.Key, time.Unix(120, 0))
	if err != nil || !recovered || got.State != domain.IncidentRecovered {
		t.Fatalf("incident=%#v recovered=%v err=%v", got, recovered, err)
	}
	got, err = repo.IncidentByID(context.Background(), incident.ID)
	if err != nil || got.State != domain.IncidentRecovered {
		t.Fatalf("incident=%#v err=%v", got, err)
	}
}

func TestRecoverCancelsQueuedJob(t *testing.T) {
	repo := newTestRepo(t)
	incident, _, err := repo.CreateOrGetIncident(context.Background(), domain.NewIncident("ws", "rule-1", "i-demo", time.Unix(100, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Enqueue(context.Background(), incident.ID, time.Unix(101, 0)); err != nil {
		t.Fatal(err)
	}
	got, recovered, err := repo.Recover(context.Background(), incident.Key, time.Unix(200, 0))
	if err != nil || !recovered || got.State != domain.IncidentRecovered {
		t.Fatalf("got=%#v recovered=%v err=%v", got, recovered, err)
	}
	if status := onlyJobStatus(t, repo, incident.ID); status != domain.JobCancelled {
		t.Fatalf("status=%s", status)
	}
}

func countEvidenceSnapshots(t *testing.T, repo *SQLiteRepository, incidentID string) int {
	t.Helper()
	var count int
	if err := repo.db.QueryRow(`SELECT COUNT(*) FROM evidence_snapshots WHERE incident_id = ?`, incidentID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func onlyJobStatus(t *testing.T, repo *SQLiteRepository, incidentID string) string {
	t.Helper()
	var status string
	if err := repo.db.QueryRow(`SELECT status FROM jobs WHERE incident_id = ?`, incidentID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}
