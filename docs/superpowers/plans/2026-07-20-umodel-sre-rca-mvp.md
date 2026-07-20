# UModel SRE RCA MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a customer-account-local RCA MVP that turns a CloudMonitor HTTP probe alert for one ECS-hosted Nginx endpoint into an evidence-backed, updatable Feishu incident card.

**Architecture:** A Go gateway accepts CloudMonitor callbacks, deduplicates incidents in SQLite, and queues jobs. A worker runs one bounded Claude Code process per incident. Its project-local `rca-investigate` Skill can call only the `sre-evidence` CLI, which resolves UModel context and invokes read-only CloudMonitor, SLS, and ActionTrail providers.

**Tech Stack:** Go 1.23, `modernc.org/sqlite`, standard-library `net/http`, `gopkg.in/yaml.v3`, Alibaba Cloud CLI `EcsRamRole` profile, Claude Code CLI, Feishu REST API, CloudMonitor 2.0/UModel, SLS Logtail.

## Global Constraints

- Independent from ARA; do not import, copy, or modify `/Users/jack/Desktop/aliyun-risk-auditor`.
- Keep raw metrics, logs, and audit events in the customer Alibaba Cloud account.
- Create one `sre` UModel domain and reuse `acs` resource entities; do not create `sre.ecs`.
- DataLink/StorageLink express semantics. Console URL templates are human-only output, never query input.
- CloudMonitor is the only incident source. Metrics, logs, and ActionTrail are evidence sources.
- `sre-evidence` accepts only five fixed read-only commands; no arbitrary SQL, URL, shell fragment, or cloud action.
- CC may invoke only `Bash(/opt/sre-rca/bin/sre-evidence:*)`; never use `--dangerously-skip-permissions`.
- Gateway/Worker own Feishu card updates. CC receives no Feishu credential, cloud credential, or write capability.
- Use an ECS Instance RAM Role and Alibaba CLI `EcsRamRole` profile. Do not store Alibaba Cloud AK/SK.
- Fault injection removes TCP/80 only; keep TCP/22 unchanged.
- TDD each task and commit only the files created by that task. Do not stage the existing research markdown.

---

## File Structure

```text
go.mod
cmd/sre-gateway/main.go
cmd/sre-evidence/main.go
internal/config/{config.go,config_test.go}
internal/domain/incident.go
internal/store/{sqlite.go,sqlite_test.go}
internal/inbound/{cloudmonitor.go,cloudmonitor_test.go}
internal/httpapi/{server.go,server_test.go}
internal/evidence/{types.go,registry.go,registry_test.go,service.go,service_test.go}
internal/provider/{exec.go,aliyun.go,aliyun_test.go}
internal/feishu/{client.go,client_test.go}
internal/cc/{runner.go,runner_test.go}
internal/worker/{worker.go,worker_test.go}
configs/{sre.example.yaml,evidence-bindings.yaml}
umodel/{sre-domain.yaml,sre-entities.yaml,sre-datasets.yaml,sre-links.yaml,sre-storage.yaml}
.claude/skills/rca-investigate/SKILL.md
deploy/{sre-gateway.service,nginx/sre-rca.conf}
scripts/{install-logtail.sh,verify-mvp.sh}
docs/runbooks/mvp-demo.md
testdata/*.json
```

## Task 1: Bootstrap Go configuration

**Files:**
- Create: `go.mod`
- Create: `cmd/sre-gateway/main.go`
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `configs/sre.example.yaml`
- Create: `testdata/valid-config.yaml`
- Create: `testdata/invalid-config.yaml`

**Interfaces:** Produces `config.Load(path string) (config.Config, error)` used by all runtime components.

- [ ] **Step 1: Write the failing tests**

```go
func TestLoadRejectsMissingCallbackToken(t *testing.T) {
    _, err := Load("../../testdata/invalid-config.yaml")
    if err == nil || !strings.Contains(err.Error(), "callback_token") { t.Fatalf("err=%v", err) }
}
func TestLoadAcceptsEcsRAMRole(t *testing.T) {
    cfg, err := Load("../../testdata/valid-config.yaml")
    if err != nil { t.Fatal(err) }
    if cfg.Aliyun.Profile != "sre-ecs-role" || cfg.Worker.MaxTurns != 8 { t.Fatalf("cfg=%#v", cfg) }
}
```

