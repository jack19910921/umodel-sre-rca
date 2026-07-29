package modelsync

import (
	"context"
	"fmt"
	"regexp"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	"github.com/alibabacloud-go/tea/dara"
	"github.com/alibabacloud-go/tea/tea"
	"github.com/jack/umodel-sre-rca/internal/provider"
)

var workspacePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

// Writer is the dedicated write boundary for customer-owned UModel SRE data.
// It does not share the evidence provider's deliberately read-only API client.
type Writer interface {
	Upsert(context.Context, string, Plan) error
}

// CMSWriter uses short-lived credentials from the customer ECS RAM role and
// calls only CMS UpsertUmodelData. It cannot write to SLS, ECS, or ActionTrail.
type CMSWriter struct {
	client *openapi.Client
}

func NewCMSWriter(region, ecsRAMRoleName string) (*CMSWriter, error) {
	if region == "" {
		return nil, fmt.Errorf("aliyun region is required")
	}
	credential, err := provider.NewECSRAMRoleCredential(ecsRAMRoleName)
	if err != nil {
		return nil, fmt.Errorf("create ECS RAM role credential: %w", err)
	}
	client, err := openapi.NewClient(&openapi.Config{
		Endpoint:   tea.String(fmt.Sprintf("cms.%s.aliyuncs.com", region)),
		RegionId:   tea.String(region),
		Credential: credential,
	})
	if err != nil {
		return nil, fmt.Errorf("create CMS UModel writer: %w", err)
	}
	return &CMSWriter{client: client}, nil
}

// Upsert writes only the supplied plan to the named UModel workspace.
func (w *CMSWriter) Upsert(ctx context.Context, workspace string, plan Plan) error {
	if w == nil || w.client == nil {
		return fmt.Errorf("UModel writer is not configured")
	}
	params, request, err := buildUpsertRequest(workspace, plan)
	if err != nil {
		return err
	}
	if _, err := w.client.CallApiWithCtx(ctx, params, request, &dara.RuntimeOptions{}); err != nil {
		return fmt.Errorf("upsert UModel data: %w", err)
	}
	return nil
}

func buildUpsertRequest(workspace string, plan Plan) (*openapi.Params, *openapi.OpenApiRequest, error) {
	if !workspacePattern.MatchString(workspace) {
		return nil, nil, fmt.Errorf("unsafe workspace name %q", workspace)
	}
	if len(plan.Elements) == 0 {
		return nil, nil, fmt.Errorf("UModel upsert plan must contain at least one element")
	}
	return &openapi.Params{
			Action:      tea.String("UpsertUmodelData"),
			Version:     tea.String("2024-03-30"),
			Protocol:    tea.String("HTTPS"),
			Pathname:    tea.String("/workspace/" + workspace + "/umodel/data"),
			Method:      tea.String("PATCH"),
			AuthType:    tea.String("AK"),
			Style:       tea.String("ROA"),
			ReqBodyType: tea.String("json"),
			BodyType:    tea.String("json"),
		}, &openapi.OpenApiRequest{Body: map[string]any{
			"method":   "upsert",
			"elements": plan.Elements,
		}}, nil
}
