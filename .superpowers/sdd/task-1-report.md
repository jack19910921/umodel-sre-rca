# Task 1 report: transactional recovery and terminal jobs

## Changed files

- `internal/domain/incident.go`: added `COMPLETED`, `CANCELLED`, and `FAILED` job statuses.
- `internal/store/sqlite.go`: replaced `MarkRecovered` with transactional `Recover`; added terminal job operations and `ErrIncidentInactive`; constrained worker state mutations to active incidents.
- `internal/store/sqlite_test.go`: added recovery-cancels-queued-job coverage and updated the recovery API test.
- `internal/httpapi/server.go`: added optional `RecoveryNotifier` wiring while preserving existing `NewGateway` invocations; recovery now uses `Recover` and notifies an existing card.
- `internal/httpapi/server_test.go`: added recovery-card notification coverage and updated the persistence-error fake for the new API.
- `internal/worker/worker.go`: completes jobs only after successful card work, retries failed jobs with bounded linear delay, and treats inactive incidents as harmless cancellations.
- `internal/worker/worker_test.go`: added coverage that a recovered incident is neither overwritten nor card-updated by a worker.

## TDD evidence

RED command:

```sh
go test ./internal/store ./internal/worker ./internal/httpapi -run 'Test(RecoverCancels|WorkerDoesNotOverwrite)' -count=1
```

Observed expected failure: `SQLiteRepository.Recover` and `domain.JobCancelled` did not exist; worker tests also could not call `Recover`; HTTP tests rejected the optional notifier and the replacement recovery contract.

GREEN targeted command:

```sh
go test ./internal/store ./internal/worker ./internal/httpapi -run 'Test(RecoverCancels|WorkerDoesNotOverwrite)' -count=1
```

Result: PASS (`store`, `worker`; `httpapi` had no test matching that exact expression).

Final verification:

```sh
go test -race ./internal/store ./internal/httpapi ./internal/worker -count=1
git diff --check
```

Result: PASS for all three packages; no whitespace errors.

## Design decisions

- `Recover` uses one SQLite transaction to choose an active incident, mark it `RECOVERED`, and cancel its `QUEUED`/`RUNNING` jobs. A missing active incident returns `recovered=false` rather than an error, preserving the ingress `recovery_ignored` response.
- `Complete`, `Fail`, `ScheduleAuditRetry`, and Feishu-message persistence guard on active incident states. They return exported `store.ErrIncidentInactive` if recovery won; the worker treats that result as a harmless cancellation.
- A failed attempt returns the claimed job to `QUEUED` after a bounded linear delay (5 seconds per attempt, capped at 30 seconds). The third claimed failure atomically marks the incident and job `FAILED`.
- `NewGateway` accepts optional dependencies so all existing `(token, captureDir)` and `(token, captureDir, repo)` calls remain valid; a supplied recovery notifier receives exactly one recovered-card update when a message ID exists.

## Concerns

None identified by the specified race-enabled package verification.

## Corrective fix (a3eab86bb99af980085bb28ef2e9199bf4d16764)

RED command:

```sh
go test ./internal/store ./internal/httpapi ./internal/worker -run 'TestClaimNextReclaimsExpiredLeaseButNotRecoveredCancelledJob|TestCloudMonitorRetriesRecoveryCardForDuplicateCallback|TestWorkerRetriesClaimedJobWhenPersistenceFails|TestWorkerCardFenceLetsRecoveryWin|TestWorkerCreatesRecoveredCardWhenRecoveryWinsBeforeMessageIDPersistence' -count=1
```

Observed expected failures: expired `RUNNING` jobs were not reclaimable; duplicate recovery returned `recovery_ignored`; persistence failures left claimed jobs `RUNNING`; recovery could be followed by Worker card work; and the create-card race did not post a recovered card.

GREEN and final verification:

```sh
go test -race ./internal/store ./internal/httpapi ./internal/worker -count=1
go test ./... -count=1
go vet ./...
git diff --check
```

All commands passed. The fix adds a five-minute job lease, an active-incident SQLite card fence, duplicate recovery-card retries, recovered create-card persistence/replay, and retry handling for Worker persistence errors. Terminal transitions cancel unfinished jobs.

Concern: SQLite card fencing deliberately holds the write transaction during bounded notifier I/O, as required by this MVP; prolonged notifier latency serializes repository writes until the caller context expires.

## Final ordering and terminal-transition correction

RED evidence:

```sh
go test ./internal/httpapi -run 'TestCloudMonitorOldRecovery' -count=1
go test ./internal/worker -run 'TestWorker.*TransitionSurvivesCrash' -count=1
```

The generation test failed because replaying recovery A at Unix 20 recovered occurrence B at Unix 30. The crash-window tests failed with the incident left `INVESTIGATING` after the current job had already been completed.

GREEN evidence:

```sh
go test ./internal/store ./internal/httpapi ./internal/worker -run 'Test.*(Recovery|Recover).*Generation|TestCloudMonitor.*OldRecovery|TestWorker.*TransitionSurvivesCrash' -count=1
go test -race ./internal/store ./internal/httpapi ./internal/worker -count=1
go test ./... -count=1
go vet ./...
git diff --check
```