- [ ] **Step 2: Verify the tests fail**

Run: `go test ./internal/config -run TestLoad -v`
Expected: FAIL because package `internal/config` is absent.

- [ ] **Step 3: Implement the smallest loader**

```go
type Config struct {
    ListenAddr, CallbackToken, StateDB string
    Aliyun struct { Profile, Workspace, Region string } `yaml:"aliyun"`
    Feishu struct { AppID, AppSecret, ChatID string } `yaml:"feishu"`
    Worker struct { MaxTurns, TimeoutSeconds int } `yaml:"worker"`
}
func Load(path string) (Config, error) {
    var cfg Config
    raw, err := os.ReadFile(path); if err != nil { return cfg, err }
    if err := yaml.Unmarshal(raw, &cfg); err != nil { return cfg, err }
    if cfg.CallbackToken == "" || cfg.StateDB == "" || cfg.Aliyun.Profile == "" || cfg.Worker.MaxTurns < 1 { return cfg, errors.New("callback_token, state_db, aliyun.profile, and worker.max_turns are required") }
    return cfg, nil
}
```

Use Go 1.23 with `modernc.org/sqlite` and `gopkg.in/yaml.v3`; put production secrets only in `/etc/sre-rca/sre.env`.

- [ ] **Step 4: Verify success**

Run: `go test ./internal/config -run TestLoad -v && go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

Run: `git add go.mod cmd/sre-gateway/main.go internal/config configs/sre.example.yaml testdata/valid-config.yaml testdata/invalid-config.yaml && git commit -m "feat: bootstrap SRE RCA configuration"`

## Task 2: Define incidents and transactional SQLite jobs

**Files:**
- Create: `internal/domain/incident.go`
- Create: `internal/store/sqlite.go`
- Create: `internal/store/sqlite_test.go`

**Interfaces:** Produces `domain.Alert`, `domain.Incident`, `domain.Job`, `domain.Evidence`, `domain.RCAResult`, and `store.Repository`.

- [ ] **Step 1: Write failing idempotency tests**

```go
func TestCreateOrGetIncidentDeduplicatesActiveAlert(t *testing.T) {
    repo := newTestRepo(t); in := domain.NewIncident("ws", "rule-1", "i-demo", time.Unix(100, 0))
    one, created, err := repo.CreateOrGetIncident(context.Background(), in)
    if err != nil || !created { t.Fatalf("one=%#v created=%v err=%v", one, created, err) }
    two, created, err := repo.CreateOrGetIncident(context.Background(), in)
    if err != nil || created || one.ID != two.ID { t.Fatalf("two=%#v created=%v err=%v", two, created, err) }
}
func TestClaimNextClaimsOneJob(t *testing.T) {
    repo := newTestRepo(t); inc := mustIncident(t, repo); mustEnqueue(t, repo, inc.ID)
    job, ok, err := repo.ClaimNext(context.Background(), "worker-a", time.Now())
    if err != nil || !ok || job.Status != domain.JobRunning { t.Fatalf("job=%#v ok=%v err=%v", job, ok, err) }
}
```

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/store -run 'Test(CreateOrGet|ClaimNext)' -v`
Expected: FAIL because `Repository` is absent.

- [ ] **Step 3: Implement model and repository**

```go
type Alert struct { Workspace, RuleID, ResourceID, State string; EventAt time.Time }
type Incident struct { ID, Key, Workspace, RuleID, ResourceID, State, FeishuMessageID string; AlertAt, CreatedAt, UpdatedAt time.Time }
type Job struct { ID, IncidentID, Status, WorkerID string; Attempt int; RunAfter time.Time }
type Repository interface {
    CreateOrGetIncident(context.Context, domain.Incident) (domain.Incident, bool, error)
    Enqueue(context.Context, string, time.Time) error
    ClaimNext(context.Context, string, time.Time) (domain.Job, bool, error)
    Complete(context.Context, string, domain.RCAResult) error
    Fail(context.Context, string, string, time.Time) error
    MarkRecovered(context.Context, string, time.Time) error
}
```

