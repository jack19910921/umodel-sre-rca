# Customer-hosted RCA Worker Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Safely consume persisted CloudMonitor incidents on the customer ECS, collect only allowlisted evidence, run the bounded local Claude Code/DeepSeek RCA Skill, and keep the same Feishu card correct through failure, retry, and recovery.

**Architecture:** Keep `sre-gateway` as a short-lived HTTP ingress process. Add a separate unprivileged `sre-worker` process that composes SQLite, a reviewed incident-selector binding registry, the fixed evidence service, Feishu client, and bounded Claude runner. Incident recovery is a transactional state change that cancels outstanding jobs before the card is updated.

**Tech Stack:** Go 1.23, modernc SQLite, YAML, standard-library `os/exec`, existing Alibaba CLI EcsRamRole profile, Claude Code with embedded DeepSeek, Feishu REST API, systemd.

## Global Constraints

- CloudMonitor remains the only incident source; metrics, SLS logs, UModel and ActionTrail remain evidence sources.
- Keep raw cloud telemetry in the customer account. SQLite stores only incident state, job state, evidence summaries and query references.
- Do not add MCP, arbitrary URL execution, arbitrary SQL, shell fragments, cloud write actions, Alibaba Cloud AK/SK, or `--dangerously-skip-permissions`.
- `sre-evidence` continues to accept exactly five fixed command forms and Claude receives only `Bash(/opt/sre-rca/bin/sre-evidence:*)`.
- All runtime processes run as `sre-rca`; fixed paths are `/usr/bin/claude`, `/opt/sre-rca/bin/sre-evidence`, `/opt/sre-rca/runtime`, and `/var/lib/sre-rca`.
- The final live fault is an HTTP probe plus removal/restoration of TCP/80 only. TCP/22 remains unchanged.
- Preserve the validated `/v1/inbound/cloudmonitor` and `/capture` ingress routes. Do not change the existing blog site.

---

### Task 1: Make jobs terminal and make recovery win every race

**Files:**
- Modify: `internal/domain/incident.go`
- Modify: `internal/store/sqlite.go`
- Modify: `internal/store/sqlite_test.go`
- Modify: `internal/httpapi/server.go`
- Modify: `internal/httpapi/server_test.go`
- Modify: `internal/worker/worker.go`
- Modify: `internal/worker/worker_test.go`

**Interfaces:** Replace the split recovery mutation with `Recover(ctx, incidentKey, at) (domain.Incident, bool, error)`. Add `CompleteJob`, `RetryOrFailJob`, and `CancelJobsForIncident`; a worker must never complete or update a recovered incident.

- [ ] **Step 1: Write failing lifecycle tests**

```go
func TestRecoverCancelsQueuedJob(t *testing.T) {
    repo, in := newActiveIncidentWithQueuedJob(t)
    got, recovered, err := repo.Recover(context.Background(), in.Key, time.Unix(200, 0))
    if err != nil || !recovered || got.State != domain.IncidentRecovered { t.Fatalf("got=%#v recovered=%v err=%v", got, recovered, err) }
    if status := onlyJobStatus(t, repo, in.ID); status != domain.JobCancelled { t.Fatalf("status=%s", status) }
}

func TestWorkerDoesNotOverwriteRecoveredIncident(t *testing.T) {
    w, repo, cards := newWorker(t, validResult())
    in := enqueueFixtureIncident(t, repo)
    mustRecover(t, repo, in)
    if err := w.RunOne(context.Background()); err != nil { t.Fatal(err) }
    if got := mustIncidentByID(t, repo, in.ID); got.State != domain.IncidentRecovered { t.Fatalf("state=%s", got.State) }
    if cards.updates != 0 { t.Fatalf("updates=%d", cards.updates) }
}
```

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/store ./internal/worker ./internal/httpapi -run 'Test(RecoverCancels|WorkerDoesNotOverwrite)' -count=1`

Expected: FAIL because terminal job and atomic recovery APIs do not exist.

- [ ] **Step 3: Implement the minimal state machine**

```go
const (
    JobQueued = "QUEUED"
    JobRunning = "RUNNING"
    JobCompleted = "COMPLETED"
    JobCancelled = "CANCELLED"
    JobFailed = "FAILED"
)

