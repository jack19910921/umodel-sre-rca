# CloudMonitor callback capture bundle

This bundle is the first, deliberately narrow deployment step. It starts only
the Gateway health endpoint and the redacted callback-capture endpoint. It does
not enable the independent RCA Worker, Feishu, cloud credentials, Logtail, or
Claude Code execution.

The customer CloudMonitor normal-ingress endpoint may be enabled in a later
ingest upgrade, but it never starts the Worker. Build and preflight the Worker
separately; only install and enable `deploy/worker/sre-worker.service` after
reviewed incident bindings, RAM/SLS/ActionTrail, Feishu, and the preflight all
succeed.

The source ingress template keeps `worker.enabled: false`. For worker
preflight, copy it to the intended `/etc/sre-rca/sre.yaml` runtime config and
set `worker.enabled: true`, then fill every reviewed binding and credential
placeholder. That edit alone is non-mutating: it neither enables nor starts a
systemd service. Do not run `systemctl enable` or `systemctl start` until the
preflight succeeds.

Upload `sre-rca-capture-linux-amd64.tar.gz` to `/root` through Workbench, then
run the commands provided in the deployment guide. The installer refuses to
overwrite an existing `sre-rca-capture` Nginx site and tests Nginx before reload.
