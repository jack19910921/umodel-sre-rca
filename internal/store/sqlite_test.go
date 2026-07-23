package store

import (
	"context"
	"errors"
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

func TestClaimFenceRejectsStaleCompletionAndRetry(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	incident, _, err := repo.CreateOrGetIncident(ctx, domain.NewIncident("ws", "rule-1", "i-lease-fence", time.Unix(100, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Enqueue(ctx, incident.ID, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	first, ok, err := repo.ClaimNext(ctx, "worker-a", time.Unix(100, 0))
	if err != nil || !ok {
		t.Fatalf("first=%#v ok=%v err=%v", first, ok, err)
	}
	second, ok, err := repo.ClaimNext(ctx, "worker-b", time.Unix(401, 0))
	if err != nil || !ok {
		t.Fatalf("second=%#v ok=%v err=%v", second, ok, err)
	}

	err = repo.CompleteJobAndIncident(ctx, first, incident.ID, domain.RCAResult{Summary: "done"}, time.Unix(402, 0))
	if !errors.Is(err, ErrJobLeaseLost) {
		t.Fatalf("stale completion err=%v, want ErrJobLeaseLost", err)
	}
	assertLiveClaimUnchanged(t, repo, incident.ID, second)

	_, err = repo.RetryOrFailJob(ctx, first, time.Unix(402, 0), 3)
	if !errors.Is(err, ErrJobLeaseLost) {
		t.Fatalf("stale retry err=%v, want ErrJobLeaseLost", err)
	}
	assertLiveClaimUnchanged(t, repo, incident.ID, second)

	_, err = repo.RetryOrFailJob(ctx, first, time.Unix(402, 0), 1)
	if !errors.Is(err, ErrJobLeaseLost) {
		t.Fatalf("stale fail err=%v, want ErrJobLeaseLost", err)
	}
	assertLiveClaimUnchanged(t, repo, incident.ID, second)
}

func TestClaimFenceRejectsStalePendingAuditSchedule(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	incident, _, err := repo.CreateOrGetIncident(ctx, domain.NewIncident("ws", "rule-1", "i-audit-fence", time.Unix(100, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Enqueue(ctx, incident.ID, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	first, ok, err := repo.ClaimNext(ctx, "worker-a", time.Unix(100, 0))
	if err != nil || !ok {
		t.Fatalf("first=%#v ok=%v err=%v", first, ok, err)
	}
	second, ok, err := repo.ClaimNext(ctx, "worker-b", time.Unix(401, 0))
	if err != nil || !ok {
		t.Fatalf("second=%#v ok=%v err=%v", second, ok, err)
	}

	err = repo.CompleteJobAndScheduleAuditRetry(ctx, first, incident.ID, time.Unix(522, 0))
	if !errors.Is(err, ErrJobLeaseLost) {
		t.Fatalf("stale pending-audit err=%v, want ErrJobLeaseLost", err)
	}
	assertLiveClaimUnchanged(t, repo, incident.ID, second)
}

func TestActiveClaimCardFenceRejectsStaleLeaseBeforeCallback(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	incident, _, err := repo.CreateOrGetIncident(ctx, domain.NewIncident("ws", "rule-1", "i-card-fence", time.Unix(100, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Enqueue(ctx, incident.ID, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	first, ok, err := repo.ClaimNext(ctx, "worker-a", time.Unix(100, 0))
	if err != nil || !ok {
		t.Fatalf("first=%#v ok=%v err=%v", first, ok, err)
	}
	second, ok, err := repo.ClaimNext(ctx, "worker-b", time.Unix(401, 0))
	if err != nil || !ok {
		t.Fatalf("second=%#v ok=%v err=%v", second, ok, err)
	}

	claimCards, ok := any(repo).(interface {
		UpdateActiveClaimCard(context.Context, domain.Job, func(domain.Incident) error) error
	})
	if !ok {
		t.Fatal("repository does not implement UpdateActiveClaimCard")
	}
	called := false
	err = claimCards.UpdateActiveClaimCard(ctx, first, func(domain.Incident) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrJobLeaseLost) {
		t.Fatalf("stale card fence err=%v, want ErrJobLeaseLost", err)
	}
	if called {
		t.Fatal("stale card callback was called")
	}
	assertLiveClaimUnchanged(t, repo, incident.ID, second)
}

func assertLiveClaimUnchanged(t *testing.T, repo *SQLiteRepository, incidentID string, want domain.Job) {
	t.Helper()
	incident, err := repo.IncidentByID(context.Background(), incidentID)
	if err != nil || incident.State != domain.IncidentReceived {
		t.Fatalf("incident=%#v err=%v", incident, err)
	}
	var got domain.Job
	var runAfter, leaseUntil int64
	err = repo.db.QueryRow(`SELECT id, incident_id, status, worker_id, attempt, run_after, lease_until FROM jobs WHERE id = ?`, want.ID).
		Scan(&got.ID, &got.IncidentID, &got.Status, &got.WorkerID, &got.Attempt, &runAfter, &leaseUntil)
	if err != nil {
		t.Fatal(err)
	}
	got.RunAfter, got.LeaseUntil = time.Unix(runAfter, 0).UTC(), time.Unix(leaseUntil, 0).UTC()
	if got.ID != want.ID || got.Status != domain.JobRunning || got.WorkerID != want.WorkerID || !got.LeaseUntil.Equal(want.LeaseUntil) || got.Attempt != want.Attempt {
		t.Fatalf("live claim=%#v, want unchanged %#v", got, want)
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

func TestRecoverClosesCompletedOrFailedIncidentWithoutChangingTerminalJob(t *testing.T) {
	for _, terminal := range []struct {
		name       string
		wantStatus string
	}{
		{
			name:       "completed",
			wantStatus: domain.JobCompleted,
		},
		{
			name:       "failed",
			wantStatus: domain.JobFailed,
		},
	} {
		t.Run(terminal.name, func(t *testing.T) {
			repo := newTestRepo(t)
			ctx := context.Background()
			incident, _, err := repo.CreateOrGetIncident(ctx, domain.NewIncident("ws", "rule-1", "i-demo", time.Unix(10, 0)))
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.Enqueue(ctx, incident.ID, time.Unix(10, 0)); err != nil {
				t.Fatal(err)
			}
			job, ok, err := repo.ClaimNext(ctx, "worker-a", time.Unix(11, 0))
			if err != nil || !ok {
				t.Fatalf("job=%#v ok=%v err=%v", job, ok, err)
			}

			if terminal.name == "completed" {
				if err := repo.CompleteJobAndIncident(ctx, job, incident.ID, domain.RCAResult{Summary: "done"}, time.Unix(12, 0)); err != nil {
					t.Fatal(err)
				}
			} else if _, err := repo.RetryOrFailJob(ctx, job, time.Unix(12, 0), 1); err != nil {
				t.Fatal(err)
			}

			got, recovered, err := repo.Recover(ctx, incident.Key, time.Unix(20, 0))
			if err != nil || !recovered || got.State != domain.IncidentRecovered {
				t.Fatalf("incident=%#v recovered=%v err=%v", got, recovered, err)
			}
			if status := onlyJobStatus(t, repo, incident.ID); status != terminal.wantStatus {
				t.Fatalf("status=%s want=%s", status, terminal.wantStatus)
			}
		})
	}
}

func TestRecoveryDuplicateReturnsLatestRecoveredGenerationWithoutChangingOlderTerminalGeneration(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	first, _, err := repo.CreateOrGetIncident(ctx, domain.NewIncident("ws", "rule-1", "i-demo", time.Unix(10, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Enqueue(ctx, first.ID, time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	job, ok, err := repo.ClaimNext(ctx, "worker-a", time.Unix(11, 0))
	if err != nil || !ok {
		t.Fatalf("job=%#v ok=%v err=%v", job, ok, err)
	}
	if err := repo.CompleteJobAndIncident(ctx, job, first.ID, domain.RCAResult{Summary: "done"}, time.Unix(12, 0)); err != nil {
		t.Fatal(err)
	}
	second, _, err := repo.CreateOrGetIncident(ctx, domain.NewIncident("ws", "rule-1", "i-demo", time.Unix(30, 0)))
	if err != nil {
		t.Fatal(err)
	}
	if _, recovered, err := repo.Recover(ctx, second.Key, time.Unix(40, 0)); err != nil || !recovered {
		t.Fatalf("first recovery recovered=%v err=%v", recovered, err)
	}

	got, recovered, err := repo.Recover(ctx, second.Key, time.Unix(40, 0))
	if err != nil || recovered || got.ID != second.ID || got.State != domain.IncidentRecovered {
		t.Fatalf("duplicate got=%#v recovered=%v err=%v", got, recovered, err)
	}
	first, err = repo.IncidentByID(ctx, first.ID)
	if err != nil || first.State != domain.IncidentCompleted {
		t.Fatalf("first=%#v err=%v", first, err)
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
