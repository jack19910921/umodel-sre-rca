package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jack/umodel-sre-rca/internal/domain"
	"github.com/jack/umodel-sre-rca/internal/evidence"
)

type AliyunConfig struct {
	Profile     string
	Workspace   string
	Region      string
	SLSProject  string
	SLSLogstore string
}

type AliyunProvider struct {
	config AliyunConfig
	runner *AliyunRunner
}

func NewAliyunProvider(config AliyunConfig, runner *AliyunRunner) *AliyunProvider {
	return &AliyunProvider{config: config, runner: runner}
}

func (p *AliyunProvider) Resolve(ctx context.Context, binding evidence.Binding, selectors evidence.Selectors, window evidence.Window) ([]domain.Evidence, error) {
	if err := validateWindow(window); err != nil {
		return nil, err
	}
	switch binding.QueryTemplate {
	case "endpoint_context_v1":
		return p.resolveContext(ctx, binding, selectors, window)
	case "availability_window_v1", "ecs_normal_state_window_v1":
		return p.resolveMetrics(ctx, binding, selectors, window)
	case "nginx_access_by_window_v1", "nginx_error_by_window_v1":
		return p.resolveLogs(ctx, binding, selectors, window)
	case "security_group_change_window_v1":
		return p.resolveChanges(ctx, binding, selectors, window)
	default:
		return nil, fmt.Errorf("template %q is not implemented", binding.QueryTemplate)
	}
}

func (p *AliyunProvider) resolveContext(ctx context.Context, binding evidence.Binding, selectors evidence.Selectors, window evidence.Window) ([]domain.Evidence, error) {
	endpointID, err := requiredSelector(selectors, "endpoint_id")
	if err != nil {
		return nil, err
	}
	if p.config.Workspace == "" {
		return nil, fmt.Errorf("aliyun workspace is required for UModel context")
	}
	query := ".entity with(domain='sre', type='sre.service_endpoint') | where endpoint_id = '" + endpointID + "' | limit 0, 1"
	raw, err := p.runner.Run(ctx, "cms", "GetEntityStoreData", []string{"--workspace", p.config.Workspace, "--from", fmt.Sprint(window.Start.Unix()), "--to", fmt.Sprint(window.End.Unix()), "--query", query})
	if err != nil {
		return nil, err
	}
	return []domain.Evidence{newEvidence(binding.ID, "context", window.End, "cms:GetEntityStoreData", "cms:GetEntityStoreData", "UModel endpoint context resolved", raw)}, nil
}

func (p *AliyunProvider) resolveMetrics(ctx context.Context, binding evidence.Binding, selectors evidence.Selectors, window evidence.Window) ([]domain.Evidence, error) {
	instanceID := selectors["instance_id"]
	probeTaskID := selectors["probe_task_id"]
	args := []string{"--StartTime", window.Start.UTC().Format(time.RFC3339), "--EndTime", window.End.UTC().Format(time.RFC3339), "--RegionId", p.config.Region}
	if instanceID != "" {
		if err := validateSelectorValue(instanceID); err != nil {
			return nil, err
		}
		args = append(args, "--Dimensions", "[{\"instanceId\":\""+instanceID+"\"}]")
	} else if probeTaskID != "" {
		if err := validateSelectorValue(probeTaskID); err != nil {
			return nil, err
		}
		args = append(args, "--Dimensions", "[{\"taskId\":\""+probeTaskID+"\"}]")
	} else {
		return nil, fmt.Errorf("metrics binding requires instance_id or probe_task_id")
	}
	raw, err := p.runner.Run(ctx, "cms", "DescribeMetricList", args)
	if err != nil {
		return nil, err
	}
	return []domain.Evidence{newEvidence(binding.ID, "metric", window.End, "cms:DescribeMetricList", "cms:DescribeMetricList", "Read-only CloudMonitor metric window retrieved", raw)}, nil
}