Create `incidents`, `jobs`, and `evidence_snapshots`. Derive `incident_key` from workspace, rule ID, and resource ID. Use a unique partial index for active states `RECEIVED`, `INVESTIGATING`, `AWAITING_AUDIT_EVENT`; claim jobs in one `BEGIN IMMEDIATE` transaction.

- [ ] **Step 4: Verify success**

Run: `go test -race ./internal/store -v`
Expected: PASS.

- [ ] **Step 5: Commit**

Run: `git add internal/domain internal/store && git commit -m "feat: add idempotent incident job queue"`

## Task 3: Establish the CloudMonitor callback boundary

**Files:**
- Create: `internal/inbound/cloudmonitor.go`
- Create: `internal/inbound/cloudmonitor_test.go`
- Create: `internal/httpapi/server.go`
- Create: `internal/httpapi/server_test.go`
- Create: `testdata/cloudmonitor-alert.json`
- Create: `testdata/cloudmonitor-recovery.json`

**Interfaces:** Produces `inbound.ParseCloudMonitor([]byte) (domain.Alert, error)` and `POST /v1/inbound/cloudmonitor?token=<token>`.

- [ ] **Step 1: Capture real callback fixtures**

Configure a temporary callback URL, trigger one alert and one recovery, save bodies as the two fixtures, redact only values, and preserve all JSON keys. This is the source of truth for the parser field map.

- [ ] **Step 2: Write failing HTTP tests**

```go
func TestCallbackCreatesOneIncident(t *testing.T) {
    srv, repo := newTestServer(t)
    req := httptest.NewRequest(http.MethodPost, "/v1/inbound/cloudmonitor?token=test-token", bytes.NewReader(readFixture(t, "cloudmonitor-alert.json")))
    rec := httptest.NewRecorder(); srv.ServeHTTP(rec, req)
    if rec.Code != http.StatusOK || countIncidents(t, repo) != 1 { t.Fatalf("code=%d", rec.Code) }
}
func TestCallbackRejectsWrongToken(t *testing.T) {
    srv, _ := newTestServer(t); rec := httptest.NewRecorder()
    srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/inbound/cloudmonitor?token=wrong", nil))
    if rec.Code != http.StatusUnauthorized { t.Fatalf("code=%d", rec.Code) }
}
```

- [ ] **Step 3: Implement parser and handler**

Add `cloudmonitor_field_map` JSON paths for workspace, rule ID, resource ID, status, and event time to config. Normalize only `ALERT` and `RECOVERED`. Authenticate using `subtle.ConstantTimeCompare`; create/enqueue only new alerts; mark active incidents on recovery; always return `{"accepted":true,"incident_id":"..."}` after persistence.

- [ ] **Step 4: Verify success**

Run: `go test ./internal/inbound ./internal/httpapi -v`
Expected: PASS for alert, duplicate, recovery, and invalid-token inputs.

- [ ] **Step 5: Commit**

Run: `git add internal/inbound internal/httpapi testdata/cloudmonitor-*.json configs/sre.example.yaml && git commit -m "feat: ingest CloudMonitor RCA alerts"`

## Task 4: Create the UModel `sre` domain and collect Nginx logs

**Files:**
- Create: `umodel/sre-domain.yaml`
- Create: `umodel/sre-entities.yaml`
- Create: `umodel/sre-datasets.yaml`
- Create: `umodel/sre-links.yaml`
- Create: `umodel/sre-storage.yaml`
- Create: `scripts/install-logtail.sh`
- Create: `docs/runbooks/mvp-demo.md`

**Interfaces:** Produces `sre.service --serves--> sre.service_endpoint --hosted_by--> acs.ecs.instance`, plus DataLinks for probe, logs, and security-group changes.

- [ ] **Step 1: Put this exact UModel acceptance checklist in the runbook**

