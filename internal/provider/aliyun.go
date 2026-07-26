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
	Workspace      string
	Region         string
	ECSRAMRoleName string
	SLSProject     string
	SLSLogstore    string
}

type AliyunProvider struct {
	config AliyunConfig
	cloud  AliyunAPI
}

func NewAliyunProvider(config AliyunConfig, cloud AliyunAPI) *AliyunProvider {
	return &AliyunProvider{config: config, cloud: cloud}
}

func (p *AliyunProvider) Resolve(ctx context.Context, binding evidence.Binding, selectors evidence.Selectors, window evidence.Window) ([]domain.Evidence, error) {
	if err := validateWindow(window); err != nil {
		return nil, err
	}
	for _, value := range selectors {
		if err := validateSelectorValue(value); err != nil {
			return nil, err
		}
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
	raw, err := p.cloud.Call(ctx, AliyunRequest{Service: "cms", Operation: "GetEntityStoreData", Query: map[string]string{
		"Workspace": p.config.Workspace,
		"From":      fmt.Sprint(window.Start.Unix()),
		"To":        fmt.Sprint(window.End.Unix()),
		"Query":     query,
	}})
	if err != nil {
		return nil, err
	}
	return []domain.Evidence{newEvidence(binding.ID, "context", window.End, "cms:GetEntityStoreData", "cms:GetEntityStoreData", "UModel endpoint context resolved", raw)}, nil
}

func (p *AliyunProvider) resolveMetrics(ctx context.Context, binding evidence.Binding, selectors evidence.Selectors, window evidence.Window) ([]domain.Evidence, error) {
	instanceID := selectors["instance_id"]
	probeTaskID := selectors["probe_task_id"]
	regionID := p.config.Region
	if selectorRegionID := selectors["region_id"]; selectorRegionID != "" {
		regionID = selectorRegionID
	}
	if err := validateSelectorValue(regionID); err != nil {
		return nil, err
	}
	query := map[string]string{"StartTime": window.Start.UTC().Format(time.RFC3339), "EndTime": window.End.UTC().Format(time.RFC3339), "RegionId": regionID}
	if instanceID != "" {
		if err := validateSelectorValue(instanceID); err != nil {
			return nil, err
		}
		query["Dimensions"] = "[{\"instanceId\":\"" + instanceID + "\"}]"
	} else if probeTaskID != "" {
		if err := validateSelectorValue(probeTaskID); err != nil {
			return nil, err
		}
		query["Dimensions"] = "[{\"taskId\":\"" + probeTaskID + "\"}]"
	} else {
		return nil, fmt.Errorf("metrics binding requires instance_id or probe_task_id")
	}
	raw, err := p.cloud.Call(ctx, AliyunRequest{Service: "cms", Operation: "DescribeMetricList", Query: query})
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
	raw, err := p.cloud.Call(ctx, AliyunRequest{Service: "sls", Operation: "GetLogsV2", Query: map[string]string{
		"project":  p.config.SLSProject,
		"logstore": p.config.SLSLogstore,
	}, Body: map[string]any{
		"from":  int32(window.Start.Unix()),
		"to":    int32(window.End.Unix()),
		"query": query,
		"line":  int64(100),
	}})
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
	raw, err := p.cloud.Call(ctx, AliyunRequest{Service: "actiontrail", Operation: "LookupEvents", Query: map[string]string{
		"ResourceName": securityGroupID,
		"EventRW":      "Write",
		"StartTime":    window.Start.UTC().Format(time.RFC3339),
		"EndTime":      window.End.UTC().Format(time.RFC3339),
		"MaxResults":   "50",
	}})
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
