package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jack/umodel-sre-rca/internal/domain"
	_ "modernc.org/sqlite"
)

type SQLiteRepository struct{ db *sql.DB }

// Repository is the persistence boundary used by inbound handlers and workers.
// It deliberately exposes domain operations instead of raw SQL state updates.
type Repository interface {
	CreateOrGetIncident(context.Context, domain.Incident) (domain.Incident, bool, error)
	IncidentByID(context.Context, string) (domain.Incident, error)
	Enqueue(context.Context, string, time.Time) error
	ClaimNext(context.Context, string, time.Time) (domain.Job, bool, error)
	MarkInvestigating(context.Context, string, time.Time) error
	SetFeishuMessageID(context.Context, string, string, time.Time) error
	StoreEvidence(context.Context, string, []domain.Evidence, time.Time) error
	OpenEvidence(context.Context, string) (domain.Evidence, error)
	Complete(context.Context, string, domain.RCAResult, time.Time) error
	Fail(context.Context, string, time.Time) error
	ScheduleAuditRetry(context.Context, string, time.Time) error
	MarkRecovered(context.Context, string, time.Time) error
}

func Open(path string) (*SQLiteRepository, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	repo := &SQLiteRepository{db: db}
	if err := repo.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return repo, nil
}

func (r *SQLiteRepository) Close() error { return r.db.Close() }

