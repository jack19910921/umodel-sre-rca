# MVP Readability Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Simplify the customer-hosted MVP's Provider and UModel write boundaries without changing its verified runtime contracts.

**Architecture:** Keep the public CLI, config and Evidence contracts stable. Extract reviewed-selector request construction from Provider resolution and classify UModel Log Protocol writes before the writer performs any remote call.

**Tech Stack:** Go, SQLite, generated Alibaba Cloud SDKs, SLS Log Protocol, CMS EntityStore API.

## Global Constraints

- Preserve every existing CLI flag, YAML key, Incident/Evidence Binding and SQLite state semantic.
- Never deploy, alter production infrastructure, add credentials, or store raw customer telemetry.
- Entity instances use `${workspace}__entity`; relation instances use `${workspace}__topo`; inspection uses CMS `GetEntityStoreData`.
- SLS evidence uses the regional generated SDK `GetLogs` path and quoted selector values.

---

### Task 1: Isolate reviewed Provider request construction

**Files:**

- Create: `internal/provider/request_builders.go`
- Modify: `internal/provider/aliyun.go`
- Test: `internal/provider/aliyun_test.go`

**Interfaces:** `buildNginxLogsRequest(AliyunConfig, string, evidence.Selectors, evidence.Window) (AliyunRequest, error)` produces one fixed, quoted SLS request. `AliyunProvider.Resolve` retains its existing external signature.

- [x] **Step 1: Write the failing characterization test**

Assert that a `blog-http`/`i-demo` access binding yields exactly `endpoint_id:"blog-http" AND log_kind:"access" AND instance_id:"i-demo"` and operation `GetLogs`.

- [x] **Step 2: Verify RED**

Run: `go test ./internal/provider -run TestBuildNginxLogsRequestQuotesEverySelectorValue -count=1`

Observed: FAIL because `buildNginxLogsRequest` was not defined.

- [x] **Step 3: Implement the minimal boundary**

Add request builders for EntityStore context/topology, CMS metrics, SLS logs and ActionTrail. Route existing Provider resolution through them without changing templates, selectors or summaries.

- [x] **Step 4: Verify GREEN**

Run: `go test ./internal/provider -count=1`

### Task 2: Classify EntityStore writes before SLS I/O

**Files:**

- Create: `internal/modelsync/writes.go`
- Modify: `internal/modelsync/writer.go`
- Test: `internal/modelsync/writer_test.go`

**Interfaces:** `buildEntityStoreWrites(string, Plan, time.Time) ([]entityStoreWrite, error)` returns zero, one or two validated batches in entity-then-topology order. `CMSWriter.Upsert` remains unchanged externally.

- [x] **Step 1: Write the failing characterization test**

Construct one endpoint record and one `runs_on` record; assert the returned batches target `customer-workspace__entity` then `customer-workspace__topo`.

- [x] **Step 2: Verify RED**

Run: `go test ./internal/modelsync -run TestBuildEntityStoreWritesSeparatesEntityAndRelationBatches -count=1`

Observed: FAIL because `buildEntityStoreWrites` was not defined.

- [x] **Step 3: Implement the minimal boundary**

Reuse existing record validation and LogGroup builders; have `CMSWriter` only loop over classified batches and retain prior error labels.

- [x] **Step 4: Verify GREEN**

Run: `go test ./internal/modelsync -count=1`

### Task 3: Document the actual customer-hosted boundary

**Files:**

- Create: `README.md`
- Create: `docs/architecture/mvp-refactor-readability.md`
- Create: `docs/development/team-onboarding.md`

- [x] **Step 1: Document verified, deliberately-disabled and future capabilities separately**

Describe the real CPU/UModel/Feishu validation, the intentionally disabled ActionTrail Binding, and future candidates without presenting them as shipped.

- [x] **Step 2: Add reproducible verification and release safety instructions**

Include the requested tests/builds, sensitive-data checks, no-deployment rule and UModel/CMS/SLS constraints.

- [ ] **Step 3: Run final repository verification and commit in two reviewable changes**

Run the exact user-requested tests/builds and security scan, commit code as `refactor: simplify MVP runtime boundaries`, then docs as `docs: add team onboarding and refactor design`.
