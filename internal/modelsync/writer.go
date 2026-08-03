package modelsync

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"time"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	"github.com/alibabacloud-go/tea/dara"
	"github.com/alibabacloud-go/tea/tea"
	sls "github.com/aliyun/aliyun-log-go-sdk"
	"github.com/gogo/protobuf/proto"
	"github.com/jack/umodel-sre-rca/internal/aliyunauth"
)

var (
	workspacePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)
	entityIDPattern  = regexp.MustCompile(`^[a-fA-F0-9]{32}$`)
)

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

// RelationInspector is a read-only boundary for verifying one explicitly
// configured directed relation through the supported EntityStore API.
type RelationInspector interface {
	InspectRelation(context.Context, string, Endpoint, Relation) (map[string]any, error)
}

type entityStoreLogClient interface {
	PutLogs(project, logstore string, group *sls.LogGroup) error
}

// CMSWriter combines the read-only CMS EntityStore inspector with the
// documented SLS Log-protocol writer used for customer-owned EntityStore data.
// It uses only short-lived ECS RAM-role credentials.
type CMSWriter struct {
	client    *openapi.Client
	logClient entityStoreLogClient
}

func NewCMSWriter(region, ecsRAMRoleName string) (*CMSWriter, error) {
	if region == "" {
		return nil, fmt.Errorf("aliyun region is required")
	}
	credential, err := aliyunauth.NewECSRAMRoleCredential(ecsRAMRoleName)
	if err != nil {
		return nil, fmt.Errorf("create ECS RAM role credential: %w", err)
	}
	client, err := openapi.NewClient(&openapi.Config{
		Endpoint:   tea.String(fmt.Sprintf("cms.%s.aliyuncs.com", region)),
		RegionId:   tea.String(region),
		Credential: credential,
	})
	if err != nil {
		return nil, fmt.Errorf("create CMS EntityStore inspector: %w", err)
	}
	return &CMSWriter{
		client: client,
		logClient: sls.CreateNormalInterfaceV2(
			fmt.Sprintf("%s-intranet.log.aliyuncs.com", region),
			sls.NewEcsRamRoleCredentialsProvider(ecsRAMRoleName),
		),
	}, nil
}

