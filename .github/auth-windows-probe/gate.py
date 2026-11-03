import hashlib,json,os,shutil,subprocess,sys
from pathlib import Path
HERE=Path(__file__).resolve().parent;CONTROL=HERE.parents[1];SOURCE='a27f0957427b4ba86f7c140fa38068922adcc9ea';BRANCH='refs/heads/validation/auth-windows-security-20261103'
def git(root,*args):return subprocess.check_output(['git',*args],cwd=root,stderr=subprocess.PIPE)
def verify():
 assert os.environ['GITHUB_REPOSITORY']=='xue-ding-e/Magpie-Next' and os.environ['GITHUB_EVENT_NAME']=='push' and os.environ['GITHUB_REF']==BRANCH
 assert os.environ['RUNNER_ENVIRONMENT']=='github-hosted' and os.environ['RUNNER_OS']=='Windows' and os.environ['GITHUB_ACTIONS']=='true'
 assert git(CONTROL,'rev-parse','HEAD').decode().strip()==os.environ['GITHUB_SHA']
 assert git(CONTROL,'rev-parse','HEAD^').decode().strip()==SOURCE
 rows=json.loads((HERE/'control-hashes.json').read_text());paths=sorted([r['path'] for r in rows]+['.github/auth-windows-probe/control-hashes.json'])
 assert sorted(git(CONTROL,'diff','--name-only',SOURCE,'HEAD','--').decode().splitlines())==paths
 for r in rows:
  f=CONTROL/r['path'];assert not f.is_symlink() and hashlib.sha256(f.read_bytes()).hexdigest()==r['sha256']
 for p in paths:assert git(CONTROL,'ls-tree','HEAD','--',p).decode().split()[0]=='100644'
 root=Path(sys.argv[2]).resolve();assert git(root,'rev-parse','HEAD').decode().strip()==SOURCE and not git(root,'status','--porcelain')
 for row in json.loads((HERE/'source-hashes.json').read_text()):assert hashlib.sha256((root/row['path']).read_bytes()).hexdigest()==row['sha256']
 return root
if __name__=='__main__':
 assert sys.argv[1]=='prepare';root=verify()
 for n in ['codex_auth_windows_acl_probe_test.go','codex_auth_windows_clipboard_probe_test.go']:
  dest=root/'internal/gui'/n;assert not dest.exists();shutil.copyfile(HERE/'overlay/internal/gui'/n,dest)
 print('Hosted Windows identity/source verified; exactly two synthetic test overlays installed. No product edits or UI acceptance claim.')
