#!/usr/bin/env bash
set -euo pipefail

# This script deliberately performs no systemctl enable/start/restart action.
# It proves the customer-hosted worker prerequisites as the service account.

if [[ "${EUID}" -ne 0 ]]; then
  echo "run as root so checks can execute as sre-rca" >&2
  exit 2
fi

incident_id="${SRE_RCA_PREFLIGHT_INCIDENT_ID:-}"
if [[ ! "${incident_id}" =~ ^[A-Za-z0-9._:-]+$ ]]; then
  echo "SRE_RCA_PREFLIGHT_INCIDENT_ID must be a safe incident reference" >&2
  exit 2
fi

worker_bin="/opt/sre-rca/bin/sre-worker"
evidence_bin="/opt/sre-rca/bin/sre-evidence"
claude_bin="/usr/bin/claude"
for executable in "${worker_bin}" "${evidence_bin}" "${claude_bin}"; do
  if [[ ! -x "${executable}" ]] || [[ "$(stat -c '%a' "${executable}")" != "755" ]]; then
    echo "required executable must exist with fixed mode 0755" >&2
    exit 1
  fi
done

if ! id -u sre-rca >/dev/null 2>&1; then
  echo "required service account sre-rca does not exist" >&2
  exit 1
fi

if [[ ! -r /etc/sre-rca/sre.env ]]; then
  echo "worker environment file is not readable" >&2
  exit 1
fi

umask 077
work_dir="$(mktemp -d /var/lib/sre-rca/sre-preflight.XXXXXX)"
trap 'rm -rf "${work_dir}"' EXIT
claude_output="${work_dir}/claude.json"
evidence_output="${work_dir}/evidence.json"
sts_output="${work_dir}/sts.json"

child_path="/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
child_home="/var/lib/sre-rca"

run_as_service_account() {
  runuser -u sre-rca -- env -i "PATH=${child_path}" "HOME=${child_home}" "$@"
}

if ! run_as_service_account /usr/bin/claude -p 'Return exactly {"ok":true}' --output-format json --max-turns 1 --tools "" --no-session-persistence >"${claude_output}" 2>"${work_dir}/claude.stderr"; then
  echo "Claude Code preflight invocation failed" >&2
  exit 1
fi
if ! run_as_service_account /opt/sre-rca/bin/sre-evidence incident context "${incident_id}" >"${evidence_output}" 2>"${work_dir}/evidence.stderr"; then
  echo "fixed evidence CLI preflight invocation failed" >&2
  exit 1
fi
if ! run_as_service_account aliyun sts GetCallerIdentity --profile sre-ecs-role >"${sts_output}" 2>"${work_dir}/sts.stderr"; then
  echo "Alibaba STS preflight invocation failed" >&2
  exit 1
fi

python3 - "${claude_output}" "${evidence_output}" "${sts_output}" <<'PY'
import json
import sys

claude_path, evidence_path, sts_path = sys.argv[1:]

def load(path):
    with open(path, encoding="utf-8") as handle:
        return json.load(handle)

def contains_ok(value):
    if isinstance(value, dict):
        return value.get("ok") is True or any(contains_ok(item) for item in value.values())
    if isinstance(value, list):
        return any(contains_ok(item) for item in value)
    if isinstance(value, str):
        try:
            return contains_ok(json.loads(value))
        except json.JSONDecodeError:
            return False
    return False

claude = load(claude_path)
evidence = load(evidence_path)
sts = load(sts_path)
if not contains_ok(claude):
    raise SystemExit("Claude Code preflight response was not the expected acknowledgement")
if not isinstance(evidence, dict) or not isinstance(evidence.get("evidence"), list):
    raise SystemExit("fixed evidence CLI response was invalid")
if not isinstance(sts, dict) or not sts.get("AccountId"):
    raise SystemExit("Alibaba STS response was invalid")
PY

echo "worker preflight succeeded"
