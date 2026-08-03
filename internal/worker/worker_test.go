package worker

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jack/umodel-sre-rca/internal/cc"
	"github.com/jack/umodel-sre-rca/internal/domain"
	"github.com/jack/umodel-sre-rca/internal/store"
)

type fakeCards struct {
	creates int
	updates int
}

func (f *fakeCards) CreateIncidentCard(context.Context, domain.Incident) (string, error) {
	f.creates++
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

func (f fakeRunner) Run(context.Context, string, []cc.EvidenceCollection) (domain.RCAResult, error) {
	return f.result, nil
}

type recordingRunner struct {
	result      domain.RCAResult
	collections []cc.EvidenceCollection
}

func (r *recordingRunner) Run(_ context.Context, _ string, collections []cc.EvidenceCollection) (domain.RCAResult, error) {
	r.collections = collections
	return r.result, nil
}

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

func TestWorkerPassesTheStoredEvidenceSetToTheRunner(t *testing.T) {
	repo, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	runner := &recordingRunner{result: validResult()}
	w := New(repo, &fakeCards{}, fakeEvidence{}, runner, "worker-a", func() time.Time { return time.Unix(200, 0) })
	_ = enqueueFixtureIncident(t, repo)

	if err := w.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(runner.collections) != 4 {
		t.Fatalf("collection count=%d want=4", len(runner.collections))
	}
	if got := runner.collections[0].Evidence[0].ID; got != "ev-context" {
		t.Fatalf("context evidence id=%q want=ev-context", got)
	}
	if got := runner.collections[3].Evidence[0].ID; got != "ev-change" {
		t.Fatalf("change evidence id=%q want=ev-change", got)
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

func TestWorkerClaimFenceSuppressesStaleCardUpdates(t *testing.T) {
	tests := []struct {
		name        string
		result      domain.RCAResult
		reclaimOn   int
		primeFailed bool
		wantEvents  []string
	}{
		{name: "investigating", result: validResult(), reclaimOn: 1},
		{name: "completed", result: validResult(), reclaimOn: 2, wantEvents: []string{domain.IncidentInvestigating}},
		{name: "pending audit", result: domain.RCAResult{Summary: "audit pending", RootCause: "security group", Confidence: 0.5, EvidenceIDs: []string{"ev-context"}, PendingAudit: true}, reclaimOn: 2, wantEvents: []string{domain.IncidentInvestigating}},
		{name: "failed", result: domain.RCAResult{Summary: "invalid", EvidenceIDs: []string{"unknown"}}, reclaimOn: 2, primeFailed: true, wantEvents: []string{domain.IncidentInvestigating}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, err := store.Open(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = base.Close() })
			incident := enqueueFixtureIncident(t, base)
			if tt.primeFailed {
				primeJobAttempt(t, base)
			}
			repo := &reclaimBeforeCardRepo{SQLiteRepository: base, reclaimOn: tt.reclaimOn}
			cards := &recordingCards{}
			w := New(repo, cards, fakeEvidence{}, fakeRunner{result: tt.result}, "worker-a", func() time.Time { return time.Unix(200, 0) })
			if err := w.RunOne(context.Background()); err != nil {
				t.Fatalf("RunOne returned stale-claim error: %v", err)
			}
			if got := cards.eventsSnapshot(); !equalStrings(got, tt.wantEvents) {
				t.Fatalf("card events=%v want=%v", got, tt.wantEvents)
			}
			if repo.reclaimed.ID == "" {
				t.Fatal("worker-b did not reclaim worker-a's expired lease")
			}
			got := mustIncidentByID(t, base, incident.ID)
			if got.State != repo.incidentAtReclaim.State || got.FeishuMessageID != repo.incidentAtReclaim.FeishuMessageID {
				t.Fatalf("incident changed after reclaim: got=%#v at_reclaim=%#v", got, repo.incidentAtReclaim)
			}
			if err := base.CompleteJob(context.Background(), repo.reclaimed); err != nil {
				t.Fatalf("worker-b claim changed after stale card attempt: %v", err)
			}
		})
	}
}

func TestWorkerChecksLiveClaimBeforeCreatingCard(t *testing.T) {
	base, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	incident := enqueueFixtureIncident(t, base)
	repo := &reclaimBeforeCreateRepo{SQLiteRepository: base}
	cards := &fakeCards{}
	w := New(repo, cards, fakeEvidence{}, fakeRunner{result: validResult()}, "worker-a", func() time.Time { return time.Unix(200, 0) })
	if err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne returned stale-claim error: %v", err)
	}
	if cards.creates != 0 || cards.updates != 0 {
		t.Fatalf("stale worker card calls: creates=%d updates=%d", cards.creates, cards.updates)
	}
	got := mustIncidentByID(t, base, incident.ID)
	if got.State != domain.IncidentReceived || got.FeishuMessageID != "" {
		t.Fatalf("incident changed after reclaim: %#v", got)
	}
	if err := base.CompleteJob(context.Background(), repo.reclaimed); err != nil {
		t.Fatalf("worker-b claim changed after stale create attempt: %v", err)
	}
}

