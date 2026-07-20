package evidence

import (
	"context"
	"time"

	"github.com/jack/umodel-sre-rca/internal/domain"
)

type EvidenceClass string

const (
	ContextClass EvidenceClass = "context"
	MetricsClass EvidenceClass = "metrics"
	LogsClass    EvidenceClass = "logs"
	ChangesClass EvidenceClass = "changes"
)

type Binding struct {
	ID                  string            `yaml:"id"`
	EvidenceClass       EvidenceClass     `yaml:"evidence_class"`
	Target              string            `yaml:"target"`
	Provider            string            `yaml:"provider"`
	QueryTemplate       string            `yaml:"query_template"`
	SelectorMapping     map[string]string `yaml:"selector_mapping"`
	ConsoleLinkTemplate string            `yaml:"console_link_template"`
	QueryURL            string            `yaml:"query_url"`
	Credentials         any               `yaml:"credentials"`
	ShellCommand        string            `yaml:"shell_command"`
}

type Selectors map[string]string

type Window struct {
	Start time.Time
	End   time.Time
}

type IncidentContext struct {
	Selectors Selectors
	Window    Window
}

// IncidentSource resolves only the stable selectors and time range. It does
// not expose a cloud SDK or arbitrary query interface to callers.
type IncidentSource interface {
	ResolveIncident(context.Context, string) (IncidentContext, error)
}

// Provider is the only component that turns a reviewed binding into a cloud
// API call. Implementations must remain read-only and allowlisted.
type Provider interface {
	Resolve(context.Context, Binding, Selectors, Window) ([]domain.Evidence, error)
}

// EvidenceLookup reads the gateway's sanitized evidence snapshot. Raw
// telemetry remains in customer-controlled cloud storage.
type EvidenceLookup interface {
	OpenEvidence(context.Context, string) (domain.Evidence, error)
}
