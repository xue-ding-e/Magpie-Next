"""Static source/lock gate and exact-byte evidence. Never executes toolchains."""
import hashlib
import json
import pathlib
import sys
import tomllib

PIN = '970111f6cb62cc02fe36a82ea7d71b37a2889c97'
LOCK = 'd661437f2dae702b0beb404df06ef60219845c5ce909138427ac4a3947b8cb2c'
# Reviewed official lock sources, not permission for arbitrary dependency hosts.
GIT = {
 'dzbarsky/rules_rust': 'b56cbaa8465e74127f1ea216f813cd377295ad81',
 'helix-editor/nucleo.git': '4253de9faabb4e5c6d81d946a5e35a90f87347ee',
 'hyperium/h3': 'e07e69412876f7e26f026bd75a48b2704d8c8283',
 'microsoft/mxc': 'b21120168d70befe12356411423266bfef168446',
 'modelcontextprotocol/rust-sdk': '98a54789b80110ad210495c4ad845c89e1980471',
 'openai-oss-forks/crossterm': 'ed1cdab335221515706178d68495bba2aed1924f',
 'openai-oss-forks/tokio-tungstenite': '0e5b2d73aa18dd9f0a50ee9ff199d5aef7594186',
 'openai-oss-forks/tungstenite-rs': '4fffad30fe373adbdcffab9545e9e9bf4f2fc19f',
}

def dependencies(root):
    raw = (root / 'codex-rs/Cargo.lock').read_bytes()
    assert hashlib.sha256(raw).hexdigest() == LOCK
    packages = tomllib.loads(raw.decode())['package']
    assert len(packages) == 1476
    allowed = {f'git+https://github.com/{repo}?rev={rev}#{rev}' for repo, rev in GIT.items()}
    for package in packages:
        source = package.get('source')
        if source is None:
            continue
        if source == 'registry+https://github.com/rust-lang/crates.io-index':
            assert len(package['checksum']) == 64
        else:
            assert source in allowed, source
    by_name = {}
    for package in packages:
        by_name.setdefault(package['name'], []).append(package)
    seen = set()
    todo = [p for p in packages if p['name'] == 'codex-thread-store']
    assert len(todo) == 1
    while todo:
        package = todo.pop()
        key = (package['name'], package['version'], package.get('source'))
        if key in seen:
            continue
        seen.add(key)
        for dependency in package.get('dependencies', []):
            parts = dependency.split()
            matches = by_name[parts[0]]
            if len(parts) > 1:
                matches = [p for p in matches if p['version'] == parts[1]]
            if len(parts) > 2:
                matches = [p for p in matches if p.get('source') == parts[2].strip('()')]
            assert len(matches) == 1, dependency
            todo.extend(matches)
    assert len(seen) == 982
    assert 'Apache License' in (root / 'LICENSE').read_text()
    assert 'OpenAI Codex' in (root / 'NOTICE').read_text()
    print(json.dumps({'pin': PIN, 'lock_sha256': LOCK, 'workspace_lock_packages': len(packages),
                      'static_reachable_upper_bound_all_platforms_features': len(seen), 'git_sources': GIT, 'boundary': 'locked no-run prepares only selected thread-store integration target closure; no whole-workspace fetch'}))

if __name__ == '__main__':
    assert len(sys.argv)==2
    dependencies(pathlib.Path(sys.argv[1]))
