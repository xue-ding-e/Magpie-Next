import hashlib,json,os,subprocess,sys
from pathlib import Path
ROOT=Path(__file__).resolve().parents[2];HERE=Path(__file__).resolve().parent
SOURCE='ff997c492a908293dc0de152ee20f83152b88aa9'
EXPECTED={'plan':'success','optional_gui':'skipped','package_sentinel':'success','legacy_publish_sentinel':'skipped','fixed_publish_sentinel':'success','failed_package_sentinel':'failure','blocked_publish_sentinel':'skipped','dryrun_publish_sentinel':'skipped','offmain_publish_sentinel':'skipped'}
def gate():
 def git(*a):return subprocess.check_output(['git',*a],cwd=ROOT,text=True).strip()
 assert os.environ['GITHUB_REPOSITORY']=='xue-ding-e/Magpie-Next' and os.environ['GITHUB_EVENT_NAME']=='push' and os.environ['GITHUB_REF']=='refs/heads/validation/publish-skip-20261103'
 assert git('rev-parse','HEAD')==os.environ['GITHUB_SHA'] and git('rev-parse','HEAD^')==SOURCE
 rows=json.loads((HERE/'control-hashes.json').read_text());paths=sorted([x['path'] for x in rows]+['.github/publish-skip-proof/control-hashes.json'])
 assert sorted(git('diff','--name-only',SOURCE,'HEAD').splitlines())==paths
 for x in rows:assert hashlib.sha256((ROOT/x['path']).read_bytes()).hexdigest()==x['sha256']
 for p in paths:assert git('ls-tree','HEAD','--',p).split()[0]=='100644'
 # Bind the real candidate publication conditions being modeled. The only
 # context substitution in the workflow is the synthetic source_ref output.
 workflow=(ROOT/'.github/workflows/next-windows-package.yml').read_text()
 assert "if: needs.impact.outputs.publish == 'true' && github.repository == 'xue-ding-e/Magpie-Next' && github.ref == 'refs/heads/main'" in workflow
 print('Exact candidate and diagnostic controls verified; all jobs are no-op admission sentinels, never publishers')
def audit(needs):
 assert {k:v['result'] for k,v in needs.items()}==EXPECTED
 print('Observed default-status skip; explicit-status admission works after expected GUI skip. Failed package, dry-run and non-main publication are blocked. No release or package operation executed.')
def selftest():
 good={k:{'result':v} for k,v in EXPECTED.items()};audit(good);n=1
 for key in EXPECTED:
  wrong=json.loads(json.dumps(good));wrong[key]['result']='cancelled'
  try:audit(wrong)
  except AssertionError:n+=1
  else:raise AssertionError('Bad dependency result accepted')
 print(n,'synthetic state controls passed; not a scheduler proof')
if __name__=='__main__':
 if sys.argv[1]=='gate':gate()
 elif sys.argv[1]=='audit':audit(json.loads(os.environ['NEEDS_JSON']))
 elif sys.argv[1]=='selftest':selftest()
 else:raise AssertionError('unknown mode')
