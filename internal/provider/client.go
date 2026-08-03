package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	sls "github.com/alibabacloud-go/sls-20201230/v6/client"
	"github.com/alibabacloud-go/tea/dara"
	"github.com/alibabacloud-go/tea/tea"
)

var entityStoreWorkspacePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

// AliyunRequest is an internal, fixed-shape request to an approved evidence
// API. It cannot carry a URL, executable, profile, or credential.
type AliyunRequest struct {
	Service   string
	Operation string
	Query     map[string]string
	Body      map[string]any
}

// AliyunAPI is the narrow cloud boundary used by the evidence provider.
type AliyunAPI interface {
	Call(context.Context, AliyunRequest) ([]byte, error)
}

var allowedActions = map[string]map[string]bool{
	"cms":         {"GetEntityStoreData": true, "DescribeMetricList": true},
	"sls":         {"GetLogs": true},
	"actiontrail": {"LookupEvents": true},
}

// SDKAliyunClient invokes fixed, read-only OpenAPI operations using temporary
// credentials from the RAM role attached to the customer ECS instance.
type SDKAliyunClient struct {
	cms         *openapi.Client
	actionTrail *openapi.Client
	sls         *sls.Client
}

func NewAliyunSDKClient(region, ecsRAMRoleName string) (*SDKAliyunClient, error) {
	if region == "" {
		return nil, fmt.Errorf("aliyun region is required")
	}
	credential, err := NewECSRAMRoleCredential(ecsRAMRoleName)
	if err != nil {
		return nil, fmt.Errorf("create ECS RAM role credential: %w", err)
	}
	cms, err := openapi.NewClient(&openapi.Config{
		Endpoint:   tea.String(fmt.Sprintf("cms.%s.aliyuncs.com", region)),
		RegionId:   tea.String(region),
		Credential: credential,
	})
	if err != nil {
		return nil, fmt.Errorf("create CMS OpenAPI client: %w", err)
	}
	actionTrail, err := openapi.NewClient(&openapi.Config{
		Endpoint:   tea.String(fmt.Sprintf("actiontrail.%s.aliyuncs.com", region)),
		RegionId:   tea.String(region),
		Credential: credential,
	})
	if err != nil {
		return nil, fmt.Errorf("create ActionTrail OpenAPI client: %w", err)
	}
	slsClient, err := sls.NewClient(&openapi.Config{
		Endpoint:   tea.String(fmt.Sprintf("%s.log.aliyuncs.com", region)),
		RegionId:   tea.String(region),
		Credential: credential,
	})
	if err != nil {
		return nil, fmt.Errorf("create SLS OpenAPI client: %w", err)
	}
	return &SDKAliyunClient{cms: cms, actionTrail: actionTrail, sls: slsClient}, nil
}

func (c *SDKAliyunClient) Call(ctx context.Context, request AliyunRequest) ([]byte, error) {
	if !allowedActions[request.Service][request.Operation] {
		return nil, fmt.Errorf("%s:%s not allowlisted", request.Service, request.Operation)
	}
	switch request.Service {
	case "cms":
		if request.Operation == "GetEntityStoreData" {
			return callEntityStoreData(ctx, c.cms, request.Query)
		}
		return callRPC(ctx, c.cms, request.Operation, "2019-01-01", request.Query)
	case "actiontrail":
		return callRPC(ctx, c.actionTrail, request.Operation, "2020-07-06", request.Query)
	case "sls":
		return callSLS(ctx, c.sls, request)
	default:
		return nil, fmt.Errorf("unsupported cloud service %q", request.Service)
	}
}

func callEntityStoreData(ctx context.Context, client *openapi.Client, values map[string]string) ([]byte, error) {
	params, request, err := buildEntityStoreDataRequest(values)
	if err != nil {
		return nil, err
	}
	response, err := client.CallApiWithCtx(ctx, params, request, &dara.RuntimeOptions{})
	if err != nil {
		return nil, err
	}
	return json.Marshal(response)
}

func buildEntityStoreDataRequest(values map[string]string) (*openapi.Params, *openapi.OpenApiRequest, error) {
	workspace := values["Workspace"]
	if !entityStoreWorkspacePattern.MatchString(workspace) {
		return nil, nil, fmt.Errorf("safe EntityStore workspace is required")
	}
	from, err := strconv.ParseInt(values["From"], 10, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("valid EntityStore from time is required: %w", err)
	}
	to, err := strconv.ParseInt(values["To"], 10, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("valid EntityStore to time is required: %w", err)
	}
	if to <= from {
		return nil, nil, fmt.Errorf("EntityStore to time must be after from time")
	}
	query := values["Query"]
	if query == "" {
		return nil, nil, fmt.Errorf("EntityStore query is required")
	}
	return &openapi.Params{
			Action:      tea.String("GetEntityStoreData"),
			Version:     tea.String("2024-03-30"),
			Protocol:    tea.String("HTTPS"),
			Pathname:    tea.String("/workspace/" + workspace + "/entitiesAndRelations"),
			Method:      tea.String("POST"),
			AuthType:    tea.String("AK"),
			Style:       tea.String("ROA"),
			ReqBodyType: tea.String("json"),
			BodyType:    tea.String("json"),
		}, &openapi.OpenApiRequest{Body: map[string]any{
			"from":  from,
			"to":    to,
			"query": query,
		}}, nil
}

func callRPC(ctx context.Context, client *openapi.Client, action, version string, values map[string]string) ([]byte, error) {
	query := make(map[string]*string, len(values))
	for key, value := range values {
		query[key] = tea.String(value)
	}
	response, err := client.CallApiWithCtx(ctx, &openapi.Params{
		Action:      tea.String(action),
		Version:     tea.String(version),
		Protocol:    tea.String("HTTPS"),
		Pathname:    tea.String("/"),
		Method:      tea.String("POST"),
		AuthType:    tea.String("AK"),
		Style:       tea.String("RPC"),
		ReqBodyType: tea.String("formData"),
		BodyType:    tea.String("json"),
	}, &openapi.OpenApiRequest{Query: query}, &dara.RuntimeOptions{})
	if err != nil {
		return nil, err
	}
	return json.Marshal(response)
}

func callSLS(_ context.Context, client *sls.Client, request AliyunRequest) ([]byte, error) {
	if request.Operation != "GetLogs" {
		return nil, fmt.Errorf("unsupported SLS operation %q", request.Operation)
	}
	project := request.Query["project"]
	logstore := request.Query["logstore"]
	if project == "" || logstore == "" {
		return nil, fmt.Errorf("SLS project and logstore are required")
	}
	from, ok := request.Body["from"].(int32)
	if !ok {
		return nil, fmt.Errorf("SLS from time is required")
	}
	to, ok := request.Body["to"].(int32)
	if !ok {
		return nil, fmt.Errorf("SLS to time is required")
	}
	query, ok := request.Body["query"].(string)
	if !ok {
		return nil, fmt.Errorf("SLS query is required")
	}
	line, ok := request.Body["line"].(int64)
	if !ok {
		return nil, fmt.Errorf("SLS line limit is required")
	}
	response, err := client.GetLogs(tea.String(project), tea.String(logstore), &sls.GetLogsRequest{
		From:  tea.Int32(from),
		To:    tea.Int32(to),
		Query: tea.String(query),
		Line:  tea.Int64(line),
	})
	if err != nil {
		return nil, err
	}
	return json.Marshal(response)
}