type Repository interface {
    Recover(context.Context, string, time.Time) (domain.Incident, bool, error)
    CompleteJob(context.Context, string) error
    RetryOrFailJob(context.Context, domain.Job, time.Time, int) (bool, error)
}
```

Implement `Recover` in one SQLite transaction: select one active incident by key, set it to `RECOVERED`, then set its jobs in `QUEUED` or `RUNNING` to `CANCELLED`. `ClaimNext` may claim only `QUEUED`. Every `Complete`, `Fail`, and `ScheduleAuditRetry` must require the incident to remain active; use an exported `store.ErrIncidentInactive` so Worker treats a concurrent recovery as a harmless cancellation. Mark the claimed job `COMPLETED` only after a successful completed card update; retry failures with a bounded exponential-free delay and turn the incident/job `FAILED` on attempt three.

- [ ] **Step 4: Wire recovery notification**

```go
type RecoveryNotifier interface {
    UpdateIncidentCard(context.Context, string, domain.Incident, domain.RCAResult) error
}

// In the POST handler, after repo.Recover succeeds:
if recovered && incident.FeishuMessageID != "" && notifier != nil {
    if err := notifier.UpdateIncidentCard(ctx, incident.FeishuMessageID, incident,
        domain.RCAResult{Summary: "CloudMonitor alert recovered."}); err != nil { /* return 500 */ }
}
```

Keep `NewGateway` backward compatible by accepting the recovery notifier as an optional final dependency. Existing callback tests retain the no-notifier path; add one test proving a recovery calls the notifier once when a message ID exists.

- [ ] **Step 5: Verify GREEN and commit**

Run: `go test -race ./internal/store ./internal/httpapi ./internal/worker -count=1`

Expected: PASS.

Commit: `git add internal/domain/incident.go internal/store internal/httpapi internal/worker && git commit -m "feat: make RCA job recovery transactional"`

### Task 2: Add reviewed incident selector bindings and concrete evidence composition

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Create: `internal/runtime/incident_source.go`
- Create: `internal/runtime/incident_source_test.go`
- Create: `internal/runtime/evidence.go`
- Create: `internal/runtime/evidence_test.go`
- Modify: `internal/provider/exec.go`
- Modify: `internal/provider/aliyun.go`
- Modify: `internal/provider/aliyun_test.go`
- Modify: `configs/sre.example.yaml`
- Create: `configs/incident-bindings.example.yaml`

**Interfaces:** `runtime.NewEvidenceService(cfg, repo)` creates `*evidence.Service`; `runtime.NewIncidentSource(repo, bindings)` resolves only reviewed selectors from one incident's workspace/rule/resource tuple.

- [ ] **Step 1: Write failing configuration and selector tests**

```go
func TestIncidentSourceRejectsUnmappedCloudMonitorRule(t *testing.T) {
    source := NewIncidentSource(fakeRepo{incident: domain.Incident{Workspace: "ws", RuleID: "unknown", ResourceID: "r"}}, Bindings{})
    _, err := source.ResolveIncident(context.Background(), "inc-1")
    if err == nil || !strings.Contains(err.Error(), "no incident binding") { t.Fatalf("err=%v", err) }
}

func TestIncidentSourceUsesReviewedBindingOnly(t *testing.T) {
    source := NewIncidentSource(fakeRepo{incident: domain.Incident{Workspace: "ws", RuleID: "probe-rule", ResourceID: "task-1"}}, Bindings{{Workspace:"ws", RuleID:"probe-rule", ResourceID:"task-1", Selectors:evidence.Selectors{"endpoint_id":"blog-http", "instance_id":"i-demo"}}})
    got, err := source.ResolveIncident(context.Background(), "inc-1")
    if err != nil || got.Selectors["endpoint_id"] != "blog-http" { t.Fatalf("got=%#v err=%v", got, err) }
}
```

- [ ] **Step 2: Verify RED**

Run: `go test ./internal/runtime ./internal/config ./internal/provider -run 'Test(IncidentSource|Aliyun)' -count=1`

Expected: FAIL because no runtime composition or binding loader exists.

- [ ] **Step 3: Implement fixed selector and executor boundaries**

```yaml
incident_bindings:
  - workspace: default-cms-your-account-cn-hangzhou
    rule_id: replace-after-http-probe-rule-created
    resource_id: replace-after-probe-callback-captured
    selectors:
      endpoint_id: blog-http
      probe_task_id: replace-after-probe-task-created
      instance_id: i-replace
      region_id: cn-hangzhou
      account_id: replace-with-account-id
      security_group_id: sg-replace
