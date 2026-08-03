#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${script_dir}/lib/verify-worker-preflight-lib.sh"

work_dir="$(mktemp -d)"
trap 'rm -rf "${work_dir}"' EXIT

target="${work_dir}/claude-real"
link="${work_dir}/claude"
printf '#!/bin/sh\nexit 0\n' >"${target}"
chmod 0755 "${target}"
ln -s "${target}" "${link}"

if ! require_fixed_executable "${link}"; then
  echo "expected executable symlink to a 0755 target to pass" >&2
  exit 1
fi

echo "verify-worker-preflight symlink mode test passed"
