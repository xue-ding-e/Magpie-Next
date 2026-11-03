import contextlib,importlib.util,io,json,os,tempfile
from pathlib import Path
s=importlib.util.spec_from_file_location('proof',Path(__file__).with_name('proof.py'));m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
checks=0
def reject(fn):
 global checks
 try:fn()
 except (AssertionError,KeyError,FileNotFoundError):checks+=1
 else:raise AssertionError('Invalid diagnostic evidence accepted')
expected={'plan':'success','zero_gui':'skipped','zero_package':'success','positive_gui':'success','positive_package':'success','negative_gui':'failure','negative_package':'skipped','required_skip':'skipped','skip_package':'skipped'}
needs={k:{'result':v,'outputs':{'verified':'true'}} for k,v in expected.items()}
m.audit(needs);checks+=1
for name in expected:
 bad=json.loads(json.dumps(needs));bad[name]['result']='cancelled';reject(lambda:m.audit(bad))
for name in ['positive_gui','negative_gui']:
 bad=json.loads(json.dumps(needs));bad[name]['outputs']={};reject(lambda:m.audit(bad))
with tempfile.TemporaryDirectory(prefix='required-gui-proof.') as d:
 root=Path(d);report=root/'reports/gui-changes';report.mkdir(parents=True);old=os.environ.get('GITHUB_OUTPUT');os.environ['GITHUB_OUTPUT']=str(root/'outputs')
 try:
  def row(path,tests=1,passed=True):return {'file':path,'process_reaped':True,'exit':0 if passed else 1,'passed':passed,'counts':{'tests':tests,'pass':tests if passed else 0,'fail':0 if passed else tests,'cancelled':0,'skipped':0,'todo':0}}
  valid={'full_gui_suite_passed':False,'source':{'head':m.SOURCE,'before':m.CI},'results':[row(m.PATHS[0],9),row(m.PATHS[1],10)]}
  def save(x): (report/'summary.json').write_text(json.dumps(x))
  save(valid);m.inspect(root,'positive');checks+=1
  for mutate in [lambda x:x['results'].pop(),lambda x:x['results'][0].update(process_reaped=False),lambda x:x['results'][1]['counts'].update(tests=9),lambda x:x.update(full_gui_suite_passed=True),lambda x:x['source'].update(head='wrong')]:
   bad=json.loads(json.dumps(valid));mutate(bad);save(bad);reject(lambda:m.inspect(root,'positive'))
  neg={'full_gui_suite_passed':False,'source':{'head':'a'*40,'before':m.SOURCE},'results':[row(m.NEG,1,False)]};(root/'negative-source.json').write_text(json.dumps({'head':'a'*40}));tap=report/Path(m.NEG).name/'result.tap';tap.parent.mkdir();tap.write_text('REQUIRED_GUI_EXPECTED_ASSERTION');save(neg);m.inspect(root,'negative');checks+=1
  tap.write_text('Browser launch error');reject(lambda:m.inspect(root,'negative'))
  tap.write_text('REQUIRED_GUI_EXPECTED_ASSERTION');neg['results'][0]['exit']=124;save(neg);reject(lambda:m.inspect(root,'negative'))
  (report/'summary.json').unlink();reject(lambda:m.inspect(root,'negative'))
 finally:
  if old is None:os.environ.pop('GITHUB_OUTPUT',None)
  else:os.environ['GITHUB_OUTPUT']=old
print(checks,'actual count/status evidence controls passed; no browser or CI')

# A deliberately skipped ancestor must not activate GitHub's implicit success()
# for a later independent diagnostic stage. Bind the exact YAML conditions.
workflow=(Path(__file__).resolve().parents[1]/'workflows/required-gui-proof.yml').read_text()
for predecessor in ['zero_package','positive_package']:
 expected="if: ${{ always() && !cancelled() && needs.plan.result == 'success' && needs."+predecessor+".result == 'success' && needs.plan.outputs.positive_required == 'true' }}"
 assert expected in workflow
 checks+=1
 def admitted(cancelled,plan,previous,required):return not cancelled and plan=='success' and previous=='success' and required=='true'
 assert admitted(False,'success','success','true');checks+=1
 for plan in ['failure','cancelled','skipped']:
  assert not admitted(False,plan,'success','true');checks+=1
 for previous in ['failure','cancelled','skipped']:
  assert not admitted(False,'success',previous,'true');checks+=1
 for required in ['false','',None]:
  assert not admitted(False,'success','success',required);checks+=1
 assert not admitted(True,'success','success','true');checks+=1
print(checks,'combined evidence and explicit dependency-condition controls passed; scheduler execution remains a CI check')
