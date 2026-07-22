# Customer-owned ECS RCA MVP runbook

This runbook deploys one endpoint into the current Alibaba Cloud account. It
does not move raw telemetry out of that account and does not modify ARA.

## Fixed demo identifiers

Use these values consistently; replace only the angle-bracketed values:

| Field | Value |
|---|---|
| service_id | `blog` |
| endpoint_id | `blog-health` |
| endpoint URL | `http://<ECS_PUBLIC_IP>/health` |
| ECS instance | `i-bp17eb4oiqsmy10fq9mu` |
| region_id | `cn-hangzhou` |
| Logstore | `sre-nginx-demo` |

## 1. UModel acceptance checklist

1. Domain `sre` appears in UModel Explorer.
2. `sre.service_endpoint` contains `account_id`, `region_id`, `instance_id`, `host`, and `probe_task_id`.
3. `blog-health` reaches `acs.ecs.instance` through the `hosted_by` relationship.
4. Nginx access and error LogSets have StorageLinks to the customer SLS Logstore `sre-nginx-demo`.
5. Each Nginx LogSet has a DataLink from `sre.service_endpoint` using `endpoint_id`.
6. `acs.ecs.securitygroup` has a DataLink to `sre.event.change` using `account_id`, `region_id`, and `resource_id`.

The files in `umodel/` are reviewed UModel resource manifests. Before applying,
substitute `${SLS_PROJECT}` and `${REGION_ID}` in `umodel/sre-storage.yaml`.
Use the workspace's UModel write mechanism (umctl or the CMS 2024-03-30 SDK) and
then run the UModel Explorer `inspect` method. Do not treat a YAML parse alone
as proof that the tenant accepted the model.

Create exactly one `sre.service` entity and one `sre.service_endpoint` entity:

```text
sre.service:
  service_id=blog, name=blog, environment=demo, criticality=low

sre.service_endpoint:
  service_id=blog, endpoint_id=blog-health,
  url=http://<ECS_PUBLIC_IP>/health, protocol=http, expected_status=200,
  host=<ECS_PUBLIC_IP>, probe_task_id=<HTTP_PROBE_TASK_ID>,
  account_id=<ALIBABA_CLOUD_ACCOUNT_ID>, region_id=cn-hangzhou,
  instance_id=i-bp17eb4oiqsmy10fq9mu
```

For periodic entity sync, write entity data to the workspace EntityStore with
stable `__domain__`, `__entity_type__`, and MD5-derived `__entity_id__`; use
`Update`, not `Create`, for the daily full sync.

## 2. Nginx log collection

1. In SLS, create project `<SLS_PROJECT>` in Hangzhou and Logstore `sre-nginx-demo`.
2. Install Logtail on this ECS using the SLS console. Confirm it can reach the
   project's intranet SLS endpoint.
3. Run:

   ```bash
   sudo SRE_ENDPOINT_ID=blog-health ./scripts/install-logtail.sh
   ```

4. In SLS, create a **user-defined ID** machine group with the identifier printed
   by the script (`sre-ecs-i-bp17eb4oiqsmy10fq9mu`). Apply it to the Logtail
   configuration.
5. Create separate Nginx text-log configurations for `/var/log/nginx/access.log`
   and `/var/log/nginx/error.log`. Use the Nginx parser for access logs and a
   plain-text/error parser for error logs.
6. In the collector processing stage, add constant fields to both streams:

   ```text
   endpoint_id = blog-health
   instance_id = i-bp17eb4oiqsmy10fq9mu
   ```

   Preserve the parsed `host` field if the Nginx format contains it. The evidence
   provider uses `endpoint_id` for UModel association and `instance_id` for its
   fixed SLS query; it never receives arbitrary SLS SQL from the Agent.
7. Generate a fresh record and verify it in SLS. Logtail only collects newly
   appended data after a configuration is applied:

   ```bash
   curl -fsS http://127.0.0.1/health
   ```

8. Verify the record has `endpoint_id=blog-health` and the exact ECS
   `instance_id`. Only then enable the two StorageLinks in UModel.

## 3. Baseline before any injected fault

```bash
curl -fsS http://127.0.0.1/health
curl -fsS http://<ECS_PUBLIC_IP>/health
```

The ActionTrail query baseline must be available before the exercise. Delayed
audit delivery is a valid state: the eventual RCA must say
`AWAITING_AUDIT_EVENT`, requeue after 120 seconds, and never invent a change.

## 4. Safety boundary for the later live exercise

Only remove the selected security group's inbound TCP/80 rule. Do not remove
TCP/22 and do not stop the ECS, Nginx, or the gateway. Restore the same TCP/80
rule immediately after the alert test. The gateway is not enabled until its
callback fixture, ECS RAM role, Feishu app credentials, and read-only provider
checks have all passed.

## 5. Deploy the local gateway only after its prerequisites pass

1. Build the two binaries in CI or on the ECS, then install them under
   `/opt/sre-rca/bin/`. Create the unprivileged `sre-rca` service user and the
   writable directory `/var/lib/sre-rca`.
2. Copy `configs/sre.example.yaml` to `/etc/sre-rca/sre.yaml`; replace every
   placeholder. Put app secrets only in `/etc/sre-rca/sre.env` with mode `0600`.
   The `sre-rca` user must own the non-interactive Claude Code authentication;
   do not rely on a personal interactive login session.
3. Install `deploy/sre-gateway.service`, then run `systemctl daemon-reload` and
   `systemctl enable --now sre-gateway`.
4. Configure TLS and the health plus CloudMonitor callback paths with
   `deploy/nginx/sre-rca.conf`, verify `nginx -t`, then reload Nginx.
5. Run `scripts/verify-mvp.sh`. A success here verifies only prerequisites; it
   is not a substitute for the CloudMonitor callback fixture or live exercise.

## 6. Capture the real CloudMonitor callback contract

This is a one-time, non-production intake mode. Keep `callback_capture_dir`
enabled, deploy the gateway, and configure the test notification action to:

```text
https://<gateway.example.com>/v1/inbound/cloudmonitor/capture?token=<callback_token>
```

Trigger one alert and one recovery. Each request is authenticated with a
constant-time token comparison; only valid JSON is accepted. The gateway
retains every JSON key but replaces values under sensitive keys such as
`secret`, `token`, `access_key`, `authorization`, and `cookie`, then writes the
fixture with mode `0600` under `/var/lib/sre-rca/callback-fixtures/`.

Do not commit customer fixture files: they can contain account and resource
identifiers even after secret redaction. Instead, derive synthetic tests from
the observed key structure. The normal `/v1/inbound/cloudmonitor` route accepts
only `ALERT` events with `OCCURRED` or `RECOVERED` status, creates one
idempotent incident job for an occurrence, and closes the matching active
incident on recovery. Keep the capture route enabled during staged rollout and
disable it only after observing normal ingestion in the customer environment.
