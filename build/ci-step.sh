#!/usr/bin/env bash
# GitHub's custom run shell: retain stdout and stderr without hiding a failed
# command behind tee. Step names are runner-generated, not branch/user input.
set -euo pipefail
if [ "$#" -ne 1 ] || [ ! -f "$1" ]; then
  echo 'ci-step: expected one script file' >&2
  exit 2
fi
case "${GITHUB_ACTION:?}" in
  ''|*[!a-zA-Z0-9_-]*) echo 'ci-step: invalid step name' >&2; exit 2 ;;
esac
logs="${RUNNER_TEMP:?}/magpie-ci-logs"
mkdir -p "$logs"
bash --noprofile --norc -e -o pipefail "$1" 2>&1 | tee "$logs/$GITHUB_ACTION.log"
