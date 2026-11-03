"""Artifact-only admission proof. Native validation is a sentinel, never rerun here."""
import hashlib,importlib.util,json,os,subprocess,sys
from pathlib import Path
CONTROL=Path(__file__).resolve().parent
SOURCE='ff997c492a908293dc0de152ee20f83152b88aa9'
AUTH='a27f0957427b4ba86f7c140fa38068922adcc9ea'
CI='d2b0e0eaaab4c363d422a719cd7ddf429e7388ff'
MAIN='4123f2978e8c55734258185a724f457d76dffe32'
BRANCH='refs/heads/validation/required-gui-20261103'
PATHS=['internal/gui/tests/codex-auth-export-state.test.cjs','internal/gui/tests/codex-auth-export.test.cjs']
NEG='internal/gui/tests/required-gui-negative.test.cjs'
NEGATIVE="const {test}=require('node:test');const assert=require('node:assert/strict');test('required GUI deliberate negative',()=>assert.fail('REQUIRED_GUI_EXPECTED_ASSERTION'));\n"
def git(root,*args,input=None):return subprocess.check_output(['git',*args],cwd=root,input=input,stderr=subprocess.PIPE)
def module(root,name):
 s=importlib.util.spec_from_file_location(name,root/'build/next-package-impact.py');m=importlib.util.module_from_spec(s);s.loader.exec_module(m);return m
def source(root):
 assert git(root,'rev-parse','HEAD').decode().strip()==SOURCE
 assert git(root,'rev-parse','HEAD^').decode().strip()==AUTH
 assert git(root,'rev-parse','HEAD^^').decode().strip()==CI
 assert not git(root,'status','--porcelain','--untracked-files=no')
 for item in json.loads((CONTROL/'source-hashes.json').read_text()):
  assert hashlib.sha256((root/item['path']).read_bytes()).hexdigest()==item['sha256'],item['path']
def gate():
 root=CONTROL.parents[1]
 assert os.environ['GITHUB_REPOSITORY']=='xue-ding-e/Magpie-Next'
 assert os.environ['GITHUB_EVENT_NAME']=='push' and os.environ['GITHUB_REF']==BRANCH
 head=os.environ['GITHUB_SHA'];assert git(root,'rev-parse','HEAD').decode().strip()==head
 assert git(root,'rev-parse','HEAD^').decode().strip()=='6de54bd1d621612f33eb72e5725d5c821abfcd21'
 assert git(root,'rev-parse','HEAD^^').decode().strip()==SOURCE
 assert not git(root,'status','--porcelain','--untracked-files=no')
 rows=json.loads((CONTROL/'control-hashes.json').read_text())
 expected=sorted([x['path'] for x in rows]+['.github/changed-gui-proof/control-hashes.json'])
 actual=git(root,'diff','--name-only',SOURCE,head,'--').decode().splitlines();assert sorted(actual)==expected
 for row in rows:assert hashlib.sha256((root/row['path']).read_bytes()).hexdigest()==row['sha256']
 for path in expected:
  f=root/path;assert f.is_file() and not f.is_symlink();assert git(root,'ls-tree','HEAD','--',path).decode().split()[0]=='100644'
 print('Exact diagnostic controls and source parent verified; no native validation claim')
def classify(root,before,after):return module(root,'impact').classify(git(root,'diff','--raw','--no-abbrev','-z','--no-renames',before,after,'--'))
def emit(**values):
 with open(os.environ['GITHUB_OUTPUT'],'a') as f:
  for k,v in values.items():f.write(k+'='+str(v)+'\n')
def impact(root):
 source(root);zero=classify(root,MAIN,CI);positive=classify(root,CI,SOURCE)
 assert zero==(True,False,'reviewed-ci-controls-only',[])
 assert positive==(True,True,'identified-product-input',PATHS)
 emit(zero_required='false',positive_required='true',gui_tests=json.dumps(PATHS,separators=(',',':')),package='true')
 print(json.dumps({'zero':zero,'positive':positive,'native_validation':'sentinel only','publication':'never executed'}))