```text
1. Domain `sre` exists.
2. `sre.service_endpoint` includes account_id, region_id, instance_id, host, probe_task_id.
3. The demo endpoint resolves through hosted_by to its acs.ecs.instance.
4. Nginx access/error LogSets StorageLink to SLS Logstore sre-nginx-demo.
5. Nginx LogSets DataLink by instance_id and host.
6. acs.ecs.securitygroup DataLink to sre.change_event by account_id, region_id, resource_id.
```

- [ ] **Step 2: Verify it fails before import**

Run: execute checks 1–6 in UModel Explorer.
Expected: at least one check fails before YAML import and SLS configuration.

- [ ] **Step 3: Implement model and installer**

```yaml
kind: entity_set_link
metadata:
  name: sre.service_endpoint_hosted_by_acs.ecs.instance
  domain: sre
spec:
  src: {domain: sre, kind: entity_set, name: sre.service_endpoint}
  dest: {domain: acs, kind: entity_set, name: acs.ecs.instance}
  entity_link_type: hosted_by
  fields_mapping: {account_id: account_id, region_id: region_id, instance_id: instance_id}
```

Model `sre.service`, `sre.service_endpoint`, `sre.availability_event`, `sre.nginx_access_log`, `sre.nginx_error_log`, and `sre.change_event`. The script writes `/etc/ilogtail/conf.d/sre-nginx.json` for Nginx access/error logs, adds `instance_id`/`host`, executes `nginx -t`, and restarts Logtail only after that test succeeds.

- [ ] **Step 4: Verify success**

Run: `curl -fsS http://127.0.0.1/health` and query `sre-nginx-demo` for the instance ID.
Expected: one HTTP 200 record and all six Explorer checks pass.

- [ ] **Step 5: Commit**

Run: `git add umodel scripts/install-logtail.sh docs/runbooks/mvp-demo.md && git commit -m "feat: add SRE UModel and Nginx telemetry"`

## Task 5: Implement Binding Registry and fixed Evidence CLI

**Files:**
- Create: `internal/evidence/types.go`
- Create: `internal/evidence/registry.go`
- Create: `internal/evidence/registry_test.go`
- Create: `internal/evidence/service.go`
- Create: `internal/evidence/service_test.go`
- Create: `cmd/sre-evidence/main.go`
- Create: `configs/evidence-bindings.yaml`
- Create: `testdata/binding-with-query-url.yaml`

**Interfaces:** Produces `evidence.Service.Context/Metrics/Logs/Changes` and commands `incident context`, `metrics query`, `logs query`, `changes query`, `evidence open`.

- [ ] **Step 1: Write failing binding and command tests**

