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
