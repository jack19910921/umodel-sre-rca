package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
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
	case "endpoint_topology_v1":
		return p.resolveTopologyContext(ctx, binding, selectors, window)
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

func (p *AliyunProvider) resolveTopologyContext(ctx context.Context, binding evidence.Binding, selectors evidence.Selectors, window evidence.Window) ([]domain.Evidence, error) {
	endpointID, err := requiredSelector(selectors, "endpoint_id")
	if err != nil {
		return nil, err
	}
	request, err := buildEntityStoreTopologyRequest(p.config, endpointID, window)
	if err != nil {
		return nil, err
	}
	raw, err := p.cloud.Call(ctx, request)
	if err != nil {
		return nil, err
	}
	return []domain.Evidence{newEvidence(binding.ID, "context", window.End, "cms:GetEntityStoreData", "cms:GetEntityStoreData:topology", "UModel endpoint topology resolved", raw)}, nil
}

func (p *AliyunProvider) resolveContext(ctx context.Context, binding evidence.Binding, selectors evidence.Selectors, window evidence.Window) ([]domain.Evidence, error) {
	endpointID, err := requiredSelector(selectors, "endpoint_id")
	if err != nil {
		return nil, err
	}
	request, err := buildEntityStoreContextRequest(p.config, endpointID, window)
	if err != nil {
		return nil, err
	}
	raw, err := p.cloud.Call(ctx, request)
	if err != nil {
		return nil, err
	}
	return []domain.Evidence{newEvidence(binding.ID, "context", window.End, "cms:GetEntityStoreData", "cms:GetEntityStoreData", "UModel endpoint context resolved", raw)}, nil
}

func (p *AliyunProvider) resolveMetrics(ctx context.Context, binding evidence.Binding, selectors evidence.Selectors, window evidence.Window) ([]domain.Evidence, error) {
	request, err := buildMetricsRequest(p.config, selectors, window)
	if err != nil {
		return nil, err
	}
	raw, err := p.cloud.Call(ctx, request)
	if err != nil {
		return nil, err
	}
	return []domain.Evidence{newEvidence(binding.ID, "metric", window.End, "cms:DescribeMetricList", "cms:DescribeMetricList", summarizeECSCPU(raw), raw)}, nil
}

func (p *AliyunProvider) resolveLogs(ctx context.Context, binding evidence.Binding, selectors evidence.Selectors, window evidence.Window) ([]domain.Evidence, error) {
	logKind := ""
	switch binding.QueryTemplate {
	case "nginx_access_by_window_v1":
		logKind = "access"
	case "nginx_error_by_window_v1":
		logKind = "error"
	default:
		return nil, fmt.Errorf("unsupported Nginx log template %q", binding.QueryTemplate)
	}
	request, err := buildNginxLogsRequest(p.config, logKind, selectors, window)
	if err != nil {
		return nil, err
	}
	raw, err := p.cloud.Call(ctx, request)
	if err != nil {
		return nil, err
	}
	return []domain.Evidence{newEvidence(binding.ID, "log", window.End, "sls:GetLogs", "sls:GetLogs", summarizeNginxAccess(raw), raw)}, nil
}

func summarizeECSCPU(raw []byte) string {
	var envelope struct {
		Body map[string]json.RawMessage `json:"body"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "云监控 ECS CPU：已读取时间窗数据，统计解析失败"
	}
	datapoints, found := envelope.Body["Datapoints"]
	if !found {
		return "云监控 ECS CPU：已读取时间窗数据，未返回数据点字段"
	}
	var encoded string
	if err := json.Unmarshal(datapoints, &encoded); err != nil {
		return "云监控 ECS CPU：已读取时间窗数据，数据点格式无法解析"
	}
	var points []map[string]any
	if err := json.Unmarshal([]byte(encoded), &points); err != nil {
		return "云监控 ECS CPU：已读取时间窗数据，数据点格式无法解析"
	}
	if len(points) == 0 {
		return "云监控 ECS CPU：0 个数据点"
	}

	var averages, maximums, minimums []float64
	for _, point := range points {
		if value, ok := numberValue(point["Average"]); ok {
			averages = append(averages, value)
		}
		if value, ok := numberValue(point["Maximum"]); ok {
			maximums = append(maximums, value)
		}
		if value, ok := numberValue(point["Minimum"]); ok {
			minimums = append(minimums, value)
		}
	}
	if len(averages) == 0 || len(maximums) == 0 || len(minimums) == 0 {
		return fmt.Sprintf("云监控 ECS CPU：%d 个数据点，缺少完整聚合值", len(points))
	}
	return fmt.Sprintf("云监控 ECS CPU：%d 个数据点，平均值 %.2f%%，最大值 %.2f%%，最小值 %.2f%%", len(points), average(averages), maximum(maximums), minimum(minimums))
}

func summarizeNginxAccess(raw []byte) string {
	var envelope struct {
		Body []map[string]any `json:"body"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return "Nginx access 日志：已读取时间窗数据，统计解析失败"
	}
	count5xx := 0
	for _, entry := range envelope.Body {
		for _, key := range []string{"http_code", "status", "status_code"} {
			status, ok := numberValue(entry[key])
			if ok && status >= 500 && status < 600 {
				count5xx++
				break
			}
		}
	}
	return fmt.Sprintf("Nginx access 日志：%d 条，5xx 响应 %d 条", len(envelope.Body), count5xx)
}

func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case string:
		parsed, err := strconv.ParseFloat(typed, 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func average(values []float64) float64 {
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func maximum(values []float64) float64 {
	result := values[0]
	for _, value := range values[1:] {
		if value > result {
			result = value
		}
	}
	return result
}

func minimum(values []float64) float64 {
	result := values[0]
	for _, value := range values[1:] {
		if value < result {
			result = value
		}
	}
	return result
}

func (p *AliyunProvider) resolveChanges(ctx context.Context, binding evidence.Binding, selectors evidence.Selectors, window evidence.Window) ([]domain.Evidence, error) {
	securityGroupID, err := requiredSelector(selectors, "security_group_id")
	if err != nil {
		return nil, err
	}
	raw, err := p.cloud.Call(ctx, buildActionTrailRequest(securityGroupID, window))
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
