package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jack/umodel-sre-rca/internal/cc"
	"github.com/jack/umodel-sre-rca/internal/domain"
	"github.com/jack/umodel-sre-rca/internal/messages"
	"github.com/jack/umodel-sre-rca/internal/store"
)

type Repository interface {
	ClaimNext(context.Context, string, time.Time) (domain.Job, bool, error)
	CheckActiveClaim(context.Context, domain.Job) error
	IncidentByID(context.Context, string) (domain.Incident, error)
	MarkInvestigatingForClaim(context.Context, domain.Job, time.Time) error
	SetFeishuMessageID(context.Context, string, string, time.Time) error
	SetFeishuMessageIDForClaim(context.Context, domain.Job, string, time.Time) error
	UpdateActiveClaimCard(context.Context, domain.Job, func(domain.Incident) error) error
	StoreEvidence(context.Context, string, []domain.Evidence, time.Time) error
	Complete(context.Context, string, domain.RCAResult, time.Time) error
	ScheduleAuditRetry(context.Context, string, time.Time) error
	CompleteJobAndIncident(context.Context, domain.Job, string, domain.RCAResult, time.Time) error
	CompleteJobAndScheduleAuditRetry(context.Context, domain.Job, string, time.Time) error
	RetryOrFailJob(context.Context, domain.Job, time.Time, int) (bool, error)
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
	Run(context.Context, string, []cc.EvidenceCollection) (domain.RCAResult, error)
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
		return w.persistenceFailure(ctx, job, err)
	}
	if incident.FeishuMessageID == "" {
		if err := w.repo.CheckActiveClaim(ctx, job); err != nil {
			if isInactive(err) {
				return nil
			}
			return w.persistenceFailure(ctx, job, err)
		}
		messageID, err := w.cards.CreateIncidentCard(ctx, incident)
		if err != nil {
			return w.fail(ctx, incident, job, err)
		}
		if err := w.repo.SetFeishuMessageIDForClaim(ctx, job, messageID, now); err != nil {
			if isInactive(err) {
				return w.finishRecoveredCardCreate(ctx, incident.ID, messageID, now)
			}
			return w.persistenceFailure(ctx, job, err)
		}
		incident, err = w.repo.IncidentByID(ctx, incident.ID)
		if err != nil {
			return w.persistenceFailure(ctx, job, err)
		}
		if incident.State == domain.IncidentRecovered {
			return w.cards.UpdateIncidentCard(ctx, incident.FeishuMessageID, incident, domain.RCAResult{Summary: messages.RecoverySummary})
		}
	}
	if err := w.repo.MarkInvestigatingForClaim(ctx, job, now); err != nil {
		if isInactive(err) {
			return nil
		}
		return w.persistenceFailure(ctx, job, err)
	}
	incident.State = domain.IncidentInvestigating
	if err := w.updateCard(ctx, job, incident, domain.RCAResult{}); err != nil {
		if isInactive(err) {
			return nil
		}
		return w.fail(ctx, incident, job, err)
	}

	collections, evidenceItems, err := w.collectEvidence(ctx, incident.ID)
	if err != nil {
		return w.fail(ctx, incident, job, err)
	}
	if err := w.repo.StoreEvidence(ctx, incident.ID, evidenceItems, now); err != nil {
		if isInactive(err) {
			return nil
		}
		return w.persistenceFailure(ctx, job, err)
	}
	result, err := w.runner.Run(ctx, incident.ID, collections)
	if err != nil {
		return w.fail(ctx, incident, job, err)
	}
	knownEvidence := make(map[string]bool, len(evidenceItems))
	for _, item := range evidenceItems {
		knownEvidence[item.ID] = true
	}
	if err := cc.ValidateResult(result, knownEvidence); err != nil {
		return w.fail(ctx, incident, job, err)
	}

	if result.PendingAudit {
		incident.State = domain.IncidentAwaitingAuditEvent
		if err := w.updateCard(ctx, job, incident, result); err != nil {
			if isInactive(err) {
				return nil
			}
			return w.fail(ctx, incident, job, err)
		}
		if err := w.repo.CompleteJobAndScheduleAuditRetry(ctx, job, incident.ID, now.Add(w.auditDelay)); err != nil {
			if isInactive(err) {
				return nil
			}
			return w.persistenceFailure(ctx, job, err)
		}
		return nil
	}
	incident.State = domain.IncidentCompleted
	if err := w.updateCard(ctx, job, incident, result); err != nil {
		if isInactive(err) {
			return nil
		}
		return w.fail(ctx, incident, job, err)
	}
	if err := w.repo.CompleteJobAndIncident(ctx, job, incident.ID, result, now); err != nil {
		if isInactive(err) {
			return nil
		}
		return w.persistenceFailure(ctx, job, err)
	}
	return nil
}

