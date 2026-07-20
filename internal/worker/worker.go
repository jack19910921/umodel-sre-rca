package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/jack/umodel-sre-rca/internal/cc"
	"github.com/jack/umodel-sre-rca/internal/domain"
)

type Repository interface {
	ClaimNext(context.Context, string, time.Time) (domain.Job, bool, error)
	IncidentByID(context.Context, string) (domain.Incident, error)
	MarkInvestigating(context.Context, string, time.Time) error
	SetFeishuMessageID(context.Context, string, string, time.Time) error
	StoreEvidence(context.Context, string, []domain.Evidence, time.Time) error
	Complete(context.Context, string, domain.RCAResult, time.Time) error
	ScheduleAuditRetry(context.Context, string, time.Time) error
	Fail(context.Context, string, time.Time) error
}

type Cards interface {
	CreateIncidentCard(context.Context, domain.Incident) (string, error)
	UpdateIncidentCard(context.Context, string, domain.Incident, domain.RCAResult) error
}

type Evidence interface {
	Context(context.Context, string) ([]domain.Evidence, error)
	Metrics(context.Context, string) ([]domain.Evidence, error)
	Logs(context.Context, string) ([]domain.Evidence, error)
	Changes(context.Context, string) ([]domain.Evidence, error)
}

type Investigator interface {
	Run(context.Context, string) (domain.RCAResult, error)
}

type Worker struct {
	repo       Repository
	cards      Cards
	evidence   Evidence
	runner     Investigator
	workerID   string
	now        func() time.Time
	auditDelay time.Duration
}

func New(repo Repository, cards Cards, evidence Evidence, runner Investigator, workerID string, now func() time.Time) *Worker {
	if now == nil {
		now = time.Now
	}
	return &Worker{repo: repo, cards: cards, evidence: evidence, runner: runner, workerID: workerID, now: now, auditDelay: 120 * time.Second}
}

// RunOne does no polling or HTTP work. It processes at most one atomically
// claimed job, which makes systemd restart behavior straightforward.
func (w *Worker) RunOne(ctx context.Context) error {
	now := w.now().UTC()
	job, ok, err := w.repo.ClaimNext(ctx, w.workerID, now)
	if err != nil || !ok {
		return err
	}
	incident, err := w.repo.IncidentByID(ctx, job.IncidentID)
	if err != nil {
		return err
	}
	if incident.FeishuMessageID == "" {
		messageID, err := w.cards.CreateIncidentCard(ctx, incident)
		if err != nil {
			return err
		}
		if err := w.repo.SetFeishuMessageID(ctx, incident.ID, messageID, now); err != nil {
			return err
		}
		incident.FeishuMessageID = messageID
	}
	if err := w.repo.MarkInvestigating(ctx, incident.ID, now); err != nil {
		return err
	}
	incident.State = domain.IncidentInvestigating
	if err := w.cards.UpdateIncidentCard(ctx, incident.FeishuMessageID, incident, domain.RCAResult{}); err != nil {
		return err
	}

	evidenceItems, err := w.collectEvidence(ctx, incident.ID)
	if err != nil {
		return w.fail(ctx, incident, err)
	}
	if err := w.repo.StoreEvidence(ctx, incident.ID, evidenceItems, now); err != nil {
		return w.fail(ctx, incident, err)
	}
	result, err := w.runner.Run(ctx, incident.ID)
	if err != nil {
		return w.fail(ctx, incident, err)
	}
	knownEvidence := make(map[string]bool, len(evidenceItems))
	for _, item := range evidenceItems {
		knownEvidence[item.ID] = true
	}
	if err := cc.ValidateResult(result, knownEvidence); err != nil {
		return w.fail(ctx, incident, err)
	}

	if result.PendingAudit {
		if err := w.repo.ScheduleAuditRetry(ctx, incident.ID, now.Add(w.auditDelay)); err != nil {
			return err
		}
		incident.State = domain.IncidentAwaitingAuditEvent
		return w.cards.UpdateIncidentCard(ctx, incident.FeishuMessageID, incident, result)
	}
	if err := w.repo.Complete(ctx, incident.ID, result, now); err != nil {
		return err
	}
	incident.State = domain.IncidentCompleted
	return w.cards.UpdateIncidentCard(ctx, incident.FeishuMessageID, incident, result)
}

func (w *Worker) collectEvidence(ctx context.Context, incidentID string) ([]domain.Evidence, error) {
	queries := []func(context.Context, string) ([]domain.Evidence, error){w.evidence.Context, w.evidence.Metrics, w.evidence.Logs, w.evidence.Changes}
	var all []domain.Evidence
	for _, query := range queries {
		items, err := query(ctx, incidentID)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
	}
	return all, nil
}

func (w *Worker) fail(ctx context.Context, incident domain.Incident, cause error) error {
	if err := w.repo.Fail(ctx, incident.ID, w.now().UTC()); err != nil {
		return fmt.Errorf("RCA failed (%v); could not persist failure: %w", cause, err)
	}
	incident.State = domain.IncidentFailed
	if updateErr := w.cards.UpdateIncidentCard(ctx, incident.FeishuMessageID, incident, domain.RCAResult{Summary: "RCA could not complete; review gateway logs and retry.", NextActions: []string{"Review the gateway error and retry the incident"}}); updateErr != nil {
		return fmt.Errorf("RCA failed: %v; update card: %w", cause, updateErr)
	}
	return fmt.Errorf("RCA failed: %w", cause)
}
