#!/usr/bin/env bash
set -euo pipefail
R="$GITHUB_WORKSPACE/.validation-runtime"
T="$GITHUB_WORKSPACE/.t"
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
 echo "NODE_PATH=$GITHUB_WORKSPACE/.github/auth-full-validation/node_modules"
 echo 'GOTOOLCHAIN=local'
 echo 'GOFLAGS=-mod=readonly'
 echo 'GOWORK=off'
 echo 'GOMAXPROCS=2'
} >> "$GITHUB_ENV"
