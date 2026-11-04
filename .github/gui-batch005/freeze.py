"""Freeze local controls, including pending receipts; does not publish or execute."""
import hashlib
import json
import re
from pathlib import Path

CONTROL = Path(__file__).resolve().parent
ROOT = CONTROL.parents[1]


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    hashes = {str(p.relative_to(ROOT)): digest(p) for p in sorted(CONTROL.rglob('*'))
              if p.is_file() and p.name not in ('control-hashes.json', 'prior-ledger.json')
              and '__pycache__' not in p.parts and 'node_modules' not in p.parts}
    manifest = CONTROL / 'control-hashes.json'
    manifest.write_text(json.dumps(hashes, indent=2) + '\n')
    workflow = ROOT / '.github/workflows/gui-batch005.yml'
    text = workflow.read_text()
    for marker, path in [('BOOTSTRAP_HASH', CONTROL / 'bootstrap.py'), ('GATE_HASH', CONTROL / 'gate.py'), ('CONTROL_HASH', manifest)]:
        relative = str(path.relative_to(ROOT))
        text, count = re.subn(r"(?:" + marker + r"|[0-9a-f]{64})(  " + re.escape(relative) + r"' \| sha256sum -c -)", digest(path) + r'\1', text)
        assert count == 1
    workflow.write_text(text)
    all_hashes = dict(hashes)
    all_hashes[str(manifest.relative_to(ROOT))] = digest(manifest)
    all_hashes[str(workflow.relative_to(ROOT))] = digest(workflow)
    frozen = {'state': 'local batch005 draft; exact receipts frozen; requires parent review before activation',
              'product': 'ff997c492a908293dc0de152ee20f83152b88aa9',
              'files_to_stage': all_hashes,
              'derived_outputs_not_to_stage': {'.github/gui-batch005/prior-ledger.json': {'size': 835286, 'sha256': '09f5b0c0b824b8fac2b8fcaa21bc022172955d456bf622bc1bbe3c1aee3a666c', 'git_blob': 'c7eb5b5acd5436c2f1e10958c4503582c66d4c1f'}},
              'control_manifest_sha256': digest(manifest),
              'workflow_sha256': digest(workflow), 'no_external_write_or_browser_execution': True}
    (ROOT / 'FROZEN-LOCAL.json').write_text(json.dumps(frozen, indent=2) + '\n')
    (ROOT / 'STAGING-PATHS.txt').write_text('\n'.join(sorted(all_hashes)) + '\n')
    print(json.dumps({k: v for k, v in frozen.items() if k != 'files_to_stage'}, indent=2))


if __name__ == '__main__':
    main()
