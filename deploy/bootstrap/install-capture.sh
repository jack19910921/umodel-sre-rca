#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  sudo ./install-capture.sh \
    --domain sre-rca.example.com \
    --certificate /absolute/path/fullchain.cer \
    --certificate-key /absolute/path/private.key
EOF
}

wait_for_gateway() {
  local address="$1"
  local attempt

  for attempt in $(seq 1 20); do
    if curl --fail --silent --show-error --max-time 1 "http://${address}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done

  curl --fail --silent --show-error --max-time 1 "http://${address}" >/dev/null
}

if [[ "${EUID}" -ne 0 ]]; then
  echo "run as root" >&2
  exit 2
fi

domain=""
certificate=""
certificate_key=""
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    --domain) domain="${2:-}"; shift 2 ;;
    --certificate) certificate="${2:-}"; shift 2 ;;
    --certificate-key) certificate_key="${2:-}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [[ ! "$domain" =~ ^[A-Za-z0-9.-]+$ ]] || [[ "$domain" != *.* ]]; then
  echo "--domain must be a DNS name" >&2
  exit 2
fi
for path in "$certificate" "$certificate_key"; do
  if [[ "$path" != /* ]] || [[ ! -r "$path" ]]; then
    echo "certificate paths must be readable absolute paths" >&2
    exit 2
  fi
done

bundle_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
gateway="$bundle_dir/bin/sre-gateway"
config="$bundle_dir/config/sre.yaml"
if [[ ! -x "$gateway" ]] || [[ ! -f "$config" ]]; then
  echo "bundle is incomplete: expected bin/sre-gateway and config/sre.yaml" >&2
  exit 1
fi
if [[ -e /etc/nginx/sites-available/sre-rca-capture || -e /etc/nginx/sites-enabled/sre-rca-capture ]]; then
  echo "refusing to overwrite existing sre-rca-capture Nginx site" >&2
  exit 1
fi
if ss -ltn | grep -qE '127\.0\.0\.1:8080\b'; then
  echo "127.0.0.1:8080 is already in use" >&2
  exit 1
fi

if ! id -u sre-rca >/dev/null 2>&1; then
  useradd --system --home-dir /var/lib/sre-rca --create-home --shell /usr/sbin/nologin sre-rca
fi
install -d -o sre-rca -g sre-rca -m 0750 /opt/sre-rca/bin /opt/sre-rca/runtime /var/lib/sre-rca
install -d -o root -g sre-rca -m 0750 /etc/sre-rca
install -m 0755 "$gateway" /opt/sre-rca/bin/sre-gateway
install -m 0640 "$config" /etc/sre-rca/sre.yaml
chown root:sre-rca /etc/sre-rca/sre.yaml

umask 077
callback_token="$(openssl rand -hex 32)"
printf 'SRE_RCA_CALLBACK_TOKEN=%s\n' "$callback_token" > /etc/sre-rca/sre.env
chmod 0600 /etc/sre-rca/sre.env

install -m 0644 "$bundle_dir/systemd/sre-gateway.service" /etc/systemd/system/sre-gateway.service
systemctl daemon-reload
systemctl enable --now sre-gateway
wait_for_gateway 127.0.0.1:8080/healthz

cat > /etc/nginx/sites-available/sre-rca-capture <<EOF
server {
    listen 443 ssl http2;
    server_name ${domain};

    ssl_certificate     ${certificate};
    ssl_certificate_key ${certificate_key};

    location = /healthz {
        proxy_pass http://127.0.0.1:8080/healthz;
        proxy_set_header Host \$host;
    }

    location = /v1/inbound/cloudmonitor/capture {
        access_log off;
        proxy_pass http://127.0.0.1:8080/v1/inbound/cloudmonitor/capture;
        proxy_set_header Host \$host;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        client_max_body_size 256k;
    }

    location / {
        return 404;
    }
}
EOF
ln -s /etc/nginx/sites-available/sre-rca-capture /etc/nginx/sites-enabled/sre-rca-capture
if ! nginx -t; then
  rm /etc/nginx/sites-enabled/sre-rca-capture
  echo "Nginx configuration test failed; existing Nginx was not reloaded" >&2
  exit 1
fi
systemctl reload nginx
curl --fail --silent --show-error --max-time 10 --resolve "${domain}:443:127.0.0.1" "https://${domain}/healthz" >/dev/null

cat <<EOF
Capture gateway is ready.

Configure the temporary CloudMonitor callback URL exactly as:
https://${domain}/v1/inbound/cloudmonitor/capture?token=${callback_token}

The token is stored only in /etc/sre-rca/sre.env (mode 0600). Trigger one alert
and one recovery, then list the redacted fixtures with:
sudo ls -l /var/lib/sre-rca/callback-fixtures/
EOF
