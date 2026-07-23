package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"
)

const (
	IncidentReceived           = "RECEIVED"
	IncidentInvestigating      = "INVESTIGATING"
	IncidentAwaitingAuditEvent = "AWAITING_AUDIT_EVENT"
	IncidentCompleted          = "COMPLETED"
	IncidentFailed             = "FAILED"
	IncidentRecovered          = "RECOVERED"
	JobQueued                  = "QUEUED"
	JobRunning                 = "RUNNING"
	JobCompleted               = "COMPLETED"
	JobCancelled               = "CANCELLED"
	JobFailed                  = "FAILED"
)

type Alert struct {
	Workspace  string
	RuleID     string
	ResourceID string
	State      string
	EventAt    time.Time
}

type Incident struct {
	ID              string
	Key             string
	Workspace       string
	RuleID          string
	ResourceID      string
	State           string
	FeishuMessageID string
	AlertAt         time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Job struct {
	ID         string
	IncidentID string
	Status     string
	WorkerID   string
	Attempt    int
	RunAfter   time.Time
}

type Evidence struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	ObservedAt time.Time `json:"observed_at"`
	Source     string    `json:"source"`
	QueryRef   string    `json:"query_ref"`
	Summary    string    `json:"summary"`
	RawRef     string    `json:"raw_ref"`
}

type RCAResult struct {
	Summary      string   `json:"summary"`
	Confidence   float64  `json:"confidence"`
	RootCause    string   `json:"root_cause"`
	EvidenceIDs  []string `json:"evidence_ids"`
	NextActions  []string `json:"next_actions"`
	PendingAudit bool     `json:"pending_audit"`
}

func NewIncident(workspace, ruleID, resourceID string, alertAt time.Time) Incident {
	now := time.Now().UTC()
	return Incident{
		ID:         newID(),
		Key:        fmt.Sprintf("%s:%s:%s", workspace, ruleID, resourceID),
		Workspace:  workspace,
		RuleID:     ruleID,
		ResourceID: resourceID,
		State:      IncidentReceived,
		AlertAt:    alertAt.UTC(),
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func NewJob(incidentID string, runAfter time.Time) Job {
	return Job{ID: newID(), IncidentID: incidentID, Status: JobQueued, RunAfter: runAfter.UTC()}
}

func newID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}