```go
func TestRegistryRejectsURLAsQueryMechanism(t *testing.T) {
    _, err := LoadRegistry("../../testdata/binding-with-query-url.yaml")
    if err == nil || !strings.Contains(err.Error(), "query_url is not supported") { t.Fatalf("err=%v", err) }
}
func TestCLIRejectsNonFixedCommand(t *testing.T) {
    if code := runEvidence(t, "sql", "select *"); code != 2 { t.Fatalf("code=%d", code) }
}
```

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/evidence ./cmd/sre-evidence -run 'Test(Registry|CLI)' -v`
Expected: FAIL because registry and dispatcher are absent.

- [ ] **Step 3: Implement bindings and CLI**

```go
type Binding struct {
    ID, Target, Provider, QueryTemplate, ConsoleLinkTemplate string
    SelectorMapping map[string]string `yaml:"selector_mapping"`
}
type Service interface {
    Context(context.Context, string) ([]domain.Evidence, error)
    Metrics(context.Context, string) ([]domain.Evidence, error)
    Logs(context.Context, string) ([]domain.Evidence, error)
    Changes(context.Context, string) ([]domain.Evidence, error)
}
```

Reject `query_url`, credentials, shell fragments, and unknown selectors. Permit `console_link_template` only in the Evidence output. Dispatch exactly the five named commands with one incident/evidence ID.

- [ ] **Step 4: Verify success**

Run: `go test ./internal/evidence ./cmd/sre-evidence -v && go run ./cmd/sre-evidence incident context inc_fixture`
Expected: PASS and JSON containing `evidence`.

- [ ] **Step 5: Commit**

Run: `git add internal/evidence cmd/sre-evidence configs/evidence-bindings.yaml testdata/binding-with-query-url.yaml && git commit -m "feat: add declarative RCA evidence CLI"`

## Task 6: Add read-only Alibaba Cloud providers

**Files:**
- Create: `internal/provider/exec.go`
- Create: `internal/provider/aliyun.go`
- Create: `internal/provider/aliyun_test.go`
- Create: `testdata/aliyun-get-entity-store-data.json`
- Create: `testdata/aliyun-cms-metrics.json`
- Create: `testdata/aliyun-sls-logs.json`
- Create: `testdata/aliyun-actiontrail-events.json`

**Interfaces:** Produces `provider.Aliyun.Resolve(ctx, binding, selectors, window) ([]domain.Evidence, error)` used by `evidence.Service`.

- [ ] **Step 1: Write failing allowlist and parsing tests**

```go
func TestAliyunRunnerRejectsWriteAction(t *testing.T) {
    _, err := NewAliyunRunner("sre-ecs-role", fakeExec{}).Run(context.Background(), "ecs", "AuthorizeSecurityGroup", nil)
    if err == nil || !strings.Contains(err.Error(), "not allowlisted") { t.Fatalf("err=%v", err) }
}
func TestActionTrailProviderBuildsChangeEvidence(t *testing.T) {
    got, err := newFixtureProvider(t, "aliyun-actiontrail-events.json").Resolve(context.Background(), changeBinding(), Selectors{"security_group_id":"sg-demo"}, Window{Start:100, End:200})
    if err != nil || got[0].Type != "change" { t.Fatalf("got=%#v err=%v", got, err) }
}
```

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/provider -run 'Test(AliyunRunner|ActionTrailProvider)' -v`
Expected: FAIL because provider is absent.

- [ ] **Step 3: Implement a single allowlisted runner**

```go
var allowedActions = map[string]map[string]bool{
    "cms": {"GetEntityStoreData": true, "GetUmodelData": true, "DescribeMetricList": true},
    "sls": {"GetLogs": true}, "actiontrail": {"LookupEvents": true},
}
func (r *AliyunRunner) Run(ctx context.Context, product, action string, args []string) ([]byte, error) {
    if !allowedActions[product][action] { return nil, fmt.Errorf("%s:%s not allowlisted", product, action) }
    return r.exec.Run(ctx, "aliyun", append([]string{"--profile", r.profile, product, action, "--output", "json"}, args...))
}
```

Use Cms/2024-03-30 `GetEntityStoreData` and `GetUmodelData` for context, CMS metrics for normal-state evidence, SLS `GetLogs` for Nginx evidence, and ActionTrail `LookupEvents` for security-group events. Every adapter gets the Incident time window and produces `Evidence{ID, Type, ObservedAt, Source, QueryRef, Summary, RawRef}`.

- [ ] **Step 4: Verify fixtures and role mode**

Run: `go test ./internal/provider -v`
Expected: PASS.

Run on ECS: `aliyun configure --mode EcsRamRole --profile sre-ecs-role && aliyun sts GetCallerIdentity --profile sre-ecs-role && go run ./cmd/sre-evidence incident context inc_fixture`
Expected: assumed-role ARN and read-only evidence JSON.

- [ ] **Step 5: Commit**

Run: `git add internal/provider testdata/aliyun-*.json && git commit -m "feat: add read-only Alibaba evidence providers"`

## Task 7: Add Feishu cards and the CC Skill runner

**Files:**
- Create: `internal/feishu/client.go`
- Create: `internal/feishu/client_test.go`
- Create: `internal/cc/runner.go`
- Create: `internal/cc/runner_test.go`
- Create: `.claude/skills/rca-investigate/SKILL.md`
- Create: `testdata/feishu-token.json`
- Create: `testdata/feishu-message.json`
- Create: `testdata/rca-result-valid.json`
- Create: `testdata/rca-result-invalid.json`

