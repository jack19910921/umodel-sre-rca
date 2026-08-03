package provider

import (
	"fmt"
	"time"

	"github.com/jack/umodel-sre-rca/internal/evidence"
	"github.com/jack/umodel-sre-rca/internal/umodelid"
)

// Request builders are the only place that turns reviewed selectors into cloud
// requests. They deliberately accept no callback payload or model output.
func buildEntityStoreContextRequest(config AliyunConfig, endpointID string, window evidence.Window) (AliyunRequest, error) {
	if config.Workspace == "" {
		return AliyunRequest{}, fmt.Errorf("aliyun workspace is required for UModel context")
	}
	return buildEntityStoreRequest(config.Workspace,
		".entity with(domain='sre', type='sre.service_endpoint') | where endpoint_id = '"+endpointID+"' | limit 0, 1",
		window), nil
}

func buildEntityStoreTopologyRequest(config AliyunConfig, endpointID string, window evidence.Window) (AliyunRequest, error) {
	if config.Workspace == "" {
		return AliyunRequest{}, fmt.Errorf("aliyun workspace is required for UModel topology context")
	}
	entityID := umodelid.EndpointEntityID(endpointID)
	query := ".topo | graph-call getNeighborNodes('sequence_out', 1, [(:\"sre@sre.service_endpoint\" {__entity_id__: '" + entityID + "'})]) | where relationType = 'runs_on'"
	return buildEntityStoreRequest(config.Workspace, query, window), nil
}

func buildEntityStoreRequest(workspace, query string, window evidence.Window) AliyunRequest {
	return AliyunRequest{Service: "cms", Operation: "GetEntityStoreData", Query: map[string]string{
		"Workspace": workspace,
		"From":      fmt.Sprint(window.Start.Unix()),
		"To":        fmt.Sprint(window.End.Unix()),
		"Query":     query,
	}}
}

func buildMetricsRequest(config AliyunConfig, selectors evidence.Selectors, window evidence.Window) (AliyunRequest, error) {
	regionID := config.Region
	if selectorRegionID := selectors["region_id"]; selectorRegionID != "" {
		regionID = selectorRegionID
	}
	if err := validateSelectorValue(regionID); err != nil {
		return AliyunRequest{}, err
	}
	query := map[string]string{
		"StartTime": window.Start.UTC().Format(time.RFC3339),
		"EndTime":   window.End.UTC().Format(time.RFC3339),
		"RegionId":  regionID,
	}
	if instanceID := selectors["instance_id"]; instanceID != "" {
		if err := validateSelectorValue(instanceID); err != nil {
			return AliyunRequest{}, err
		}
		query["Namespace"] = "acs_ecs_dashboard"
		query["MetricName"] = "cpu_total"
		query["Period"] = "60"
		query["Dimensions"] = "[{\"instanceId\":\"" + instanceID + "\"}]"
	} else if probeTaskID := selectors["probe_task_id"]; probeTaskID != "" {
		if err := validateSelectorValue(probeTaskID); err != nil {
			return AliyunRequest{}, err
		}
		query["Dimensions"] = "[{\"taskId\":\"" + probeTaskID + "\"}]"
	} else {
		return AliyunRequest{}, fmt.Errorf("metrics binding requires instance_id or probe_task_id")
	}
	return AliyunRequest{Service: "cms", Operation: "DescribeMetricList", Query: query}, nil
}

func buildNginxLogsRequest(config AliyunConfig, logKind string, selectors evidence.Selectors, window evidence.Window) (AliyunRequest, error) {
	endpointID, err := requiredSelector(selectors, "endpoint_id")
	if err != nil {
		return AliyunRequest{}, err
	}
	instanceID, err := requiredSelector(selectors, "instance_id")
	if err != nil {
		return AliyunRequest{}, err
	}
	if config.SLSProject == "" || config.SLSLogstore == "" {
		return AliyunRequest{}, fmt.Errorf("SLS project and logstore are required for Nginx logs")
	}
	return AliyunRequest{Service: "sls", Operation: "GetLogs", Query: map[string]string{
		"project": config.SLSProject, "logstore": config.SLSLogstore,
	}, Body: map[string]any{
		"from":  int32(window.Start.Unix()),
		"to":    int32(window.End.Unix()),
		"query": "endpoint_id:\"" + endpointID + "\" AND log_kind:\"" + logKind + "\" AND instance_id:\"" + instanceID + "\"",
		"line":  int64(100),
	}}, nil
}

func buildActionTrailRequest(securityGroupID string, window evidence.Window) AliyunRequest {
	return AliyunRequest{Service: "actiontrail", Operation: "LookupEvents", Query: map[string]string{
		"ResourceName": securityGroupID,
		"EventRW":      "Write",
		"StartTime":    window.Start.UTC().Format(time.RFC3339),
		"EndTime":      window.End.UTC().Format(time.RFC3339),
		"MaxResults":   "50",
	}}
}
