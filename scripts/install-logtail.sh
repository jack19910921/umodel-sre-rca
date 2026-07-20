#!/usr/bin/env bash
set -euo pipefail

# Prepares the ECS for an SLS custom-identifier machine group. Collection rules
# themselves are created in SLS: Logtail receives and manages them centrally,
# so this script intentionally never writes user_log_config.json.
#
# Usage:
#   sudo SRE_ENDPOINT_ID=blog-health ./scripts/install-logtail.sh

if [[ "${EUID}" -ne 0 ]]; then
  echo "run as root (for example: sudo SRE_ENDPOINT_ID=blog-health $0)" >&2
  exit 2
fi

: "${SRE_ENDPOINT_ID:?SRE_ENDPOINT_ID is required (for example, blog-health)}"

metadata_url="http://100.100.100.200/latest/meta-data/instance-id"
instance_id="$(curl --fail --silent --show-error --connect-timeout 2 --max-time 5 "$metadata_url")"
if [[ ! "$instance_id" =~ ^i-[A-Za-z0-9]+$ ]]; then
  echo "unexpected ECS instance ID from metadata service" >&2
  exit 1
fi

if ! command -v nginx >/dev/null; then
  echo "nginx is not installed" >&2
  exit 1
fi
nginx -t
curl --fail --silent --show-error --max-time 5 http://127.0.0.1/health >/dev/null

if [[ ! -x /etc/init.d/ilogtaild ]] && ! systemctl list-unit-files 2>/dev/null | grep -q '^ilogtaild'; then
  cat >&2 <<'EOF'
Logtail is not installed. Install it from the SLS console first, then rerun.
Do not create /usr/local/ilogtail/user_log_config.json locally: SLS delivers
that file after you apply a Logtail collection configuration to a machine group.
EOF
  exit 1
fi

install -d -m 0755 /etc/ilogtail
printf 'sre-ecs-%s\n' "$instance_id" > /etc/ilogtail/user_defined_id
chmod 0644 /etc/ilogtail/user_defined_id

if [[ -x /etc/init.d/ilogtaild ]]; then
  /etc/init.d/ilogtaild restart
else
  systemctl restart ilogtaild
fi

cat <<EOF
Prepared Logtail custom identifier: sre-ecs-$instance_id

In SLS, create a user-defined machine group containing that exact identifier,
then create two Nginx text-log configurations for:
  /var/log/nginx/access.log
  /var/log/nginx/error.log

Use the collection pipeline to add these constant fields to each record:
  endpoint_id=$SRE_ENDPOINT_ID
  instance_id=$instance_id

The model uses these fields as the DataLink keys. See docs/runbooks/mvp-demo.md
for parsing, storage-link, and verification steps.
EOF
