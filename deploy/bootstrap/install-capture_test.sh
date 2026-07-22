#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
installer="${script_dir}/install-capture.sh"

if ! grep -q '^wait_for_gateway()' "$installer"; then
  echo "expected install-capture.sh to define wait_for_gateway" >&2
  exit 1
fi

if ! grep -q 'wait_for_gateway 127.0.0.1:8080/healthz' "$installer"; then
  echo "expected install-capture.sh to wait for the systemd-started gateway" >&2
  exit 1
fi
