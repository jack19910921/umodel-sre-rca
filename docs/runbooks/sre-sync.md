# Custom SRE domain synchronizer

`sre-sync` is an administrator-run provider for the custom UModel `sre`
domain. It creates/updates a small service-endpoint projection and, when
explicitly enabled, writes the corresponding directed relation to an existing
native ECS entity.

It is not part of the alert callback path and it does not start the RCA worker.
It uses the ECS RAM role. Its read-only inspection mode calls CMS
`GetEntityStoreData`. When explicitly run with `--apply`, it writes the
endpoint *instance data* and the configured relation instance to CloudMonitor
2.0 EntityStore through the SLS Log Protocol. It does not mutate ECS,
ActionTrail, or the native `acs` domain.

CloudMonitor 2.0 requires custom entity instances to be written to the SLS
logstore named `${workspace}__entity` in the workspace project. Each log record
contains the endpoint fields plus these required EntityStore fields:

- `__domain__`;
- `__entity_type__`;
- `__entity_id__`;
- `__last_observed_time__` (Unix seconds).

The synchronizer also sends `__method__=Update` and
`__keep_alive_seconds__`, making the operation idempotent for the deterministic
endpoint entity ID.

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
`POST /workspace/{workspace}/entitiesAndRelations` (`GetEntityStoreData`).
The required `from`, `to`, and `query` values are sent in its JSON request
body, with the UModel query:

```text
.entity with(domain='sre', type='sre.service_endpoint') | limit 0, 10
```

The response is the raw EntityStore result for the custom endpoint model. A
successful response with `data: []` is normal before the first entity write; it
confirms that the workspace and the ECS RAM role can reach the read API. Inspect
`responseStatus.statusItem`: `UModelNotExist` is the authoritative signal that
the model has not been registered in the runtime EntityStore. Grant
`cms:GetEntityStoreData` only if the command returns an authorization error.

## Relation instance guardrail

An EntitySetLink in UModel Explorer is only a **schema definition**. Relation
instances must be written separately through the SLS Log Protocol to
`${workspace}__topo`. The relation is written only when `relation.type` is
non-empty, and it must exactly match the persisted EntitySetLink type.

For the verified SRE endpoint to ECS link, use:

```yaml
relation:
  type: runs_on
  destination_domain: acs
  destination_entity_type: acs.ecs.instance
```

The emitted topology log has the official required fields:

- `__src_domain__`, `__src_entity_type__`, `__src_entity_id__`;
- `__dest_domain__`, `__dest_entity_type__`, `__dest_entity_id__`;
- `__relation_type__`.

It also sends `__method__=Update`, `__last_observed_time__`, and
`__keep_alive_seconds__`. Endpoint and relation records are sent separately to
`${workspace}__entity` and `${workspace}__topo`. Keep the default
`relation.type: ""` until the schema, scoped permission, and dry-run output
have all been confirmed.

## Apply

Only after reviewing dry-run output, write the plan:

```bash
/opt/sre-rca/bin/sre-sync \
  --config /etc/sre-rca/sre-sync.yaml \
  --apply
```

The command is idempotent: the SRE endpoint entity ID is deterministically
derived from `endpoint_id`. Re-running it updates the same custom endpoint and
the same directed relation.

## Read-only relation verification

After `--apply`, run:

```bash
/opt/sre-rca/bin/sre-sync \
  --config /etc/sre-rca/sre-sync.yaml \
  --inspect-relation
```

This uses the supported CMS `GetEntityStoreData` API (not a restricted graph
API). It runs the documented `.topo | graph-call getNeighborNodes` outbound
traversal, then filters the returned `relationType` and destination entity ID.
A non-empty `data` result proves the configured runtime relation exists. An
empty result means no matching runtime edge was returned for that time window.

## Expire an accidentally-written legacy relation

Topology relationships support `Update` and `Expire`; do not use an invented
`Delete` method. To retire one old relationship type for the single configured
endpoint, first preview the exact one-record topology payload:

```bash
/opt/sre-rca/bin/sre-sync \
  --config /etc/sre-rca/sre-sync.yaml \
  --expire-relation-type related_to
```

The preview must contain exactly one element with the six source/destination
identity fields, `__relation_type__: related_to`, `__method__: Expire`, and no
`__domain__` entity field. It intentionally omits `__keep_alive_seconds__`.

Only after that review, write the expiry record:

```bash
/opt/sre-rca/bin/sre-sync \
  --config /etc/sre-rca/sre-sync.yaml \
  --expire-relation-type related_to \
  --apply
```

This uses the existing, resource-scoped `log:PostLogStoreLogs` permission on
`${workspace}__topo`; no new RAM permission is required. After CloudMonitor
consumes the expiry event, run `--inspect-relation` with `relation.type: runs_on`
to verify the intended canonical edge.

## Required RAM permission

The existing endpoint writer needs the following SLS write permission:

```json
{
  "Version": "1",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["log:PostLogStoreLogs"],
      "Resource": [
        "acs:log:*:*:project/<workspace>/logstore/<workspace>__entity"
      ]
    },
    {
      "Effect": "Allow",
      "Action": ["cms:GetEntityStoreData"],
      "Resource": ["*"]
    }
  ]
}
```

Replace both `<workspace>` placeholders with the actual workspace name. The
CloudMonitor callback receiver and evidence CLI keep their own separate,
read-only access boundaries.

To enable the relation writer, add **only** this additional resource to the
existing `log:PostLogStoreLogs` statement:

```json
"acs:log:*:*:project/default-cms-1876202723954089-cn-hangzhou/logstore/default-cms-1876202723954089-cn-hangzhou__topo"
```

`--inspect-relation` uses the existing read-only `cms:GetEntityStoreData`
permission. Its resource may be scoped to:

```text
acs:cms:cn-hangzhou:1876202723954089:workspace/default-cms-1876202723954089-cn-hangzhou/entitystore
```
