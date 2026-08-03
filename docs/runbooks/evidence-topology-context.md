# Static topology evidence binding

`endpoint_runs_on_topology` is a reviewed `context` evidence binding. It is
read-only and resolves the runtime `runs_on` edge from the custom
`sre.service_endpoint` entity to its ECS neighbor through CMS
`GetEntityStoreData`.

The binding accepts only `endpoint_id` from a reviewed incident binding. It
derives the deterministic SRE EntityStore ID locally, uses a fixed
`graph-call getNeighborNodes('sequence_out', 1, ...)` query, and filters the
result to `relationType = 'runs_on'`. It accepts no URL, cloud action, shell
command, or credential from an incident or model prompt.

Install the binding YAML only after reviewing a backup and a diff. It does not
start, restart, or enable `sre-worker`. The binding becomes active only when a
future, explicitly enabled worker invokes `sre-evidence incident context` for
a reviewed incident binding.

The ECS RAM role needs the existing read-only CMS EntityStore permission. No
new write permission is required.
