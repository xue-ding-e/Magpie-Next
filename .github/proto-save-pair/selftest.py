import importlib.util
from pathlib import Path
p=Path(__file__).resolve().parent
s=importlib.util.spec_from_file_location('runner',p/'run.py');m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
def tap(passed,failed,error='',skip=0):
 return ''.join('not ok '+str(i)+' - synthetic\n  ---\n  error: '+error+'\n  code: ERR_SYNTHETIC\n  ...\n' for i in range(failed))+f'# tests 4\n# pass {passed}\n# fail {failed}\n# cancelled 0\n# skipped {skip}\n# todo 0\n'
assert m.classify(tap(4,0),0,'pass')['expected_result_matched']
for kind,error in [('old-delayed','the last account was picked and saved: undefined'),('wrong-payload','the last account was picked and saved: ["incorrect"]'),('http-error','the settings save completed successfully'),('aborted-post','|-\n    page.waitForResponse: Timeout 5000ms exceeded.\n    Call log:')]:
 t=tap(0,4,error)
 assert m.classify(t,1,kind)['expected_result_matched']
 assert not m.classify(t,0,kind)['expected_result_matched']
 assert not m.classify(t.replace(error,'unrelated failure'),1,kind)['expected_result_matched']
for text,code in [(tap(0,4,'unrelated'),1),(tap(4,0),1),(tap(3,1,'unrelated'),0),(tap(4,0)+'# tests 4\n',0),(tap(4,0,skip=1),0),(tap(4,0),124),(tap(4,0)+'browserType.launch:',0),('',0)]:
 assert not m.classify(text,code,'pass')['expected_result_matched']
print('21 classifier positive/negative checks passed')