func TestWorkerLeaseFenceStopsBeforeInvestigatingAfterReclaim(t *testing.T) {
	base, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = base.Close() })
	incident := enqueueFixtureIncident(t, base)
	if err := base.SetFeishuMessageID(context.Background(), incident.ID, "om-existing", time.Unix(101, 0)); err != nil {
		t.Fatal(err)
	}
	repo := &reclaimBeforeInvestigatingRepo{SQLiteRepository: base}
	cards := &recordingCards{}
	w := New(repo, cards, fakeEvidence{}, fakeRunner{result: validResult()}, "worker-a", func() time.Time { return time.Unix(200, 0) })
	if err := w.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne returned stale-claim error: %v", err)
	}
	if events := cards.eventsSnapshot(); len(events) != 0 {
		t.Fatalf("card events=%v, want none after stale claim", events)
	}
	got := mustIncidentByID(t, base, incident.ID)
	if got.State != domain.IncidentReceived {
		t.Fatalf("incident state=%s, want %s", got.State, domain.IncidentReceived)
	}
	if err := base.CheckActiveClaim(context.Background(), repo.reclaimed); err != nil {
		t.Fatalf("worker-b claim changed after stale investigating attempt: %v", err)
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

	allowRecovery := make(chan struct{})
	recovered := make(chan error, 1)
	go func() {
		<-allowRecovery
		got, ok, err := base.Recover(context.Background(), incident.Key, time.Unix(201, 0))
		if err == nil && ok {
			err = cards.UpdateIncidentCard(context.Background(), got.FeishuMessageID, got, domain.RCAResult{Summary: "CloudMonitor alert recovered."})
		}
		recovered <- err
	}()
	close(cards.releaseWorkerUpdate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	close(allowRecovery)
	if err := <-recovered; err != nil {
		t.Fatal(err)
	}
	events := cards.eventsSnapshot()
	if len(events) != 3 || events[0] != domain.IncidentInvestigating || events[1] != domain.IncidentCompleted || events[2] != domain.IncidentRecovered {
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

func (markInvestigatingFailureRepo) MarkInvestigatingForClaim(context.Context, domain.Job, time.Time) error {
	return errors.New("write unavailable")
}

type crashAfterCompleteJobRepo struct{ *store.SQLiteRepository }

func (r crashAfterCompleteJobRepo) CompleteJobAndIncident(ctx context.Context, job domain.Job, incidentID string, result domain.RCAResult, at time.Time) error {
	if err := r.SQLiteRepository.CompleteJobAndIncident(ctx, job, incidentID, result, at); err != nil {
		return err
	}
	panic("simulated process crash after terminal transaction")
}

func (r crashAfterCompleteJobRepo) CompleteJobAndScheduleAuditRetry(ctx context.Context, job domain.Job, incidentID string, retryAt time.Time) error {
	if err := r.SQLiteRepository.CompleteJobAndScheduleAuditRetry(ctx, job, incidentID, retryAt); err != nil {
		return err
	}
	panic("simulated process crash after terminal transaction")
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

type reclaimBeforeCardRepo struct {
	*store.SQLiteRepository
	reclaimOn         int
	updates           int
	reclaimed         domain.Job
	incidentAtReclaim domain.Incident
}

type reclaimBeforeCreateRepo struct {
	*store.SQLiteRepository
	reclaimed domain.Job
}

type reclaimBeforeInvestigatingRepo struct {
	*store.SQLiteRepository
	reclaimed domain.Job
}

func (r *reclaimBeforeInvestigatingRepo) MarkInvestigatingForClaim(ctx context.Context, job domain.Job, at time.Time) error {
	reclaimed, ok, err := r.SQLiteRepository.ClaimNext(ctx, "worker-b", time.Unix(501, 0))
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("worker-b could not reclaim expired lease")
	}
	r.reclaimed = reclaimed
	return r.SQLiteRepository.MarkInvestigatingForClaim(ctx, job, at)
}

func (r *reclaimBeforeCreateRepo) CheckActiveClaim(ctx context.Context, job domain.Job) error {
	reclaimed, ok, err := r.SQLiteRepository.ClaimNext(ctx, "worker-b", time.Unix(501, 0))
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("worker-b could not reclaim expired lease")
	}
	r.reclaimed = reclaimed
	return r.SQLiteRepository.CheckActiveClaim(ctx, job)
}

func (r *reclaimBeforeCardRepo) beforeCard(ctx context.Context) error {
	r.updates++
	if r.updates != r.reclaimOn {
		return nil
	}
	job, ok, err := r.SQLiteRepository.ClaimNext(ctx, "worker-b", time.Unix(501, 0))
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("worker-b could not reclaim expired lease")
	}
	r.reclaimed = job
	r.incidentAtReclaim, err = r.SQLiteRepository.IncidentByID(ctx, job.IncidentID)
	return err
}

func (r *reclaimBeforeCardRepo) UpdateActiveClaimCard(ctx context.Context, job domain.Job, update func(domain.Incident) error) error {
	if err := r.beforeCard(ctx); err != nil {
		return err
	}
	claimCards, ok := any(r.SQLiteRepository).(interface {
		UpdateActiveClaimCard(context.Context, domain.Job, func(domain.Incident) error) error
	})
	if !ok {
		return errors.New("repository does not implement UpdateActiveClaimCard")
	}
	return claimCards.UpdateActiveClaimCard(ctx, job, update)
}

func primeJobAttempt(t *testing.T, repo *store.SQLiteRepository) {
	t.Helper()
	ctx := context.Background()
	job, ok, err := repo.ClaimNext(ctx, "primer", time.Unix(101, 0))
	if err != nil || !ok {
		t.Fatalf("first prime claim=%#v ok=%v err=%v", job, ok, err)
	}
	if _, err := repo.RetryOrFailJob(ctx, job, time.Unix(102, 0), 3); err != nil {
		t.Fatal(err)
	}
	job, ok, err = repo.ClaimNext(ctx, "primer", time.Unix(107, 0))
	if err != nil || !ok {
		t.Fatalf("second prime claim=%#v ok=%v err=%v", job, ok, err)
	}
	if _, err := repo.RetryOrFailJob(ctx, job, time.Unix(108, 0), 3); err != nil {
		t.Fatal(err)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func (r *blockingMessageIDRepo) SetFeishuMessageIDForClaim(ctx context.Context, job domain.Job, messageID string, at time.Time) error {
	close(r.setEntered)
	<-r.releaseSet
	return r.SQLiteRepository.SetFeishuMessageIDForClaim(ctx, job, messageID, at)
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
