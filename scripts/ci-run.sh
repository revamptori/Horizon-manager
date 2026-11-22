#!/usr/bin/env bash
# Preserve the command's status and expose bounded diagnostics through GitHub's annotations API.
# This wrapper is for SDK/build/test commands only; never pass secrets or signing credentials.
set -uo pipefail
log="$(mktemp)"
"$@" 2>&1 | tee "$log"
status=${PIPESTATUS[0]}
if ((status != 0)); then
  details="$(tail -n 45 "$log")"
  details="${details//'%'/'%25'}"
  details="${details//$'\r'/'%0D'}"
  details="${details//$'\n'/'%0A'}"
  printf '::error title=Build diagnostics::%s\n' "$details"
fi
rm -f "$log"
exit "$status"
