import hashlib,json,os,subprocess
from pathlib import Path
CONTROL=Path(__file__).resolve().parent
ROOT=CONTROL.parents[1]
def git(*args,cwd=ROOT):return subprocess.check_output(['git',*args],cwd=cwd,text=True).strip()
def main():
 assert os.environ['GITHUB_REPOSITORY']=='xue-ding-e/Magpie-Next'
 assert os.environ['GITHUB_EVENT_NAME']=='push'
 assert os.environ['GITHUB_REF']=='refs/heads/validation/auth-gui-pilot-20261102'
 manifest=json.loads((CONTROL/'control-hashes.json').read_text())
 expected=set(manifest)|{'.github/gui-pilot/control-hashes.json','.github/workflows/gui-pilot.yml'}
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
 inventory=json.loads((CONTROL/'inventory.json').read_text())
 assert inventory['product']==src['source'] and inventory['batch_id']=='pilot-001'
 assert len(inventory['selected'])==8 and len(set(inventory['selected']))==8
 expected_files={item['path']:item for item in inventory['files']}
 actual_files={str(p.relative_to(source)) for p in (source/'internal/gui/tests').glob('*.test.cjs')}
 assert len(expected_files)==827 and set(expected_files)==actual_files
 for file,item in expected_files.items():assert hashlib.sha256((source/file).read_bytes()).hexdigest()==item['sha256'],file
 assert all(expected_files[file]['previous_state']=='not-loaded' for file in inventory['selected'])
 assert json.loads((CONTROL/'package.json').read_text())['dependencies']=={'playwright':'1.62.1'}
 print('Exact independent product and CI-only single-parent controls verified; Phase1 remains failed.')
if __name__=='__main__':main()
