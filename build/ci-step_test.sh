#!/usr/bin/env bash
# Synthetic checks only: the wrapper must preserve failed commands and both
# output streams, including a failure inside a pipeline or before a later line.
set -euo pipefail
wrapper="$(cd "$(dirname "$0")" && pwd)/ci-step.sh"
root=$(mktemp -d "${RUNNER_TEMP:?}/magpie-ci-step-test.XXXXXXXX")
cleanup() {
  rm -f "$root/input.sh" "$root/magpie-ci-logs/pass.log" \
    "$root/magpie-ci-logs/fail.log" "$root/magpie-ci-logs/pipeline.log" \
    "$root/magpie-ci-logs/early.log"
  rmdir "$root/magpie-ci-logs" "$root"
}
trap cleanup EXIT
check() {
  local name=$1 expected=$2 code=0
  RUNNER_TEMP="$root" GITHUB_ACTION="$name" bash "$wrapper" "$root/input.sh" || code=$?
  if [ "$code" -ne "$expected" ]; then
    echo "FAIL $name: got exit $code, want $expected" >&2
    exit 1
  fi
}
printf 'printf "synthetic stdout\\n"; printf "synthetic stderr\\n" >&2\n' > "$root/input.sh"
check pass 0
grep -Fx 'synthetic stdout' "$root/magpie-ci-logs/pass.log"
grep -Fx 'synthetic stderr' "$root/magpie-ci-logs/pass.log"
printf 'echo synthetic failure >&2; exit 17\n' > "$root/input.sh"
check fail 17
grep -Fx 'synthetic failure' "$root/magpie-ci-logs/fail.log"
printf 'false | true\n' > "$root/input.sh"
check pipeline 1
printf 'false\necho should-not-run\n' > "$root/input.sh"
check early 1
if grep -q 'should-not-run' "$root/magpie-ci-logs/early.log"; then
  echo 'FAIL early: executed after a failed command' >&2
  exit 1
fi
echo 'PASS: CI logging preserves command and pipeline failures, early exit, stdout and stderr'