**Interfaces:** Produces `feishu.Client.CreateIncidentCard/UpdateIncidentCard` and `cc.Runner.Run(ctx, incidentID) (domain.RCAResult, error)`.

- [ ] **Step 1: Write failing card and runner tests**

```go
func TestCreateIncidentCardReturnsMessageID(t *testing.T) {
    id, err := newFeishuFixtureClient(t).CreateIncidentCard(context.Background(), domain.Incident{ID:"inc-1", State:"RECEIVED"})
    if err != nil || id != "om_demo" { t.Fatalf("id=%q err=%v", id, err) }
}
func TestRunnerAllowsOnlyEvidenceCLI(t *testing.T) {
    cmd := buildCommand("claude", "/opt/sre-rca/bin/sre-evidence", "inc-1")
    if !strings.Contains(strings.Join(cmd.Args, " "), "Bash(/opt/sre-rca/bin/sre-evidence:*)") { t.Fatalf("args=%q", cmd.Args) }
}
func TestParseResultRejectsMissingEvidence(t *testing.T) {
    if _, err := ParseResult(readFixture(t, "rca-result-invalid.json")); err == nil { t.Fatal("wanted validation error") }
}
```

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/feishu ./internal/cc -v`
Expected: FAIL because packages are absent.

- [ ] **Step 3: Implement the bounded integration**

Use a Feishu self-built app's tenant token to send one `interactive` card to configured `chat_id`; store its `message_id`; update only phase, summary, confidence, evidence links, and manual next actions; reject a rendered card over 30 KB.

Create `SKILL.md` with `disable-model-invocation: true`. Its exact CLI sequence is:

```text
sre-evidence incident context $ARGUMENTS
sre-evidence metrics query $ARGUMENTS
sre-evidence logs query $ARGUMENTS
sre-evidence changes query $ARGUMENTS
```

It emits `summary`, `confidence`, `root_cause`, `evidence_ids`, `next_actions`; missing ActionTrail data emits state `AWAITING_AUDIT_EVENT`. Use `exec.CommandContext`, never `sh -c`, for `claude -p /rca-investigate <incident-id> --output-format json --max-turns 8 --allowedTools Bash(/opt/sre-rca/bin/sre-evidence:*)`. Reject unknown evidence IDs and confidence outside `[0,1]`.

- [ ] **Step 4: Verify success**

Run: `go test ./internal/feishu ./internal/cc -v`
Expected: PASS.

Run on ECS as the future systemd user: `claude -p "return {\"ok\":true}" --output-format json --max-turns 1`
Expected: exit code 0 and JSON; otherwise configure dedicated non-interactive CC authentication before service enablement.

- [ ] **Step 5: Commit**

Run: `git add internal/feishu internal/cc .claude/skills/rca-investigate testdata/feishu-*.json testdata/rca-result-*.json && git commit -m "feat: add Feishu card and Claude RCA skill runner"`

## Task 8: Orchestrate, deploy, and run the live fault demo

**Files:**
- Create: `internal/worker/worker.go`
- Create: `internal/worker/worker_test.go`
- Create: `deploy/sre-gateway.service`
- Create: `deploy/nginx/sre-rca.conf`
- Create: `scripts/verify-mvp.sh`
- Modify: `cmd/sre-gateway/main.go`
- Modify: `docs/runbooks/mvp-demo.md`

**Interfaces:** Consumes Store, Feishu, CC Runner, Evidence Service; produces systemd-managed Gateway/Worker and an acceptance transcript.

- [ ] **Step 1: Write failing worker tests**

```go
func TestWorkerCompletesAndUpdatesOneCard(t *testing.T) {
    w, repo, cards := newWorker(t, validRunnerResult()); inc := enqueueFixtureIncident(t, repo)
    if err := w.RunOne(context.Background()); err != nil { t.Fatal(err) }
    got := mustIncidentByID(t, repo, inc.ID)
    if got.State != domain.IncidentCompleted || cards.UpdateCount != 2 { t.Fatalf("incident=%#v updates=%d", got, cards.UpdateCount) }
}
func TestWorkerRetriesPendingAuditAfter120Seconds(t *testing.T) {
    w, repo, _ := newWorker(t, pendingAuditResult()); enqueueFixtureIncident(t, repo)
    if err := w.RunOne(context.Background()); err != nil { t.Fatal(err) }
    if got := mustOnlyIncident(t, repo); got.State != domain.IncidentAwaitingAudit { t.Fatalf("state=%s", got.State) }
}
```

- [ ] **Step 2: Verify failure**

Run: `go test ./internal/worker -v`
Expected: FAIL because Worker is absent.

- [ ] **Step 3: Implement state machine and service assets**

```text
RECEIVED -> CONTEXT_READY -> INVESTIGATING -> COMPLETED
                                  -> AWAITING_AUDIT_EVENT -> QUEUED after 120 seconds
                                  -> FAILED after three failed attempts