func (r *SQLiteRepository) migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS incidents (id TEXT PRIMARY KEY, incident_key TEXT NOT NULL, workspace TEXT NOT NULL, rule_id TEXT NOT NULL, resource_id TEXT NOT NULL, state TEXT NOT NULL, feishu_message_id TEXT NOT NULL DEFAULT '', alert_at INTEGER NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS incidents_active_key ON incidents(incident_key) WHERE state IN ('RECEIVED','INVESTIGATING','AWAITING_AUDIT_EVENT')`,
		`CREATE TABLE IF NOT EXISTS jobs (id TEXT PRIMARY KEY, incident_id TEXT NOT NULL, status TEXT NOT NULL, worker_id TEXT NOT NULL DEFAULT '', attempt INTEGER NOT NULL DEFAULT 0, run_after INTEGER NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS jobs_claimable ON jobs(status, run_after)`,
		`CREATE TABLE IF NOT EXISTS evidence_snapshots (id TEXT PRIMARY KEY, incident_id TEXT NOT NULL, evidence_json TEXT NOT NULL, created_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS evidence_records (id TEXT PRIMARY KEY, incident_id TEXT NOT NULL, evidence_json TEXT NOT NULL, created_at INTEGER NOT NULL)`,
	}
	for _, statement := range statements {
		if _, err := r.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (r *SQLiteRepository) CreateOrGetIncident(ctx context.Context, in domain.Incident) (domain.Incident, bool, error) {
	_, err := r.db.ExecContext(ctx, `INSERT INTO incidents (id, incident_key, workspace, rule_id, resource_id, state, alert_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, in.ID, in.Key, in.Workspace, in.RuleID, in.ResourceID, in.State, in.AlertAt.Unix(), in.CreatedAt.Unix(), in.UpdatedAt.Unix())
	if err == nil {
		return in, true, nil
	}
	row := r.db.QueryRowContext(ctx, `SELECT id, incident_key, workspace, rule_id, resource_id, state, feishu_message_id, alert_at, created_at, updated_at FROM incidents WHERE incident_key = ? AND state IN ('RECEIVED','INVESTIGATING','AWAITING_AUDIT_EVENT')`, in.Key)
	got, scanErr := scanIncident(row)
	if scanErr != nil {
		return domain.Incident{}, false, fmt.Errorf("insert incident: %w; read active incident: %v", err, scanErr)
	}
	return got, false, nil
}

func (r *SQLiteRepository) Enqueue(ctx context.Context, incidentID string, runAfter time.Time) error {
	job := domain.NewJob(incidentID, runAfter)
	_, err := r.db.ExecContext(ctx, `INSERT INTO jobs (id, incident_id, status, run_after) VALUES (?, ?, ?, ?)`, job.ID, job.IncidentID, job.Status, job.RunAfter.Unix())
	return err
}

func (r *SQLiteRepository) IncidentByID(ctx context.Context, id string) (domain.Incident, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id, incident_key, workspace, rule_id, resource_id, state, feishu_message_id, alert_at, created_at, updated_at FROM incidents WHERE id = ?`, id)
	in, err := scanIncident(row)
	if err != nil {
		return domain.Incident{}, err
	}
	return in, nil
}

func (r *SQLiteRepository) MarkInvestigating(ctx context.Context, incidentID string, at time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE incidents SET state = ?, updated_at = ? WHERE id = ? AND state IN ('RECEIVED','AWAITING_AUDIT_EVENT')`, domain.IncidentInvestigating, at.Unix(), incidentID)
	if err != nil {
		return err
	}
	return oneRow(result, "mark investigating", incidentID)
}

func (r *SQLiteRepository) SetFeishuMessageID(ctx context.Context, incidentID, messageID string, at time.Time) error {
	if messageID == "" {
		return fmt.Errorf("Feishu message id is required")
	}
	result, err := r.db.ExecContext(ctx, `UPDATE incidents SET feishu_message_id = ?, updated_at = ? WHERE id = ?`, messageID, at.Unix(), incidentID)
	if err != nil {
		return err
	}
	return oneRow(result, "set Feishu message id", incidentID)
}

func (r *SQLiteRepository) StoreEvidence(ctx context.Context, incidentID string, evidence []domain.Evidence, at time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, item := range evidence {
		if item.ID == "" {
			return fmt.Errorf("evidence id is required")
		}
		raw, err := json.Marshal(item)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO evidence_records (id, incident_id, evidence_json, created_at) VALUES (?, ?, ?, ?)`, item.ID, incidentID, string(raw), at.Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *SQLiteRepository) OpenEvidence(ctx context.Context, evidenceID string) (domain.Evidence, error) {
	var raw string
	if err := r.db.QueryRowContext(ctx, `SELECT evidence_json FROM evidence_records WHERE id = ?`, evidenceID).Scan(&raw); err != nil {
		return domain.Evidence{}, err
	}
	var item domain.Evidence
	if err := json.Unmarshal([]byte(raw), &item); err != nil {
		return domain.Evidence{}, fmt.Errorf("decode stored evidence: %w", err)
	}
	return item, nil
}

func (r *SQLiteRepository) Complete(ctx context.Context, incidentID string, result domain.RCAResult, at time.Time) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal RCA result: %w", err)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	updated, err := tx.ExecContext(ctx, `UPDATE incidents SET state = ?, updated_at = ? WHERE id = ? AND state IN ('RECEIVED','INVESTIGATING','AWAITING_AUDIT_EVENT')`, domain.IncidentCompleted, at.Unix(), incidentID)
	if err != nil {
		return err
	}
	if err := oneRow(updated, "complete incident", incidentID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO evidence_snapshots (id, incident_id, evidence_json, created_at) VALUES (?, ?, ?, ?)`, domainSnapshotID(), incidentID, string(raw), at.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *SQLiteRepository) Fail(ctx context.Context, incidentID string, at time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE incidents SET state = ?, updated_at = ? WHERE id = ? AND state IN ('RECEIVED','INVESTIGATING','AWAITING_AUDIT_EVENT')`, domain.IncidentFailed, at.Unix(), incidentID)
	if err != nil {
		return err
	}
	return oneRow(result, "fail incident", incidentID)
}

func (r *SQLiteRepository) ScheduleAuditRetry(ctx context.Context, incidentID string, retryAt time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	updated, err := tx.ExecContext(ctx, `UPDATE incidents SET state = ?, updated_at = ? WHERE id = ? AND state IN ('RECEIVED','INVESTIGATING','AWAIT_AUDIT_EVENT','AWAITING_AUDIT_EVENT')`, domain.IncidentAwaitingAuditEvent, retryAt.Unix(), incidentID)
	if err != nil {
		return err
	}
	if err := oneRow(updated, "schedule audit retry", incidentID); err != nil {
		return err
	}
	job := domain.NewJob(incidentID, retryAt)
	if _, err := tx.ExecContext(ctx, `INSERT INTO jobs (id, incident_id, status, run_after) VALUES (?, ?, ?, ?)`, job.ID, job.IncidentID, job.Status, job.RunAfter.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *SQLiteRepository) MarkRecovered(ctx context.Context, incidentKey string, at time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE incidents SET state = ?, updated_at = ? WHERE incident_key = ? AND state IN ('RECEIVED','INVESTIGATING','AWAITING_AUDIT_EVENT')`, domain.IncidentRecovered, at.Unix(), incidentKey)
	if err != nil {
		return err
	}
	return oneRow(result, "recover incident", incidentKey)
}

func (r *SQLiteRepository) ClaimNext(ctx context.Context, workerID string, now time.Time) (domain.Job, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.Job{}, false, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `SELECT id, incident_id, status, worker_id, attempt, run_after FROM jobs WHERE status = ? AND run_after <= ? ORDER BY run_after, id LIMIT 1`, domain.JobQueued, now.Unix())
	job, err := scanJob(row)
	if err == sql.ErrNoRows {
		return domain.Job{}, false, tx.Commit()
	}
	if err != nil {
		return domain.Job{}, false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET status = ?, worker_id = ?, attempt = attempt + 1 WHERE id = ? AND status = ?`, domain.JobRunning, workerID, job.ID, domain.JobQueued)
	if err != nil {
		return domain.Job{}, false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return domain.Job{}, false, err
	}
	job.Status, job.WorkerID, job.Attempt = domain.JobRunning, workerID, job.Attempt+1
	return job, true, tx.Commit()
}

func scanIncident(row interface{ Scan(...any) error }) (domain.Incident, error) {
	var in domain.Incident
	var alertAt, createdAt, updatedAt int64
	err := row.Scan(&in.ID, &in.Key, &in.Workspace, &in.RuleID, &in.ResourceID, &in.State, &in.FeishuMessageID, &alertAt, &createdAt, &updatedAt)
	in.AlertAt, in.CreatedAt, in.UpdatedAt = time.Unix(alertAt, 0).UTC(), time.Unix(createdAt, 0).UTC(), time.Unix(updatedAt, 0).UTC()
	return in, err
}

func scanJob(row interface{ Scan(...any) error }) (domain.Job, error) {
	var job domain.Job
	var runAfter int64
	err := row.Scan(&job.ID, &job.IncidentID, &job.Status, &job.WorkerID, &job.Attempt, &runAfter)
	job.RunAfter = time.Unix(runAfter, 0).UTC()
	return job, err
}

func domainSnapshotID() string {
	// A snapshot is an immutable event. Reusing the same cryptographically-random
	// ID shape as the domain records avoids user-controlled filenames or IDs.
	return domain.NewJob("snapshot", time.Unix(0, 0)).ID
}

func oneRow(result sql.Result, operation, id string) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("%s %s: %w", operation, id, sql.ErrNoRows)
	}
	return nil
}
