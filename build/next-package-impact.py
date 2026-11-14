"""Reviewed GUI tests and documentation do not change package bytes.
Go regressions and reviewed plugin inputs require packaging, not publication.
All unknown/invalid diff evidence fails the impact job, blocking packaging.
Validation is a separate job and is never path-filtered by this script.
"""
import os,re,subprocess
SHA=re.compile(r'[0-9a-f]{40}\Z')
TEST=re.compile(rb'internal/gui/tests/[A-Za-z0-9_-]+\.test\.cjs\Z')
GO_TEST=re.compile(rb'(?:(?:internal|plugins|third_party/jsonschema)/(?:[A-Za-z0-9_-]+/)*)?[A-Za-z0-9_]+_test\.go\Z')
GO_SOURCE=re.compile(rb'(?:internal|plugins|third_party/jsonschema)/(?:[A-Za-z0-9_-]+/)*[A-Za-z0-9_]+\.go\Z')
TESTDATA=re.compile(rb'(?:internal|plugins|third_party/jsonschema)/(?:[A-Za-z0-9_-]+/)*testdata/.+\Z')
DOC=re.compile(rb'docs/subsystems/[A-Za-z0-9_-]+\.md\Z')
DOCS={b'README.md',b'README.zh-CN.md',b'internal/gui/tests/README.md',
      b'AGENTS.md',b'CLAUDE.md',b'LESSONS.md',b'.gitignore',
      b'docs/code-standards.md',b'docs/contributors.md',b'docs/integrating.md',
      b'docs/reference.md',b'docs/session-relocation.md',b'docs/upstream-tracking.md',
      b'third_party/jsonschema/LICENSE',b'third_party/jsonschema/MAGPIE.md',b'third_party/jsonschema/README.md',
      b'.github/ISSUE_TEMPLATE/bug_report.yml',b'.github/ISSUE_TEMPLATE/config.yml',b'.github/ISSUE_TEMPLATE/feature_request.yml'}
CI={b'.github/workflows/next-windows-package.yml',b'build/next-package-impact.py',b'build/next-package-impact_test.py',
    b'.github/workflows/test-ui.yml',b'build/ui-test-shard.mjs',b'build/ui-test-shard.test.mjs',
    b'.github/ui-preview/package.json',
    b'.github/workflows/test.yml',b'.github/workflows/docker.yml',b'.github/workflows/docker-daily.yml',
    b'.github/workflows/docker-stable.yml',b'.github/workflows/notes-mirror.yml',
    b'.github/workflows/privacy-filter.yml',b'.github/workflows/release.yml',b'.github/workflows/ui-preview.yml',
    b'build/ci-step.sh',b'build/ci-step_test.sh',b'build/ci_home.py',b'build/ci_home_test.py',b'build/race-shards.sh',
    b'Makefile',b'Dockerfile',b'build/assetpack/main.go',b'build/icon/gen.go',
    b'build/darwin/AppIcon.icon/icon.json',b'build/darwin/Assets.car',b'build/darwin/Info.plist',
    b'internal/pii/guard/coreml_gen.py',b'native/privacy-filter/build.sh',
    b'native/privacy-filter/patches/weights-once.patch',b'native/privacy-filter/patches/weights-paged.patch',
    b'build/darwin/footprint-overlay.sh',b'build/darwin/footprint_test.go'}
# These reviewed scripts retain executable Git modes. The pinned upstream
# upgrade adds only the exact footprint overlay; other additions, symlinks
# and mode/type transitions still need separate review.
EXECUTABLE_CI={b'build/race-shards.sh',b'native/privacy-filter/build.sh',b'build/darwin/footprint-overlay.sh'}
EXECUTABLE_ADDITIONS={b'build/darwin/footprint-overlay.sh'}
FIXTURES={b'internal/gui/tests/reader.cjs',b'internal/gui/tests/strings.cjs',
          b'internal/gui/tests/fixtures/browser-activity.cjs',
          b'internal/gui/tests/fixtures/day-card-icon.svg',
          b'internal/gui/tests/fixtures/caller-keys.cjs',b'internal/gui/tests/fixtures/opendesign-favicon.svg',
          b'internal/gui/tests/fixtures/plugins-github-31.json'}
