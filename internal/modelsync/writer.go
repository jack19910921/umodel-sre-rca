package modelsync

import (
	"context"
	"fmt"
	"regexp"
	"time"

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

// SchemaInspector is a deliberately read-only boundary used to verify how the
// CMS UModel service currently sees a workspace before attempting a write.
type SchemaInspector interface {
	InspectSchema(context.Context, string, []string) (map[string]any, error)
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

// InspectSchema reads the supported EntityStore query API for the supplied
// SRE domain. It never creates or mutates UModel data.
func (w *CMSWriter) InspectSchema(ctx context.Context, workspace string, domains []string) (map[string]any, error) {
	if w == nil || w.client == nil {
		return nil, fmt.Errorf("UModel writer is not configured")
	}
	if len(domains) != 1 || domains[0] != "sre" {
		return nil, fmt.Errorf("only the sre domain can be inspected")
	}
	params, request, err := buildGetEntityStoreDataRequest(
		workspace,
		"sre",
		"sre.service_endpoint",
		time.Now().UTC(),
	)
	if err != nil {
		return nil, err
	}
	result, err := w.client.CallApiWithCtx(ctx, params, request, &dara.RuntimeOptions{})
	if err != nil {
		return nil, fmt.Errorf("get UModel EntityStore data: %w", err)
	}
	return result, nil
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
		}, &openapi.OpenApiRequest{
			Query: map[string]*string{
				"method": tea.String("upsert"),
			},
			Body: map[string]any{
				"elements": plan.Elements,
			},
		}, nil
}

func buildGetEntityStoreDataRequest(workspace, domain, entityType string, observedAt time.Time) (*openapi.Params, *openapi.OpenApiRequest, error) {
	if !workspacePattern.MatchString(workspace) {
		return nil, nil, fmt.Errorf("unsafe workspace name %q", workspace)
	}
	if domain != "sre" || entityType != "sre.service_endpoint" {
		return nil, nil, fmt.Errorf("unsupported EntityStore inspection target %q/%q", domain, entityType)
	}
	if observedAt.IsZero() {
		return nil, nil, fmt.Errorf("EntityStore inspection time is required")
	}
	to := observedAt.UTC().Unix()
	from := observedAt.UTC().Add(-48 * time.Hour).Unix()
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
		}, &openapi.OpenApiRequest{
			Body: map[string]any{
				"from":  from,
				"to":    to,
				"query": ".entity with(domain='" + domain + "', type='" + entityType + "') | limit 0, 10",
			},
		}, nil
}