any active state + recovery callback -> RECOVERED
```

Update the card before CC begins and after each transition. Persist sanitized failures and never invent a root cause. Run systemd as unprivileged `sre-rca`, `WorkingDirectory=/opt/sre-rca/runtime`, `EnvironmentFile=/etc/sre-rca/sre.env`, `Restart=on-failure`, `NoNewPrivileges=true`. Nginx terminates TLS and proxies only `/v1/inbound/cloudmonitor` to `127.0.0.1:8080`; other paths return 404.

- [ ] **Step 4: Verify unit tests and complete live acceptance**

Run: `go test -race ./...`
Expected: PASS.

Then execute exactly:

```text
1. Attach minimum read-only ECS RAM Role; verify STS identity.
2. Import umodel YAML; create demo service/endpoint; verify hosted_by edge.
3. Install Logtail and verify one Nginx 200 log in sre-nginx-demo.
4. Create HTTP probe for http://<EIP>/health with two failures to alert and recovery enabled.
5. Route only its trigger/recovery events through HTTP Action Integration to Gateway.
6. Add Feishu app bot to group and configure secrets in /etc/sre-rca/sre.env.
7. Build, install, start systemd; run bash scripts/verify-mvp.sh.
8. Delete TCP/80 only; verify one Feishu card includes probe, ECS normal, Nginx log, ActionTrail evidence.
9. Restore TCP/80; verify the same card becomes RECOVERED.
```

`verify-mvp.sh` uses `set -euo pipefail` and runs Nginx health curl, `systemctl is-active --quiet sre-gateway`, `aliyun sts GetCallerIdentity --profile sre-ecs-role`, and Gateway `/healthz`.

- [ ] **Step 5: Commit**

Run: `git add internal/worker cmd/sre-gateway/main.go deploy scripts/verify-mvp.sh docs/runbooks/mvp-demo.md && git commit -m "feat: run customer-hosted RCA MVP"`

## Self-Review

### Spec coverage

- `sre` domain and `acs` reuse: Task 4.
- Metrics, logs, and changes: Tasks 4–6.
- Binding/Provider/CLI instead of URL or MCP: Tasks 5–6.
- CloudMonitor-only intake: Task 3.
- Feishu lifecycle card and CC Skill: Task 7.
- Idempotency, audit-delay retry, recovery: Tasks 2, 3, 8.
- Customer-side security and live security-group fault/recovery: Global Constraints and Task 8.

### Placeholder scan

No `TODO`, `TBD`, “implement later”, or generic test/error directive is used. CloudMonitor field names are captured from a redacted real callback before parser code is written in Task 3.

### Type consistency

- `domain.Alert`, `domain.Incident`, `domain.Job`, `domain.Evidence`, `domain.RCAResult` begin in Task 2 and remain unchanged.
- `store.Repository` is the only persistence dependency after Task 2.
- `evidence.Service` in Task 5 consumes the Alibaba Provider in Task 6.
- `cc.Runner.Run` returns `domain.RCAResult`, which Task 8 Worker persists.
