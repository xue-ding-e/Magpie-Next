#!/usr/bin/env bash
set -euo pipefail
mkdir -p .validation-runtime/reports/go
run() {
  local name=$1 seconds=$2; shift 2
  printf '\nSTART %s %s\n' "$name" "$(date -u +%FT%TZ)"
  timeout "$seconds" "$@" 2>&1 | tee ".validation-runtime/reports/go/$name.log"
  printf 'PASS %s %s\n' "$name" "$(date -u +%FT%TZ)"
}
bad=$(gofmt -l $(git ls-files '*.go'))
printf '%s' "$bad" > .validation-runtime/reports/go/gofmt.log
if [ -n "$bad" ]; then printf '%s\n' "$bad"; exit 1; fi
run vet-native 1200 go vet -tags gtk3 ./...
run vet-linux-nogui 1200 env GOOS=linux go vet -tags nogui ./...
run vet-windows 1200 env GOOS=windows CGO_ENABLED=0 go vet ./...
run build-native 1200 go build -tags gtk3 -o "$RUNNER_TEMP/auth-native" .
run build-linux-nogui 1200 env GOOS=linux go build -tags nogui -o "$RUNNER_TEMP/auth-nogui" .
run build-windows 1200 env GOOS=windows CGO_ENABLED=0 go build -o "$RUNNER_TEMP/auth.exe" .
run test-all-nogui 3600 go test -p 2 -tags nogui -count=1 -timeout 45m ./...

git diff --exit-code