```

`IncidentSource` must return a 30-minute window ending at the incident alert time and must reject duplicate binding keys and empty selector values. Implement `provider.OSExecutor` with `exec.CommandContext`, `Stdout` capture capped at 1 MiB, no shell, and command timeout inherited from context. Add SLS project/logstore and evidence/incident-binding paths to `Config`; reject blank values only when worker mode is enabled. Create exactly one `AliyunProvider` instance and register it under the existing five provider names; do not add an arbitrary provider lookup.

Validate every selector before composing UModel or SLS queries. Preserve the existing Alibaba API allowlist and hash/redact raw response bodies into Evidence references rather than passing raw body text to Claude.

- [ ] **Step 4: Verify GREEN and commit**

Run: `go test -race ./internal/runtime ./internal/config ./internal/provider ./internal/evidence -count=1`

Expected: PASS.

Commit: `git add internal/runtime internal/config internal/provider configs/sre.example.yaml configs/incident-bindings.example.yaml && git commit -m "feat: compose reviewed RCA evidence selectors"`

### Task 3: Make `sre-evidence` and Claude execution usable by the service account

**Files:**
- Modify: `cmd/sre-evidence/main.go`
- Modify: `cmd/sre-evidence/main_test.go`
- Modify: `internal/cc/runner.go`
- Modify: `internal/cc/runner_test.go`
- Modify: `.claude/skills/rca-investigate/SKILL.md`
- Modify: `configs/sre.example.yaml`

**Interfaces:** The CLI loads fixed runtime configuration itself and exposes only five immutable forms. `cc.Runner` executes the fixed binary, fixed runtime directory, fixed skill, bounded timeout, and parses only a validated RCA JSON result.

- [ ] **Step 1: Write failing service-composition and command tests**

```go
func TestCLIConstructsConfiguredService(t *testing.T) {
    cfg := writeRuntimeConfig(t)
    code := run([]string{"--config", cfg, "incident", "context", "inc-1"}, &out, &errOut, nil)
    if code != 0 || !strings.Contains(out.String(), "evidence") { t.Fatalf("code=%d out=%s err=%s", code, out.String(), errOut.String()) }
}

