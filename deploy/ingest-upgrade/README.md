# CloudMonitor ingest upgrade

This additive upgrade enables the normal, customer-hosted endpoint
`POST /v1/inbound/cloudmonitor`. It parses the observed CloudMonitor 2.0 direct
Webhook contract, creates an idempotent incident job for `OCCURRED`, and marks
the matching active incident recovered for `RECOVERED`.

It does **not** start the RCA worker or invoke Claude Code. Before exposing the
normal endpoint, put `FEISHU_APP_ID` and `FEISHU_APP_SECRET` in
`/etc/sre-rca/sre.env` and add the following to `/etc/sre-rca/sre.yaml` so
`RECOVERED` can update an existing card:

```yaml
feishu:
  app_id: ${FEISHU_APP_ID}
  app_secret: ${FEISHU_APP_SECRET}
  chat_id: ${FEISHU_CHAT_ID} # required when the Worker is enabled later
```

Do not change the CloudMonitor target URL until the local endpoint has been
verified.

## Upgrade the binary

1. Back up the installed binary:

   ```bash
   cp -a /opt/sre-rca/bin/sre-gateway /opt/sre-rca/bin/sre-gateway.before-ingest
   ```

2. Install the `bin/sre-gateway` from this bundle with mode `0755`, then:

   ```bash
   systemctl restart sre-gateway
   systemctl is-active sre-gateway
   curl --fail --silent --show-error http://127.0.0.1:8080/healthz
   ```

The existing `/etc/sre-rca/sre.yaml` remains valid. Keep
`callback_capture_dir` enabled during this staged rollout.

## Expose the normal callback path

Add this exact location block to the existing TLS server block for the gateway
domain. Keep the existing `/capture` block until normal ingestion is verified.

```nginx
location = /v1/inbound/cloudmonitor {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    client_max_body_size 256k;
}
```

Run `nginx -t` before reloading Nginx. The health check remains:

```text
https://<gateway-domain>/healthz
```

## Verification and cutover

Use the already-redacted trigger fixture first, then the recovery fixture, as
authenticated POST requests to the normal endpoint. This creates and closes a
test incident without re-generating an alert. Only after both requests return
`202` should the CloudMonitor custom Webhook URL change from:

```text
/v1/inbound/cloudmonitor/capture?token=<token>
```

to:

```text
/v1/inbound/cloudmonitor?token=<token>
```

Rollback is local and immediate: restore the backup binary, restart
`sre-gateway`, and point the CloudMonitor webhook back to `/capture`.
