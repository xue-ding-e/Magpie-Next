#!/usr/bin/env bash
# Executed only inside the controller's network namespace after bounded fetch.
set -euo pipefail
: "${MAGPIE_SOURCE:?}" "${CODEX_SOURCE:?}" "${EVIDENCE:?}" "${MAGPIE_SYNTHETIC_OUTPUT:?}"
command -v cargo
command -v rustc
command -v go
export CARGO_NET_OFFLINE=true GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local
# Preparation already downloaded and compiled only this integration target.
cd "$CODEX_SOURCE/codex-rs"
printf 'official-parser-and-index-observation-test\n' > "$EVIDENCE/stage"
timeout 5m cargo test --locked --offline -p codex-rollout --test magpie_synthetic_contract -- --exact typed_fixture_and_real_parser_contract --nocapture --test-threads=1 2>&1 | tee "$EVIDENCE/rust-test.log"
grep -F 'REAL_PARSER_PASS variants=2 revisions=4 phases=2 schema_negative=5 legacy_fallback=1' "$EVIDENCE/rust-test.log"
grep -F 'REAL_INDEX_PASS phases=4 revisions=4 observation_only=duplicate,attached,stale,missing_leaf' "$EVIDENCE/rust-test.log"
grep -F '1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out' "$EVIDENCE/rust-test.log"
cd "$MAGPIE_SOURCE"
python3 build/native-schema/audit.py seal "$MAGPIE_SYNTHETIC_OUTPUT" > "$EVIDENCE/fixture-hashes.json"
# Golden hashes are from successful run 41160069956, not regenerated expectations.
cmp build/native-schema/reference-hashes.json "$EVIDENCE/fixture-hashes.json"
cp -a "$MAGPIE_SYNTHETIC_OUTPUT" "$EVIDENCE/fixture"
printf 'go-shared-bytes-test\n' > "$EVIDENCE/stage"
test -z "$(gofmt -l internal/sessions/codex_native_schema_fixture_test.go)"
timeout 10m go test -mod=readonly -p=2 -tags nogui,magpie_native_schema_validation -count=1 -timeout=8m -json -run '^TestNativeSchemaSharedFixture$' ./internal/sessions | tee "$EVIDENCE/go-tests.jsonl"
python3 - <<'PY'
import json, os
from pathlib import Path
root=Path(os.environ['EVIDENCE'])
events=[json.loads(line) for line in (root/'go-tests.jsonl').read_text().splitlines()]
assert not any(e.get('Action') in ('fail','skip') for e in events)
passed={e['Test'] for e in events if e.get('Action')=='pass' and 'Test' in e}
assert passed=={'TestNativeSchemaSharedFixture','TestNativeSchemaSharedFixture/shrink','TestNativeSchemaSharedFixture/grow'},passed
assert any(e.get('Action')=='pass' and e.get('Package')=='github.com/yetone/magpie/internal/sessions' and 'Test' not in e for e in events)
PY
# Re-seal and compare after Go: both validators consumed the SAME exact bytes.
python3 build/native-schema/audit.py seal "$MAGPIE_SYNTHETIC_OUTPUT" > "$EVIDENCE/fixture-hashes-after.json"
cmp "$EVIDENCE/fixture-hashes.json" "$EVIDENCE/fixture-hashes-after.json"
git diff --exit-code
git diff --cached --exit-code
git -C "$CODEX_SOURCE" diff --exit-code
printf 'passed-parser-index-observation-and-go-admission-not-sqlite-materialization-or-migration\n' > "$EVIDENCE/stage"
