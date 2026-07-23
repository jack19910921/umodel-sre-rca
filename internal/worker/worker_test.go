package worker

import (
	"context"
	"errors"
	"sync"
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

func TestWorkerCompletionTransitionSurvivesCrashAfterCurrentJobCompletion(t *testing.T) {
	base, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	incident := enqueueFixtureIncident(t, base)
	repo := crashAfterCompleteJobRepo{SQLiteRepository: base}
	w := New(repo, &fakeCards{}, fakeEvidence{}, fakeRunner{result: validResult()}, "worker-a", func() time.Time { return time.Unix(200, 0) })
	_ = runWorkerSafely(w)
	got, err := base.IncidentByID(context.Background(), incident.ID)
	if err != nil || got.State != domain.IncidentCompleted {
		t.Fatalf("incident=%#v err=%v", got, err)
	}
	if _, ok, err := base.ClaimNext(context.Background(), "worker-b", time.Unix(210, 0)); err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestWorkerAuditTransitionSurvivesCrashAfterCurrentJobCompletion(t *testing.T) {
	base, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	incident := enqueueFixtureIncident(t, base)
	repo := crashAfterCompleteJobRepo{SQLiteRepository: base}
	result := domain.RCAResult{Summary: "audit event pending", RootCause: "security group", Confidence: 0.5, EvidenceIDs: []string{"ev-context"}, PendingAudit: true}
	w := New(repo, &fakeCards{}, fakeEvidence{}, fakeRunner{result: result}, "worker-a", func() time.Time { return time.Unix(200, 0) })
	_ = runWorkerSafely(w)
	got, err := base.IncidentByID(context.Background(), incident.ID)
	if err != nil || got.State != domain.IncidentAwaitingAuditEvent {
		t.Fatalf("incident=%#v err=%v", got, err)
	}
	job, ok, err := base.ClaimNext(context.Background(), "worker-b", time.Unix(320, 0))
	if err != nil || !ok || job.IncidentID != incident.ID || job.Attempt != 1 {
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

func TestWorkerRetriesClaimedJobWhenPersistenceFails(t *testing.T) {
	base, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	incident := enqueueFixtureIncident(t, base)
	cards := &fakeCards{}
	w := New(markInvestigatingFailureRepo{SQLiteRepository: base}, cards, fakeEvidence{}, fakeRunner{result: validResult()}, "worker-a", func() time.Time { return time.Unix(200, 0) })
	if err := w.RunOne(context.Background()); err == nil {
		t.Fatal("RunOne succeeded after MarkInvestigating persistence failure")
	}
	job, ok, err := base.ClaimNext(context.Background(), "worker-b", time.Unix(210, 0))
	if err != nil || !ok || job.IncidentID != incident.ID || job.Attempt != 2 {
		t.Fatalf("job=%#v ok=%v err=%v", job, ok, err)
	}
}

func TestWorkerCardFenceLetsRecoveryWin(t *testing.T) {
	base, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	incident := enqueueFixtureIncident(t, base)
	cards := newBlockingCards()
	w := New(base, cards, fakeEvidence{}, fakeRunner{result: validResult()}, "worker-a", func() time.Time { return time.Unix(200, 0) })
	done := make(chan error, 1)
	go func() { done <- w.RunOne(context.Background()) }()
	<-cards.workerUpdateStarted

	recovered := make(chan error, 1)
	go func() {
		got, ok, err := base.Recover(context.Background(), incident.Key, time.Unix(201, 0))
		if err == nil && ok {
			err = cards.UpdateIncidentCard(context.Background(), got.FeishuMessageID, got, domain.RCAResult{Summary: "CloudMonitor alert recovered."})
		}
		recovered <- err
	}()
	close(cards.releaseWorkerUpdate)
	if err := <-recovered; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	events := cards.eventsSnapshot()
	if len(events) != 2 || events[0] != domain.IncidentInvestigating || events[1] != domain.IncidentRecovered {
		t.Fatalf("card events=%v", events)
	}
}

func TestWorkerCreatesRecoveredCardWhenRecoveryWinsBeforeMessageIDPersistence(t *testing.T) {
	base, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	incident := enqueueFixtureIncident(t, base)
	repo := &blockingMessageIDRepo{SQLiteRepository: base, setEntered: make(chan struct{}), releaseSet: make(chan struct{})}
	cards := &recordingCards{}
	w := New(repo, cards, fakeEvidence{}, fakeRunner{result: validResult()}, "worker-a", func() time.Time { return time.Unix(200, 0) })
	done := make(chan error, 1)
	go func() { done <- w.RunOne(context.Background()) }()
	<-repo.setEntered
	if _, ok, err := base.Recover(context.Background(), incident.Key, time.Unix(201, 0)); err != nil || !ok {
		t.Fatalf("recovered=%v err=%v", ok, err)
	}
	close(repo.releaseSet)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if events := cards.eventsSnapshot(); len(events) != 1 || events[0] != domain.IncidentRecovered {
		t.Fatalf("card events=%v", events)
	}
	got := mustIncidentByID(t, base, incident.ID)
	if got.FeishuMessageID != "om-demo" || got.State != domain.IncidentRecovered {
		t.Fatalf("incident=%#v", got)
	}
}

type markInvestigatingFailureRepo struct{ *store.SQLiteRepository }

func (markInvestigatingFailureRepo) MarkInvestigating(context.Context, string, time.Time) error {
	return errors.New("write unavailable")
}

type crashAfterCompleteJobRepo struct{ *store.SQLiteRepository }

func (r crashAfterCompleteJobRepo) CompleteJob(ctx context.Context, jobID string) error {
	if err := r.SQLiteRepository.CompleteJob(ctx, jobID); err != nil {
		return err
	}
	panic("simulated process crash after completing current job")
}

func runWorkerSafely(w *Worker) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("simulated process crash")
		}
	}()
	return w.RunOne(context.Background())
}

type blockingMessageIDRepo struct {
	*store.SQLiteRepository
	setEntered chan struct{}
	releaseSet chan struct{}
}

func (r *blockingMessageIDRepo) SetFeishuMessageID(ctx context.Context, incidentID, messageID string, at time.Time) error {
	close(r.setEntered)
	<-r.releaseSet
	return r.SQLiteRepository.SetFeishuMessageID(ctx, incidentID, messageID, at)
}

type blockingCards struct {
	mu                  sync.Mutex
	events              []string
	workerUpdateStarted chan struct{}
	releaseWorkerUpdate chan struct{}
	startedWorkerUpdate bool
}

func newBlockingCards() *blockingCards {
	return &blockingCards{workerUpdateStarted: make(chan struct{}), releaseWorkerUpdate: make(chan struct{})}
}

func (c *blockingCards) CreateIncidentCard(context.Context, domain.Incident) (string, error) {
	return "om-demo", nil
}

func (c *blockingCards) UpdateIncidentCard(_ context.Context, _ string, incident domain.Incident, _ domain.RCAResult) error {
	if incident.State == domain.IncidentInvestigating {
		c.mu.Lock()
		if !c.startedWorkerUpdate {
			c.startedWorkerUpdate = true
			c.mu.Unlock()
			close(c.workerUpdateStarted)
			<-c.releaseWorkerUpdate
			c.mu.Lock()
		}
		c.events = append(c.events, incident.State)
		c.mu.Unlock()
		return nil
	}
	c.mu.Lock()
	c.events = append(c.events, incident.State)
	c.mu.Unlock()
	return nil
}

func (c *blockingCards) eventsSnapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.events...)
}

type recordingCards struct {
	mu     sync.Mutex
	events []string
}

func (c *recordingCards) CreateIncidentCard(context.Context, domain.Incident) (string, error) {
	return "om-demo", nil
}

func (c *recordingCards) UpdateIncidentCard(_ context.Context, _ string, incident domain.Incident, _ domain.RCAResult) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, incident.State)
	return nil
}

func (c *recordingCards) eventsSnapshot() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.events...)
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