// Upsert writes EntityStore entities through SLS Log protocol, as required by
// CloudMonitor 2.0. UModel schema mutations are intentionally out of scope.
func (w *CMSWriter) Upsert(ctx context.Context, workspace string, plan Plan) error {
	if w == nil || w.logClient == nil {
		return fmt.Errorf("EntityStore writer is not configured")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !workspacePattern.MatchString(workspace) {
		return fmt.Errorf("unsafe workspace name %q", workspace)
	}
	entities, relations := splitEntityStorePlan(plan)
	observedAt := time.Now().UTC()
	if len(entities.Elements) > 0 {
		group, err := buildEntityStoreLogGroup(entities, observedAt)
		if err != nil {
			return err
		}
		if err := w.logClient.PutLogs(workspace, entityStoreEntityLogStoreName(workspace), group); err != nil {
			return fmt.Errorf("write UModel EntityStore entity data: %w", err)
		}
	}
	if len(relations.Elements) > 0 {
		group, err := buildEntityStoreRelationLogGroup(relations, observedAt)
		if err != nil {
			return err
		}
		if err := w.logClient.PutLogs(workspace, entityStoreTopoLogStoreName(workspace), group); err != nil {
			return fmt.Errorf("write UModel EntityStore relation data: %w", err)
		}
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

// InspectRelation verifies the configured SRE endpoint's directed relation to
// the native ECS entity. It never writes SLS data.
func (w *CMSWriter) InspectRelation(ctx context.Context, workspace string, endpoint Endpoint, relation Relation) (map[string]any, error) {
	if w == nil || w.client == nil {
		return nil, fmt.Errorf("UModel relation inspector is not configured")
	}
	params, request, err := buildGetEntityStoreRelationRequest(workspace, endpoint, relation, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	result, err := w.client.CallApiWithCtx(ctx, params, request, &dara.RuntimeOptions{})
	if err != nil {
		return nil, fmt.Errorf("get UModel EntityStore relation data: %w", err)
	}
	return result, nil
}

func entityStoreEntityLogStoreName(workspace string) string {
	return workspace + "__entity"
}

func entityStoreTopoLogStoreName(workspace string) string {
	return workspace + "__topo"
}

func splitEntityStorePlan(plan Plan) (entities Plan, relations Plan) {
	for _, element := range plan.Elements {
		if isRelationElement(element) {
			relations.Elements = append(relations.Elements, element)
			continue
		}
		entities.Elements = append(entities.Elements, element)
	}
	return entities, relations
}

func buildEntityStoreLogGroup(plan Plan, observedAt time.Time) (*sls.LogGroup, error) {
	if len(plan.Elements) == 0 {
		return nil, fmt.Errorf("EntityStore write plan must contain at least one entity")
	}
	if observedAt.IsZero() {
		return nil, fmt.Errorf("EntityStore write time is required")
	}

	logs := make([]*sls.Log, 0, len(plan.Elements))
	for index, element := range plan.Elements {
		if isRelationElement(element) {
			return nil, fmt.Errorf("element %d is a relation; EntityStore entity writer only accepts entities", index)
		}
		if err := validateEntityElement(element); err != nil {
			return nil, fmt.Errorf("element %d: %w", index, err)
		}
		log, err := buildEntityStoreLog(element, observedAt, index)
		if err != nil {
			return nil, err
		}
		logs = append(logs, log)
	}

	return newEntityStoreLogGroup(logs), nil
}

func buildEntityStoreRelationLogGroup(plan Plan, observedAt time.Time) (*sls.LogGroup, error) {
	if len(plan.Elements) == 0 {
		return nil, fmt.Errorf("EntityStore relation write plan must contain at least one relation")
	}
	if observedAt.IsZero() {
		return nil, fmt.Errorf("EntityStore write time is required")
	}

	logs := make([]*sls.Log, 0, len(plan.Elements))
	for index, element := range plan.Elements {
		if !isRelationElement(element) {
			return nil, fmt.Errorf("element %d is an entity; EntityStore relation writer only accepts relations", index)
		}
		if err := validateRelationElement(element); err != nil {
			return nil, fmt.Errorf("element %d: %w", index, err)
		}
		log, err := buildEntityStoreLog(element, observedAt, index)
		if err != nil {
			return nil, err
		}
		logs = append(logs, log)
	}

	return newEntityStoreLogGroup(logs), nil
}

func buildEntityStoreLog(element map[string]any, observedAt time.Time, index int) (*sls.Log, error) {
	keys := make([]string, 0, len(element))
	for key := range element {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	contents := make([]*sls.LogContent, 0, len(keys))
	for _, key := range keys {
		value := element[key]
		if value == nil {
			return nil, fmt.Errorf("element %d field %q must not be null", index, key)
		}
		contents = append(contents, &sls.LogContent{
			Key:   proto.String(key),
			Value: proto.String(fmt.Sprint(value)),
		})
	}
	return &sls.Log{
		Time:     proto.Uint32(uint32(observedAt.UTC().Unix())),
		Contents: contents,
	}, nil
}

func newEntityStoreLogGroup(logs []*sls.Log) *sls.LogGroup {
	return &sls.LogGroup{
		Topic:  proto.String("umodel-entity-store"),
		Source: proto.String("sre-sync"),
		Logs:   logs,
	}
}

func isRelationElement(element map[string]any) bool {
	_, ok := element["__src_domain__"]
	return ok
}

func validateEntityElement(element map[string]any) error {
	for _, field := range []string{
		"__domain__",
		"__entity_type__",
		"__entity_id__",
		"__last_observed_time__",
	} {
		value, ok := element[field]
		if !ok || fmt.Sprint(value) == "" {
			return fmt.Errorf("missing required EntityStore field %q", field)
		}
	}
	return nil
}

func validateRelationElement(element map[string]any) error {
	for _, field := range []string{
		"__src_domain__",
		"__src_entity_type__",
		"__src_entity_id__",
		"__dest_domain__",
		"__dest_entity_type__",
		"__dest_entity_id__",
		"__relation_type__",
	} {
		value, ok := element[field]
		if !ok || fmt.Sprint(value) == "" {
			return fmt.Errorf("missing required EntityStore relation field %q", field)
		}
	}
	return nil
}

func buildGetEntityStoreDataRequest(workspace, domain, entityType string, observedAt time.Time) (*openapi.Params, *openapi.OpenApiRequest, error) {
	if domain != "sre" || entityType != "sre.service_endpoint" {
		return nil, nil, fmt.Errorf("unsupported EntityStore inspection target %q/%q", domain, entityType)
	}
	return buildGetEntityStoreDataRequestWithQuery(
		workspace,
		".entity with(domain='"+domain+"', type='"+entityType+"') | limit 0, 10",
		observedAt,
	)
}

// buildGetEntityStoreRelationRequest builds the documented outbound-neighbor
// traversal. It filters the response to the configured relation type and the
// exact destination entity ID without using graph-match edge filters.
func buildGetEntityStoreRelationRequest(workspace string, endpoint Endpoint, relation Relation, observedAt time.Time) (*openapi.Params, *openapi.OpenApiRequest, error) {
	plan, err := BuildPlan(endpoint, relation, observedAt)
	if err != nil {
		return nil, nil, err
	}
	if len(plan.Elements) != 2 {
		return nil, nil, fmt.Errorf("relation inspection requires a configured relation")
	}
	relationElement := plan.Elements[1]
	sourceEntityID := fmt.Sprint(relationElement["__src_entity_id__"])
	destinationEntityID := fmt.Sprint(relationElement["__dest_entity_id__"])
	if !entityIDPattern.MatchString(sourceEntityID) || !entityIDPattern.MatchString(destinationEntityID) {
		return nil, nil, fmt.Errorf("relation inspection requires 128-bit hexadecimal entity IDs")
	}

	query := ".topo | graph-call getNeighborNodes('sequence_out', 1, [(:\"" +
		fmt.Sprint(relationElement["__src_domain__"]) + "@" + fmt.Sprint(relationElement["__src_entity_type__"]) +
		"\" {__entity_id__: '" + sourceEntityID + "'})]) | where relationType = '" +
		fmt.Sprint(relationElement["__relation_type__"]) + "' | extend dest_id = json_extract_scalar(destNode, '$.properties.__entity_id__') | where dest_id = '" +
		destinationEntityID + "'"
	return buildGetEntityStoreDataRequestWithQuery(workspace, query, observedAt)
}

func buildGetEntityStoreDataRequestWithQuery(workspace, query string, observedAt time.Time) (*openapi.Params, *openapi.OpenApiRequest, error) {
	if !workspacePattern.MatchString(workspace) {
		return nil, nil, fmt.Errorf("unsafe workspace name %q", workspace)
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
				"query": query,
			},
		}, nil
}
