import hashlib,json,os,subprocess
from pathlib import Path
CONTROL=Path(__file__).resolve().parent
ROOT=CONTROL.parents[1]
def git(*args,cwd=ROOT):return subprocess.check_output(['git',*args],cwd=cwd,text=True).strip()
def main():
 assert os.environ['GITHUB_REPOSITORY']=='xue-ding-e/Magpie-Next'
 assert os.environ['GITHUB_EVENT_NAME']=='push'
 assert os.environ['GITHUB_REF']=='refs/heads/validation/auth-width-versions-20261102'
 manifest=json.loads((CONTROL/'control-hashes.json').read_text())
 expected=set(manifest)|{'.github/width-version-pair/control-hashes.json','.github/workflows/width-version-pair.yml'}
 src=json.loads((CONTROL/'source.json').read_text())
 assert git('rev-parse','HEAD')==os.environ['GITHUB_SHA']
 assert git('rev-parse','HEAD^')==src['source']
 assert len(git('rev-list','--parents','-n','1','HEAD').split())==2
 assert set(git('diff','--name-only','HEAD^','HEAD').splitlines())==expected
 for file,digest in manifest.items():assert hashlib.sha256((ROOT/file).read_bytes()).hexdigest()==digest,file
 source=ROOT/'source'
 assert git('rev-parse','HEAD',cwd=source)==src['source']
 assert git('rev-parse','HEAD^',cwd=source)==src['parent']
 assert git('rev-parse','HEAD^{tree}',cwd=source)==src['tree']
 assert not git('status','--porcelain','--untracked-files=no',cwd=source)
 for file,digest in src['files'].items():assert hashlib.sha256((source/file).read_bytes()).hexdigest()==digest,file
 for label,version in [('v1611','1.61.1'),('v1621','1.62.1')]:
  package=CONTROL/'versions'/label
  assert json.loads((package/'package.json').read_text())['dependencies']=={'playwright':version}
  lock=json.loads((package/'package-lock.json').read_text())
  for name in ('playwright','playwright-core'):
   info=lock['packages']['node_modules/'+name]
   assert info['version']==version and info['resolved'].startswith('https://registry.npmjs.org/'+name+'/') and info['integrity'].startswith('sha512-')
 print('Exact independent product and CI-only single-parent controls verified; Phase1 remains failed.')
if __name__=='__main__':main()
