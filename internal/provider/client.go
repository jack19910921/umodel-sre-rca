package provider

import (
	"context"
	"encoding/json"
	"fmt"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	sls "github.com/alibabacloud-go/sls-20201230/v6/client"
	"github.com/alibabacloud-go/tea/dara"
	"github.com/alibabacloud-go/tea/tea"
)

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
	"sls":         {"GetLogsV2": true},
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
		version := "2019-01-01"
		if request.Operation == "GetEntityStoreData" {
			version = "2024-03-30"
		}
		return callRPC(ctx, c.cms, request.Operation, version, request.Query)
	case "actiontrail":
		return callRPC(ctx, c.actionTrail, request.Operation, "2020-07-06", request.Query)
	case "sls":
		return callSLS(ctx, c.sls, request)
	default:
		return nil, fmt.Errorf("unsupported cloud service %q", request.Service)
	}
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

func callSLS(ctx context.Context, client *sls.Client, request AliyunRequest) ([]byte, error) {
	project := request.Query["project"]
	logstore := request.Query["logstore"]
	if project == "" || logstore == "" {
		return nil, fmt.Errorf("SLS project and logstore are required")
	}
	response, err := client.CallApiWithCtx(ctx, &openapi.Params{
		Action:      tea.String("GetLogsV2"),
		Version:     tea.String("2020-12-30"),
		Protocol:    tea.String("HTTPS"),
		Pathname:    tea.String("/logstores/" + logstore + "/logs"),
		Method:      tea.String("POST"),
		AuthType:    tea.String("AK"),
		Style:       tea.String("ROA"),
		ReqBodyType: tea.String("json"),
		BodyType:    tea.String("json"),
	}, &openapi.OpenApiRequest{
		HostMap: map[string]*string{"project": tea.String(project)},
		Body:    request.Body,
	}, &dara.RuntimeOptions{})
	if err != nil {
		return nil, err
	}
	return json.Marshal(response)
}
