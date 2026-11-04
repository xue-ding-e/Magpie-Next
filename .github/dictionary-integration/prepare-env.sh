#!/usr/bin/env bash
set -euo pipefail
# Use a task-owned runner temp directory OUTSIDE any repository. A Go
# TempDir inside the checkout changes existing work-directory git detection.
if [ "${1:-}" = cleanup ]; then
  python3 - <<'PY_CLEAN'
import json, os, shutil, stat
from pathlib import Path
record = json.loads(os.environ['AUTH_TMP_RECORD'])
p = Path(record['path'])
root = Path(os.environ['RUNNER_TEMP']).resolve(strict=True)
assert p.parent == root and p.name.startswith('ma.') and not p.is_symlink()
s = p.lstat()
assert stat.S_ISDIR(s.st_mode) and [s.st_dev, s.st_ino] == record['identity']
shutil.rmtree(p)
PY_CLEAN
  exit
fi
R="$GITHUB_WORKSPACE/.validation-runtime"
T=$(python3 - <<'PY_TEMP'
import json, os, tempfile
from pathlib import Path
root = Path(os.environ['RUNNER_TEMP']).resolve(strict=True)
assert not any((p / '.git').exists() for p in (root, *root.parents)), 'Runner temp must not be inside a repository'
assert len(os.fsencode(str(root / 'ma.XXXXXXXX') + '/org.chromium.Chromium.XXXXXX/SingletonSocket')) < 108, 'Temporary socket path too long'
p = Path(tempfile.mkdtemp(prefix='ma.', dir=root))
s = p.stat()
with open(os.environ['GITHUB_ENV'], 'a') as f:
    f.write('AUTH_TMP_RECORD=' + json.dumps({'path':str(p), 'identity':[s.st_dev,s.st_ino]}) + '\n')
print(p)
PY_TEMP
)
mkdir -p "$R"/{home,npm-cache,browsers,reports}
test ! -L "$T"
mkdir -p -m 700 "$T"
: > "$R/npmrc"
: > "$R/global-npmrc"
# Retain Go caches before changing HOME, as required by the repository.
if [ "${1:-}" = go ]; then
  { echo "GOPATH=$(go env GOPATH)"; echo "GOMODCACHE=$(go env GOMODCACHE)"; echo "GOCACHE=$(go env GOCACHE)"; } >> "$GITHUB_ENV"
fi
{
 echo "HOME=$R/home"
 echo "USERPROFILE=$R/home"
 echo "XDG_CONFIG_HOME=$R/home/.config"
 echo "XDG_CACHE_HOME=$R/home/.cache"
 echo "XDG_DATA_HOME=$R/home/.local/share"
 echo "XDG_STATE_HOME=$R/home/.local/state"
 echo "APPDATA=$R/home/AppData/Roaming"
 echo "LOCALAPPDATA=$R/home/AppData/Local"
 echo "TMPDIR=$T"
 echo "NPM_CONFIG_USERCONFIG=$R/npmrc"
 echo "NPM_CONFIG_GLOBALCONFIG=$R/global-npmrc"
 echo "NPM_CONFIG_CACHE=$R/npm-cache"
 echo 'NPM_CONFIG_REGISTRY=https://registry.npmjs.org/'
 echo "PLAYWRIGHT_BROWSERS_PATH=$R/browsers"
 echo 'PLAYWRIGHT_DOWNLOAD_CONNECTION_TIMEOUT=30000'
 echo "NODE_PATH=$GITHUB_WORKSPACE/.github/dictionary-integration/node_modules"
 echo 'GOTOOLCHAIN=local'
 echo 'GOFLAGS=-mod=readonly'
 echo 'GOWORK=off'
 echo 'GOMAXPROCS=2'
} >> "$GITHUB_ENV"
