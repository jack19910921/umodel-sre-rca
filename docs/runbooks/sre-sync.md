# Custom SRE domain synchronizer

`sre-sync` is an administrator-run provider for the custom UModel `sre`
domain. It creates/updates a small service-endpoint projection and may create
the corresponding relation to an existing native ECS entity.

It is not part of the alert callback path and it does not start the RCA worker.
It uses the ECS RAM role and calls CMS `UpsertUmodelData` only when explicitly
run with `--apply`; it does not write to ECS, SLS, ActionTrail, or the native
`acs` domain. Its read-only inspection mode calls CMS `GetEntityStoreData`.

## Build

On a trusted build machine:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o sre-sync ./cmd/sre-sync
```

Copy the resulting binary and a copy of
`configs/sre-sync.example.yaml` to the customer-owned ECS host. Keep the YAML
outside the worker runtime, for example `/etc/sre-rca/sre-sync.yaml`, with
permissions `0640` owned by `root:sre-rca`.

## Safe first run

The default is dry-run. It validates the YAML and prints the exact UModel
elements that would be sent, without creating a cloud client or writing data.

```bash
/opt/sre-rca/bin/sre-sync \
  --config /etc/sre-rca/sre-sync.yaml
```

Confirm in the output that every entity has:

- `__domain__` equal to `sre`;
- `__entity_type__` equal to `sre.service_endpoint`;
- the intended `endpoint_id` and native `ecs_entity_id`.

## Read-only UModel inspection

When an apply request fails, inspect the service-side EntityStore before
changing the entity payload. This command does not write data and is mutually
exclusive with `--apply`:

```bash
/opt/sre-rca/bin/sre-sync \
  --config /etc/sre-rca/sre-sync.yaml \
  --inspect-schema
```

The command uses the supported CloudMonitor 2.0 API
`POST /workspace/{workspace}/entitiesAndRelations` (`GetEntityStoreData`) with
the UModel query:

```text
.entity with(domain='sre', type='sre.service_endpoint') | limit 0, 10
```

The response is the raw EntityStore result for the custom endpoint model. It
can confirm whether the saved workspace has registered
`sre.service_endpoint` at runtime and whether the ECS RAM role can query it.
Inspect `responseStatus.statusItem`: `UModelNotExist` is the authoritative
signal that the model has not been registered in the runtime EntityStore.
Grant `cms:GetEntityStoreData` only if the command returns an authorization
error.

## Relation guardrail

An EntitySetLink in UModel Explorer is only a **schema definition**. A
relation data record should be written only after the persisted link type is
known exactly. Keep `relation.type` empty until then; this creates endpoint
data only.

When confirmed, set `relation.type` to the exact relation type already saved
in the UModel schema. The synchronizer accepts only a relation from
`sre.service_endpoint` to `acs.ecs.instance`, so it cannot accidentally create
a relationship to another native resource type.

## Apply

Only after reviewing dry-run output, write the plan:

```bash
/opt/sre-rca/bin/sre-sync \
  --config /etc/sre-rca/sre-sync.yaml \
  --apply
```

The command is idempotent: the SRE endpoint entity ID is deterministically
derived from `endpoint_id`. Re-running it updates the same custom endpoint.

## Required RAM permission

Grant the `sre-rca` ECS RAM role only the CMS action needed for this command,
scoped to the target workspace where possible:

```text
cms:UpsertUmodelData
```

The optional inspection mode additionally needs:

```text
cms:GetEntityStoreData
```

The CloudMonitor callback receiver and evidence CLI keep their own separate,
read-only access boundaries.
