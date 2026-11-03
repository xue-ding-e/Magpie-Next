import hashlib,json,os,subprocess
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2]
HERE=Path(__file__).resolve().parent
SOURCE='ff997c492a908293dc0de152ee20f83152b88aa9'
def git(path,*args):return subprocess.check_output(['git',*args],cwd=path,stderr=subprocess.PIPE).decode().strip()
assert os.environ['GITHUB_REPOSITORY']=='xue-ding-e/Magpie-Next'
assert os.environ['GITHUB_EVENT_NAME']=='push' and os.environ['GITHUB_REF']=='refs/heads/validation/auth-unix-20261103'
assert os.environ['GITHUB_ACTIONS']=='true' and os.environ['RUNNER_ENVIRONMENT']=='github-hosted' and os.environ['RUNNER_OS'] in ('Linux','macOS')
assert git(ROOT,'rev-parse','HEAD')==os.environ['GITHUB_SHA'] and git(ROOT,'rev-parse','HEAD^')==SOURCE
rows=json.loads((HERE/'control-hashes.json').read_text());paths=sorted([r['path'] for r in rows]+['.github/auth-unix-regression/control-hashes.json'])
assert sorted(git(ROOT,'diff','--name-only',SOURCE,'HEAD').splitlines())==paths
for r in rows:
 f=ROOT/r['path'];assert not f.is_symlink() and hashlib.sha256(f.read_bytes()).hexdigest()==r['sha256']
for path in paths:assert git(ROOT,'ls-tree','HEAD','--',path).split()[0]=='100644'
source=ROOT.parent/'source';assert git(source,'rev-parse','HEAD')==SOURCE and git(source,'rev-parse','HEAD^')=='a27f0957427b4ba86f7c140fa38068922adcc9ea' and not git(source,'status','--porcelain')
for r in json.loads((HERE/'source-hashes.json').read_text()):assert hashlib.sha256((source/r['path']).read_bytes()).hexdigest()==r['sha256']
print('Exact private-export candidate source and isolated Unix regression controls verified')