func (p *AliyunProvider) resolveLogs(ctx context.Context, binding evidence.Binding, selectors evidence.Selectors, window evidence.Window) ([]domain.Evidence, error) {
	endpointID, err := requiredSelector(selectors, "endpoint_id")
	if err != nil {
		return nil, err
	}
	if p.config.SLSProject == "" || p.config.SLSLogstore == "" {
		return nil, fmt.Errorf("SLS project and logstore are required for Nginx logs")
	}
	query := "endpoint_id:" + endpointID
	raw, err := p.runner.Run(ctx, "sls", "GetLogs", []string{"--project", p.config.SLSProject, "--logstore", p.config.SLSLogstore, "--from", fmt.Sprint(window.Start.Unix()), "--to", fmt.Sprint(window.End.Unix()), "--query", query})
	if err != nil {
		return nil, err
	}
	return []domain.Evidence{newEvidence(binding.ID, "log", window.End, "sls:GetLogs", "sls:GetLogs", "Read-only Nginx log window retrieved", raw)}, nil
}

func (p *AliyunProvider) resolveChanges(ctx context.Context, binding evidence.Binding, selectors evidence.Selectors, window evidence.Window) ([]domain.Evidence, error) {
	securityGroupID, err := requiredSelector(selectors, "security_group_id")
	if err != nil {
		return nil, err
	}
	raw, err := p.runner.Run(ctx, "actiontrail", "LookupEvents", []string{"--ResourceName", securityGroupID, "--EventRW", "Write", "--StartTime", window.Start.UTC().Format(time.RFC3339), "--EndTime", window.End.UTC().Format(time.RFC3339), "--MaxResults", "50"})
	if err != nil {
		return nil, err
	}
	var response struct {
		RequestID string `json:"RequestId"`
		Events    []struct {
			EventID     string `json:"eventId"`
			EventName   string `json:"eventName"`
			EventTime   string `json:"eventTime"`
			RequestID   string `json:"requestId"`
			ServiceName string `json:"serviceName"`
			User        struct {
				UserName string `json:"userName"`
			} `json:"userIdentity"`
		} `json:"Events"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, fmt.Errorf("decode ActionTrail response: %w", err)
	}
	evidenceItems := make([]domain.Evidence, 0, len(response.Events))
	for _, event := range response.Events {
		observedAt, err := time.Parse(time.RFC3339, event.EventTime)
		if err != nil {
			return nil, fmt.Errorf("decode ActionTrail event time: %w", err)
		}
		summary := fmt.Sprintf("ActionTrail %s on %s by %s", event.EventName, securityGroupID, event.User.UserName)
		evidenceItems = append(evidenceItems, newEvidence(binding.ID+":"+event.EventID, "change", observedAt, "actiontrail:LookupEvents", "actiontrail:"+response.RequestID+":"+event.RequestID, summary, raw))
	}
	return evidenceItems, nil
}

func requiredSelector(selectors evidence.Selectors, name string) (string, error) {
	value := selectors[name]
	if value == "" {
		return "", fmt.Errorf("missing selector %q", name)
	}
	if err := validateSelectorValue(value); err != nil {
		return "", err
	}
	return value, nil
}

var safeSelector = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,256}$`)

func validateSelectorValue(value string) error {
	if !safeSelector.MatchString(value) {
		return fmt.Errorf("selector value contains unsupported characters")
	}
	return nil
}

func validateWindow(window evidence.Window) error {
	if window.Start.IsZero() || window.End.IsZero() || !window.End.After(window.Start) {
		return fmt.Errorf("invalid evidence window")
	}
	return nil
}

func newEvidence(seed, evidenceType string, observedAt time.Time, source, queryRef, summary string, raw []byte) domain.Evidence {
	hash := sha256.Sum256(append([]byte(seed+":"+queryRef+":"), raw...))
	return domain.Evidence{ID: hex.EncodeToString(hash[:12]), Type: evidenceType, ObservedAt: observedAt.UTC(), Source: source, QueryRef: queryRef, Summary: strings.TrimSpace(summary), RawRef: "sha256:" + hex.EncodeToString(hash[:])}
}