def negative(root):
 source(root);f=root/NEG;assert not f.exists();f.write_text(NEGATIVE)
 git(root,'add','--',NEG)
 git(root,'-c','user.name=Qing Liang','-c','user.email=106448173+xue-ding-e@users.noreply.github.com','commit','--no-gpg-sign','-m','Synthetic local required-GUI failure probe')
 head=git(root,'rev-parse','HEAD').decode().strip();assert git(root,'rev-parse','HEAD^').decode().strip()==SOURCE
 assert git(root,'diff','--name-only',SOURCE,head,'--').decode().splitlines()==[NEG]
 assert classify(root,SOURCE,head)==(False,False,'reviewed-docs-or-tests-only',[NEG])
 (root/'negative-source.json').write_text(json.dumps({'head':head,'before':SOURCE,'paths':[NEG],'synthetic':True})+'\n')
 print('Created local-only assertion-negative fixture; original product and auth files unchanged')
def execute(root,kind):
 meta=json.loads((root/'negative-source.json').read_text()) if kind=='negative' else {'head':SOURCE,'before':CI,'paths':PATHS}
 env=dict(os.environ,GITHUB_SHA=meta['head'],BEFORE_SHA=meta['before'],GITHUB_EVENT_NAME='push',GUI_REQUIRED='true',GUI_TESTS=json.dumps(meta['paths'],separators=(',',':')))
 result=subprocess.run([sys.executable,'-I','-B','build/gui-changes/run.py'],cwd=root,env=env)
 raise SystemExit(result.returncode)
def inspect(root,kind):
 s=json.loads((root/'reports/gui-changes/summary.json').read_text());assert s['full_gui_suite_passed'] is False
 rows=s['results'];assert all(x['process_reaped'] for x in rows)
 if kind=='positive':
  assert s['source']['head']==SOURCE and s['source']['before']==CI
  assert [x['file'] for x in rows]==PATHS and [x['counts']['tests'] for x in rows]==[9,10]
  assert all(x['passed'] and x['exit']==0 and x['counts']['pass']==x['counts']['tests'] and all(x['counts'][k]==0 for k in ['fail','cancelled','skipped','todo']) for x in rows)
 else:
  meta=json.loads((root/'negative-source.json').read_text());assert s['source']['head']==meta['head'] and s['source']['before']==SOURCE
  assert len(rows)==1 and rows[0]['file']==NEG and rows[0]['exit']==1 and not rows[0]['passed']
  assert rows[0]['counts']=={'tests':1,'pass':0,'fail':1,'cancelled':0,'skipped':0,'todo':0}
  assert 'REQUIRED_GUI_EXPECTED_ASSERTION' in (root/'reports/gui-changes'/Path(NEG).name/'result.tap').read_text()
 emit(verified='true');print(kind+' exact terminal evidence verified')
def audit(needs):
 expected={'plan':'success','zero_gui':'skipped','zero_package':'success','positive_gui':'success','positive_package':'success','negative_gui':'failure','negative_package':'skipped','required_skip':'skipped','skip_package':'skipped'}
 assert {k:v['result'] for k,v in needs.items()}==expected
 assert needs['positive_gui']['outputs']['verified']=='true' and needs['negative_gui']['outputs']['verified']=='true'
 print('Real jobs admission verified: empty skipped allowed; two originals passed; deliberate failure and required skip blocked package sentinels. Overall workflow intentionally retains failed negative job. No native/package/release/full827 acceptance.')
if __name__=='__main__':
 mode=sys.argv[1]
 if mode=='gate':gate()
 elif mode=='impact':impact(Path(sys.argv[2]).resolve())
 elif mode=='negative':negative(Path(sys.argv[2]).resolve())
 elif mode=='execute':execute(Path(sys.argv[2]).resolve(),sys.argv[3])
 elif mode=='inspect':inspect(Path(sys.argv[2]).resolve(),sys.argv[3])
 elif mode=='audit':audit(json.loads(os.environ['NEEDS_JSON']))
 else:raise AssertionError('Unknown mode')