All commands passed. Recovery and duplicate-recovery lookup now correlate by `alert_at <= recovery event time`, selecting `alert_at DESC, id DESC`. Worker terminal completion and pending-audit scheduling each use one repository transaction for the current job, incident state, and any required follow-up job/snapshot.

Commit: `9fa1e7a6278c22b307b7bd88b20c6402e2847c78`

Concerns: the existing SQLite card-fencing trade-off remains unchanged; terminal-transition transactions introduce no new notifier I/O or cross-process coordination.

## Final lease-ownership and Gateway-constructor correction

RED evidence:

```sh
go test ./internal/store ./internal/httpapi -run 'Test(ClaimFence|NewGatewayRemains)' -count=1
```

Observed expected failures: the composite terminal APIs accepted only a job ID, `ErrJobLeaseLost` did not exist, and `NewGateway` accepted `...any` without the explicit notifier constructor.

GREEN evidence:

```sh
go test ./internal/store ./internal/httpapi ./internal/worker -run 'Test.*(Lease|Claim|Transition|Gateway)' -count=1
```

Result: PASS for all three packages. The stale-claim tests reclaim A's expired lease as B at Unix 401, then verify A cannot complete, queue a pending-audit follow-up, retry, or fail B's live claim. The worker crash tests now intercept the composite APIs used by `Worker`, proving terminal state and follow-up work commit together.

Final verification:

```sh
go test -race ./internal/store ./internal/httpapi ./internal/worker -count=1
go test ./... -count=1
go vet ./...
git diff --check
```

All commands passed.

Commit: `356a85173c2164be32f42a954927539f307128de`

Concerns: `NewGateway` retains its requested variadic strong-typed repository form and uses the first supplied repository; notifier wiring is intentionally only available through `NewGatewayWithNotifier`.

## Deterministic card-fence regression test

The worker regression now enables a documented, inert-by-default SQLite test seam. The active-card update holds its test fence for the callback duration; `Recover` signals only after its non-blocking fence acquisition fails, then waits on that same fence. The test therefore releases the blocked Worker only after recovery is demonstrably contending, with no sleep, retry, or scheduler-yield synchronization.

Repeated-test evidence:

```sh
go test ./internal/worker -run TestWorkerCardFenceLetsRecoveryWin -count=100
```

Result: PASS (100 iterations).

Final verification:

```sh
go test -race ./internal/store ./internal/httpapi ./internal/worker -count=1
go test ./... -count=1
go vet ./...
git diff --check
```

All commands passed.

Commit: `9ba95f5`

Concerns: the test-only `sync.Mutex` fence is nil unless explicitly enabled; production SQLite transaction and card-update behavior are unchanged.

## Recovery after RCA terminal result

RED evidence:

```sh
go test ./internal/store ./internal/httpapi ./internal/worker -run 'Test.*(Recover|Recovery|CardFence)' -count=1
```

Observed expected failures: `Recover` returned `recovered=false` for `COMPLETED`
and `FAILED` incidents; the CloudMonitor recovery handler left a completed
incident unchanged; the delayed old-recovery generation tests returned
`recovery_ignored`; and the card history did not end in `RECOVERED` after the
worker completed.

GREEN evidence:

```sh
go test ./internal/store ./internal/httpapi ./internal/worker -run 'Test.*(Recover|Recovery|CardFence)' -count=1
go test ./internal/worker -run TestWorkerCardFenceLetsRecoveryWin -count=100
```

Result: PASS. The 100-run card-fence regression completed stably without the
test-only repository synchronization hook.

Final verification:

```sh
go test -race ./internal/store ./internal/httpapi ./internal/worker -count=1
go test ./... -count=1
go vet ./...
git diff --check
```

All commands passed. `Recover` now selects the latest eligible incident
generation in an active, `COMPLETED`, or `FAILED` state; it cancels only
queued/running jobs, so completed and failed jobs remain terminal.

Commit: `bf7c6487fa8b74d4cd1694052b83f136800c3aed`

Concerns: none identified by the specified verification. The report follows
the fix commit so it can record its exact hash.

## Recovery generation idempotency final correction

RED evidence:

```sh
go test ./internal/store ./internal/httpapi -run 'Test.*Recovery.*(Generation|Duplicate|Old)' -count=1
```

Observed expected failure: replaying recovery@40 selected older terminal A
after newer B was already `RECOVERED`; the second recovery-card update targeted
A rather than B.

GREEN and final verification:

```sh
go test ./internal/store ./internal/httpapi -run 'Test.*Recovery.*(Generation|Duplicate|Old)' -count=1
go test -race ./internal/store ./internal/httpapi ./internal/worker -count=1
go test ./... -count=1
go vet ./...
git diff --check
```

All commands passed. `Recover` now selects the latest eligible generation
including `RECOVERED`; a latest recovered generation is returned without a
state transition, so the duplicate callback retries B's card update and never
falls back to A. The old recovery@20 regression now claims the job generated by
B's OCCURRED callback directly, with no extra hand-enqueued job.

Commit: `855e661`

Concerns: none identified by the specified verification.
