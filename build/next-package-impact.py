"""Only verified regular GUI-test modifications may omit a redundant package.
All unknown/invalid diff evidence fails the impact job, blocking packaging.
Validation is a separate job and is never path-filtered by this script.
"""
import os,re,subprocess
SHA=re.compile(r'[0-9a-f]{40}\Z')
TEST=re.compile(rb'internal/gui/tests/[A-Za-z0-9_-]+\.test\.cjs\Z')
CI={b'.github/workflows/next-windows-package.yml',b'build/next-package-impact.py',b'build/next-package-impact_test.py'}
def product_path(path):
 # Only identified inputs consumed by the existing native package build.
 return path in {b'main.go',b'go.mod',b'go.sum',b'LICENSE'} or (path.startswith(b'internal/') and path.endswith(b'.go') and not path.endswith(b'_test.go')) or path.startswith(b'internal/gui/assets/') or path.startswith(b'build/windows/')
def classify(raw):
 if not raw:return False,False,'unchanged-tree'
 fields=raw.split(b'\0');assert fields[-1]==b'' and len(fields)%2==1,'Malformed raw diff'
 ci_changed=False;product_changed=False
 for i in range(0,len(fields)-1,2):
  meta,path=fields[i],fields[i+1]
  match=re.fullmatch(rb':([0-7]{6}) ([0-7]{6}) ([0-9a-f]{40}) ([0-9a-f]{40}) ([MADT])',meta)
  assert match and path and not any(x in path for x in (b'\n',b'\r',b'..')) and not path.startswith(b'/'),'Unsupported diff evidence'
  before,after,old,new,status=match.groups()
  regular=(status==b'M' and before==after==b'100644')
  added=(status==b'A' and before==b'000000' and after==b'100644' and old==b'0'*40)
  deleted=(status==b'D' and before==b'100644' and after==b'000000' and new==b'0'*40)
  assert regular or added or deleted,'Unclassified mode/type transition'
  if path in CI:
   assert regular or added,'Deleting classification controls needs separate review'
   ci_changed=True
  elif TEST.fullmatch(path):
   assert regular,'Adding/deleting/renaming tests needs separate review'
  elif product_path(path):
   product_changed=True
  else:raise AssertionError('Unclassified changed path; no package/publication decision')
 if product_changed:return True,True,'identified-product-input'
 if ci_changed:return True,False,'reviewed-ci-controls-only'
 return False,False,'regular-gui-test-only'
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