# Explicit build inputs and files embedded by the linked Go source include
# modules split out of app.js and the masking dictionaries. The former
# icon.png remains listed so its upstream replacement can be classified.
PRODUCT_FILES={b'main.go',b'go.mod',b'go.sum',b'LICENSE',b'THIRD_PARTY_NOTICES',b'third_party/jsonschema/go.mod',
               b'internal/agent/pi_extension.ts',b'internal/catalog/bundled/api.json.gz',b'internal/claudetui/tap.js',
               b'internal/codexcat/codex_prompt.md',b'internal/codexcat/codex_instructions.json',
               b'internal/desktopiso/magpie.png',b'internal/gateway/keypage.html',b'internal/gui/pagebridge.js',
               b'internal/gui/tray.png',b'internal/gui/icon.png',b'internal/gui/icon-windows.ico',b'internal/gui/icon-1024.png',b'internal/gui/icon-256.png',
               b'internal/library/market_pi.json',b'internal/library/market_servers.json',b'internal/library/market_skills.json',
               b'internal/pii/guard/coreml.json',b'internal/pii/guard/coreml.mlmodel',
               b'internal/pii/guard/kernel_amd64.s',b'internal/pii/guard/kernel_arm64.s',
               b'internal/plugin/pi.js',b'internal/plugin/market.json',
               b'internal/provider/magpie.svg',b'internal/redact/bip39english.txt',b'internal/redact/zhdict.txt',
               b'internal/update/attest_root.json',
               b'internal/update/launcher-amd64.com',b'internal/update/launcher-arm64.com'}
PRODUCT_TREES=(b'internal/gui/assets/',b'internal/omarchy/widget/',b'internal/browserext/ext/',
               b'third_party/jsonschema/metaschemas/',b'build/windows/')
# The site is deployed separately; its known scripts, docs and public assets
# cannot authorize a native release. Unknown files beside them still fail.
SITE_FILES={b'site/.gitignore',b'site/contributors-exclude.txt',b'site/credits-cli.mjs',b'site/credits.mjs',
            b'site/files.js',b'site/i18n.js',b'site/llms-full.mjs',b'site/partners.js',b'site/pluginstats.js',
            b'site/r2stub.mjs',b'site/sponsors.js',b'site/worker.js',b'site/wrangler.jsonc',
            b'site/public/BingSiteAuth.xml',b'site/public/docs.css',b'site/public/ff9d582867a8d6064bba06c3149d23ec.txt',
            b'site/public/import.html',b'site/public/index.html',b'site/public/install.sh',
            b'site/public/llms-full.txt',b'site/public/llms.txt',b'site/public/robots.txt',b'site/public/sitemap.xml'}
SITE_TEST=re.compile(rb'site/[A-Za-z0-9_-]+\.test\.mjs\Z')
SITE_ASSET=re.compile(rb'site/public/(?:docs/[A-Za-z0-9_/-]+\.(?:md|html)|(?:icons|img)/[A-Za-z0-9_./-]+\.(?:svg|png|webp|jpg|jpeg|gif|mp4))\Z')
# Reviewed embedded host and its synthetic regression require a new package,
# but do not extend automatic publication authorization.
PLUGIN_INPUTS={b'internal/plugin/host.js',b'internal/plugin/testdata/oauth_callback.cjs'}
def product_path(path):
 # Only identified inputs consumed by the existing native package build.
 return (path in PRODUCT_FILES or path.startswith(PRODUCT_TREES) or
         bool(re.fullmatch(rb'internal/gui/trayflap/[A-Za-z0-9_-]+\.png',path)) or
         (bool(GO_SOURCE.fullmatch(path)) and not path.endswith(b'_test.go') and not TESTDATA.fullmatch(path)))
