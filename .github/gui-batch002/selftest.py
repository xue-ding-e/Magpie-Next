"""Lightweight classifier controls; no browser/product execution."""
from pathlib import Path
import importlib.util
p=Path(__file__).with_name('run.py');spec=importlib.util.spec_from_file_location('pilot',p);m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
def tap(passed=3,failed=0,skipped=0,cancelled=0,todo=0):
 return '\n'.join(f'# {k} {v}' for k,v in {'tests':3,'pass':passed,'fail':failed,'skipped':skipped,'cancelled':cancelled,'todo':todo}.items())
assert m.counts(tap(),0)['passed']
assert m.counts(tap(2,1),1)['terminal_summary'] and not m.counts(tap(2,1),1)['passed']
for text,code in [('',0),(tap(),1),(tap(2,1),0),(tap(2,0,1),0),(tap(2,0,0,1),1),(tap(2,0,0,0,1),0),(tap()+'\n# tests 3',0),(tap(),-9),(tap(4),0),(tap(0),0)]:assert not m.counts(text,code)['passed']
print('12 classifier controls passed; not browser acceptance')