func (w *Worker) collectEvidence(ctx context.Context, incidentID string) ([]cc.EvidenceCollection, []domain.Evidence, error) {
	queries := []struct {
		form  string
		query func(context.Context, string) ([]domain.Evidence, error)
	}{
		{form: "incident context", query: w.evidence.Context},
		{form: "metrics query", query: w.evidence.Metrics},
		{form: "logs query", query: w.evidence.Logs},
		{form: "changes query", query: w.evidence.Changes},
	}
	collections := make([]cc.EvidenceCollection, 0, len(queries))
	var all []domain.Evidence
	for _, query := range queries {
		items, err := query.query(ctx, incidentID)
		if err != nil {
			return nil, nil, err
		}
		collections = append(collections, cc.EvidenceCollection{Form: query.form, Evidence: items})
		all = append(all, items...)
	}
	return collections, all, nil
}

func (w *Worker) fail(ctx context.Context, incident domain.Incident, job domain.Job, cause error) error {
	if job.Attempt >= 3 && incident.FeishuMessageID != "" {
		incident.State = domain.IncidentFailed
		if err := w.updateCard(ctx, job, incident, domain.RCAResult{Summary: messages.FailureSummary, NextActions: []string{messages.FailureAction}}); err != nil {
			if isInactive(err) {
				return nil
			}
			cause = fmt.Errorf("%v; update card: %w", cause, err)
		}
	}
	failed, err := w.repo.RetryOrFailJob(ctx, job, w.now().UTC(), 3)
	if isInactive(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("RCA failed (%v); could not persist retry: %w", cause, err)
	}
	if !failed {
		return fmt.Errorf("RCA failed: %w", cause)
	}
	return fmt.Errorf("RCA failed: %w", cause)
}

func (w *Worker) updateCard(ctx context.Context, job domain.Job, incident domain.Incident, result domain.RCAResult) error {
	return w.repo.UpdateActiveClaimCard(ctx, job, func(domain.Incident) error {
		return w.cards.UpdateIncidentCard(ctx, incident.FeishuMessageID, incident, result)
	})
}

func (w *Worker) finishRecoveredCardCreate(ctx context.Context, incidentID, messageID string, at time.Time) error {
	incident, err := w.repo.IncidentByID(ctx, incidentID)
	if err != nil || incident.State != domain.IncidentRecovered {
		return nil
	}
	if err := w.repo.SetFeishuMessageID(ctx, incidentID, messageID, at); err != nil {
		return err
	}
	incident, err = w.repo.IncidentByID(ctx, incidentID)
	if err != nil {
		return err
	}
	return w.cards.UpdateIncidentCard(ctx, incident.FeishuMessageID, incident, domain.RCAResult{Summary: messages.RecoverySummary})
}

func (w *Worker) persistenceFailure(ctx context.Context, job domain.Job, cause error) error {
	if isInactive(cause) {
		return nil
	}
	if _, err := w.repo.RetryOrFailJob(ctx, job, w.now().UTC(), 3); err != nil {
		if isInactive(err) {
			return nil
		}
		return fmt.Errorf("persist RCA retry after %v: %w", cause, err)
	}
	return fmt.Errorf("persist RCA: %w", cause)
}

func isInactive(err error) bool {
	return errors.Is(err, store.ErrIncidentInactive) || errors.Is(err, store.ErrJobLeaseLost)
}
