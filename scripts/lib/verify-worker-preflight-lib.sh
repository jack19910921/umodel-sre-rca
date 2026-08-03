#!/usr/bin/env bash

require_fixed_executable() {
  local executable="$1"
  local mode

  if [[ ! -x "${executable}" ]]; then
    echo "required executable is missing or not executable: ${executable}" >&2
    return 1
  fi

  if ! mode="$(python3 - "${executable}" <<'PY'
import os
import stat
import sys

print(format(stat.S_IMODE(os.stat(sys.argv[1]).st_mode), "o"))
PY
  )"; then
    echo "cannot inspect executable mode: ${executable}" >&2
    return 1
  fi

  if [[ "${mode}" != "755" ]]; then
    echo "required executable target must have fixed mode 0755: ${executable}" >&2
    return 1
  fi
}
