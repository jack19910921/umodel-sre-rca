# CloudMonitor callback capture bundle

This bundle is the first, deliberately narrow deployment step. It starts only
the Gateway health endpoint and the redacted callback-capture endpoint. It does
not enable normal incident ingestion, Feishu, cloud credentials, Logtail, or
Claude Code execution.

Upload `sre-rca-capture-linux-amd64.tar.gz` to `/root` through Workbench, then
run the commands provided in the deployment guide. The installer refuses to
overwrite an existing `sre-rca-capture` Nginx site and tests Nginx before reload.
