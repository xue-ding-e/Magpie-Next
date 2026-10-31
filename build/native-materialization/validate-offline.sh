#!/usr/bin/env bash
# Called only after successful fail-closed namespace preflight, with env-i.
set -euo pipefail
: "${MAGPIE_SOURCE:?}" "${CODEX_SOURCE:?}" "${EVIDENCE:?}" "${MAGPIE_OWNED_ROOT:?}"
: "${MAGPIE_GOLDEN_INPUT:?}" "${MAGPIE_MATERIALIZATION_OUTPUT:?}"
command -v cargo
command -v rustc
# Only loopback exists in the fresh unshare namespace; no network-enabled fallback.
test "$(ip -o link show | wc -l)" = 1
export CARGO_NET_OFFLINE=true
cd "$MAGPIE_SOURCE"
python3 build/native-materialization/audit-local-projection.py golden "$MAGPIE_GOLDEN_INPUT" > "$EVIDENCE/golden-before.json"
printf 'offline-native-local-projection-test\n' > "$EVIDENCE/stage"
cd "$CODEX_SOURCE/codex-rs"
timeout 5m cargo test --locked --offline -p codex-thread-store --test magpie_local_projection_contract -- --exact native_local_projection_four_revisions --nocapture --test-threads=1 2>&1 | tee "$EVIDENCE/rust-test.log"
grep -F 'REAL_LOCAL_PROJECTION_PASS cases=4 revisions_per_case=4 golden_unchanged=16 raw_mutants_per_case=2 wrong_reference_mutants_per_case=2 local_resume_only=1' "$EVIDENCE/rust-test.log"
grep -F '1 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out' "$EVIDENCE/rust-test.log"
cd "$MAGPIE_SOURCE"
python3 build/native-materialization/audit-local-projection.py golden "$MAGPIE_GOLDEN_INPUT" > "$EVIDENCE/golden-after.json"
cmp "$EVIDENCE/golden-before.json" "$EVIDENCE/golden-after.json"
python3 build/native-materialization/audit-local-projection.py final "$MAGPIE_GOLDEN_INPUT" "$MAGPIE_MATERIALIZATION_OUTPUT" > "$EVIDENCE/materialization-hashes.json"
cp -a "$MAGPIE_MATERIALIZATION_OUTPUT" "$EVIDENCE/event-rich"
python3 build/native-materialization/audit-dependencies.py "$CODEX_SOURCE" > "$EVIDENCE/dependency-audit-after.json"
cmp "$EVIDENCE/dependency-audit.json" "$EVIDENCE/dependency-audit-after.json"
git diff --exit-code
git diff --cached --exit-code
git -C "$CODEX_SOURCE" diff --exit-code
git -C "$CODEX_SOURCE" diff --cached --exit-code
python3 - <<'PY'
import os, subprocess
from pathlib import Path
root=Path(os.environ['CODEX_SOURCE'])
expected={'codex-rs/thread-store/tests/magpie_local_projection_contract.rs',
          'codex-rs/thread-store/tests/magpie_reference_hashes.json'}
actual=set(subprocess.check_output(['git','-C',str(root),'ls-files','--others','--exclude-standard'],text=True).splitlines())
assert actual==expected,(actual,expected)
for name in expected:
    assert (root/name).read_bytes()==(Path(os.environ['MAGPIE_SOURCE'])/'build/native-materialization'/Path(name).name).read_bytes()
PY
printf 'passed-local-synthetic-sqlite-projection-not-provider-resume-or-user-migration\n' > "$EVIDENCE/stage"