def classify(raw):
 if not raw:return False,False,'unchanged-tree'
 fields=raw.split(b'\0');assert fields[-1]==b'' and len(fields)%2==1,'Malformed raw diff'
 ci_changed=False;product_changed=False;go_tests_changed=False
 for i in range(0,len(fields)-1,2):
  meta,path=fields[i],fields[i+1]
  match=re.fullmatch(rb':([0-7]{6}) ([0-7]{6}) ([0-9a-f]{40}) ([0-9a-f]{40}) ([MADT])',meta)
  assert match and path and not any(x in path for x in (b'\n',b'\r',b'..')) and not path.startswith(b'/'),'Unsupported diff evidence'
  before,after,old,new,status=match.groups()
  regular=(status==b'M' and before==after==b'100644')
  added=(status==b'A' and before==b'000000' and after==b'100644' and old==b'0'*40)
  deleted=(status==b'D' and before==b'100644' and after==b'000000' and new==b'0'*40)
  executable=(path in EXECUTABLE_CI and status==b'M' and before==after==b'100755' or
              path in EXECUTABLE_ADDITIONS and status==b'A' and before==b'000000' and after==b'100755' and old==b'0'*40)
  assert regular or added or deleted or executable,'Unclassified mode/type transition'
  if path in CI:
   assert regular or added or executable,'Deleting classification controls needs separate review'
   ci_changed=True
  elif TEST.fullmatch(path) or DOC.fullmatch(path) or path in DOCS:
   # Mode/type checks above still reject executable references and symlinks.
   # A test or reference added beside product code must not block its
   # release; alone it never authorizes publishing another executable.
   pass
  # Directory embeds may contain Go-test-looking resource names. Product
  # inputs must win over the test-only classification, apart from the exact
  # plugin inputs reviewed above as nonpublishing changes.
  elif path in PLUGIN_INPUTS:
   go_tests_changed=True
  elif product_path(path):
   product_changed=True
  elif GO_TEST.fullmatch(path) or TESTDATA.fullmatch(path) or path in FIXTURES:
   go_tests_changed=True
  elif path in SITE_FILES or SITE_TEST.fullmatch(path) or SITE_ASSET.fullmatch(path):
   pass
  else:raise AssertionError('Unclassified changed path; no package/publication decision: '+repr(path))
 if product_changed:return True,True,'identified-product-input'
 if ci_changed:return True,False,'reviewed-ci-controls-only'
 if go_tests_changed:return True,False,'reviewed-nonpublishing-inputs-only'
 return False,False,'tests-or-documentation-only'
def git(*args):return subprocess.check_output(['git',*args],stderr=subprocess.PIPE)
def main():
 assert os.environ['GITHUB_REPOSITORY']=='xue-ding-e/Magpie-Next','Wrong repository'
 head=os.environ['GITHUB_SHA'];assert SHA.fullmatch(head)
 assert git('rev-parse','HEAD').decode().strip()==head,'Wrong checkout'
 assert not git('status','--porcelain','--untracked-files=no'),'Dirty checkout'
 if os.environ['GITHUB_EVENT_NAME']=='push' and os.environ['GITHUB_REF']=='refs/heads/main':
  before=os.environ['BEFORE_SHA'];assert SHA.fullmatch(before) and before!='0'*40,'Missing previous source'
  for sha in (before,head):git('cat-file','-e',sha+'^{commit}')
  package,publish,reason=classify(git('diff','--raw','--no-abbrev','-z','--no-renames',before,head,'--'))
 else:
  manual=os.environ.get('MANUAL_PUBLISH','false');assert manual in ('true','false'),'Invalid explicit publication input'
  package=True;publish=os.environ['GITHUB_EVENT_NAME']=='workflow_dispatch' and os.environ['GITHUB_REF']=='refs/heads/main' and manual=='true';reason='explicit-manual-publish' if publish else 'build-without-publication'
 with open(os.environ['GITHUB_OUTPUT'],'a',encoding='utf8') as f:f.write('package='+str(package).lower()+'\npublish='+str(publish).lower()+'\n')
 print('Packaging decision:',str(package).lower(),'publication:',str(publish).lower(),reason,'; validation remains required')
if __name__=='__main__':main()