func TestRunnerSetsFixedWorkingDirectoryAndTimeout(t *testing.T) {
    cmd := buildCommand("/usr/bin/claude", "/opt/sre-rca/bin/sre-evidence", "/opt/sre-rca/runtime", "inc-1", 8)
    if cmd.Dir != "/opt/sre-rca/runtime" || !strings.Contains(strings.Join(cmd.Args, " "), "Bash(/opt/sre-rca/bin/sre-evidence:*)") { t.Fatalf("cmd=%#v", cmd) }
}
```

- [ ] **Step 2: Verify RED**

Run: `go test ./cmd/sre-evidence ./internal/cc -run 'Test(CLIConstructs|RunnerSets)' -count=1`

Expected: FAIL because the CLI always receives a nil service and the runner has no fixed directory.

- [ ] **Step 3: Implement the fixed invocation**

`main` accepts one optional leading `--config /etc/sre-rca/sre.yaml`, then one of the five existing command triplets; it never accepts cloud command arguments. Use a package-level constructor injected by tests and `runtime.NewEvidenceService` in production. `cc.Runner` gains `WorkingDirectory`, validates it is absolute, creates `context.WithTimeout(ctx, timeout)`, uses `exec.CommandContext`, and captures bounded combined output for diagnostics without persisting raw model output. The Skill contains the four evidence collection commands and a strict JSON-only final response; it does not contain any Feishu, cloud credential, or write instruction.

- [ ] **Step 4: Verify GREEN and commit**

Run: `go test -race ./cmd/sre-evidence ./internal/cc -count=1`

Expected: PASS.

Commit: `git add cmd/sre-evidence internal/cc .claude/skills/rca-investigate configs/sre.example.yaml && git commit -m "feat: run bounded RCA evidence skill"`

### Task 4: Add a separate worker process and customer-hosted preflight

**Files:**
- Create: `cmd/sre-worker/main.go`
- Create: `cmd/sre-worker/main_test.go`
- Create: `deploy/worker/sre-worker.service`
- Create: `scripts/verify-worker-preflight.sh`
- Modify: `deploy/bootstrap/README.md`
- Modify: `docs/runbooks/mvp-demo.md`

**Interfaces:** `sre-worker` owns one polling loop and invokes `Worker.RunOne` with the configured job timeout. `sre-gateway` remains HTTP ingress only.

- [ ] **Step 1: Write failing poll-loop tests**

```go
func TestRunLoopUsesConfiguredTimeout(t *testing.T) {
    runner := &fakeRunner{}
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    go runLoop(ctx, fakeWorker{run: runner.RunOne}, 5*time.Millisecond, 20*time.Millisecond)
    <-runner.started
    if deadline, ok := runner.context.Deadline(); !ok || time.Until(deadline) > 30*time.Millisecond { t.Fatal("missing bounded deadline") }
}
```

- [ ] **Step 2: Verify RED**

Run: `go test ./cmd/sre-worker -run TestRunLoopUsesConfiguredTimeout -count=1`

Expected: FAIL because the worker command does not exist.

- [ ] **Step 3: Implement worker process and preflight**

`cmd/sre-worker` loads the same config and composes repo, binding registry, evidence service, Feishu client, and `cc.Runner`. The loop calls `RunOne` every configured poll interval; each call gets a fresh timeout context and treats `store.ErrIncidentInactive` as success. Any other error is logged once and the loop continues.

The systemd unit runs as `sre-rca`, has `WorkingDirectory=/opt/sre-rca/runtime`, `EnvironmentFile=/etc/sre-rca/sre.env`, `NoNewPrivileges=true`, `PrivateTmp=true`, `ProtectHome=true`, `ProtectSystem=strict`, and only `ReadWritePaths=/var/lib/sre-rca`. It starts after `sre-gateway.service` but does not restart the gateway.

`verify-worker-preflight.sh` uses `set -euo pipefail`, confirms executable permissions, verifies `/usr/bin/claude -p 'Return exactly {"ok":true}' --output-format json --max-turns 1` as `sre-rca`, invokes one fixed `sre-evidence incident context` fixture, and checks `aliyun sts GetCallerIdentity --profile sre-ecs-role`. It never prints environment variables or credentials.

- [ ] **Step 4: Verify GREEN and commit**

Run: `go test -race ./cmd/sre-worker ./internal/worker && bash -n scripts/verify-worker-preflight.sh`

Expected: PASS.

Commit: `git add cmd/sre-worker deploy/worker scripts/verify-worker-preflight.sh deploy/bootstrap/README.md docs/runbooks/mvp-demo.md && git commit -m "feat: add customer-hosted RCA worker runtime"`

## Live Acceptance After Code Review

1. Add an HTTP-only `sre-rca.jack-sre.com` Nginx `/healthz` server block that returns `200`; do not change the blog configuration.
2. Create a one-minute CloudMonitor HTTP probe for `http://sre-rca.jack-sre.com/healthz`, add a distinct capture-only webhook, and capture one `OCCURRED` plus one `RECOVERED` callback before normal cutover.
3. Populate the reviewed incident binding with the probe callback's workspace, rule ID, resource ID, probe task ID, ECS instance ID, security group ID, account ID, and region.
4. Attach a least-privilege ECS RAM role, configure SLS/ActionTrail read access, import the `sre` UModel domain, configure Logtail, and verify one record from each evidence source.
5. Configure the Feishu self-built app credentials only in `/etc/sre-rca/sre.env`, run the worker preflight as `sre-rca`, then enable `sre-worker.service`.
6. Remove TCP/80 only, wait for one card to show evidence-backed analysis, restore TCP/80, and verify the same card changes to `RECOVERED`.

## Self-Review

- Spec coverage: Tasks 1-4 cover job lifecycle, recovery, selector routing, fixed evidence execution, bounded Claude invocation, worker isolation, and preflight. The six live acceptance steps cover UModel, probe, SLS, ActionTrail, Feishu, and the TCP/80-only fault.
- Placeholder scan: no `TODO`, `TBD`, or unspecified error handling remains.
- Type consistency: `Recover` is the single recovery mutation; `IncidentSource` feeds `evidence.Service`; `sre-worker` owns `Worker.RunOne`; `sre-gateway` remains ingress-only.
