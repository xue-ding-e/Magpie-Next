import copy,importlib.util
from pathlib import Path
s=importlib.util.spec_from_file_location('count',Path(__file__).with_name('count.py'));m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
def fixture():
 e=[{'Action':'pass','Package':p,'Test':t} for p,ts in m.EXPECTED.items() for t in sorted(ts)]
 p='github.com/yetone/magpie/internal/provider';t='TestSavedCodexAuthJSONRejectsWrongModeAndIdentity'
 e += [{'Action':'pass','Package':p,'Test':t+'/synthetic'+str(i)} for i in range(10)]
 e += [{'Action':'pass','Package':p} for p in m.EXPECTED]
 return e
e=fixture();assert m.check(e)['top_level']==8
checks=1
for mutate in [lambda e:e.pop(0),lambda e:e.append(e[0]),lambda e:e[0].update(Action='skip'),lambda e:e[0].update(Action='fail'),lambda e:e.pop(-1),lambda e:e[0].update(Test='Unexpected'),lambda e:e.pop(8)]:
 e=fixture();mutate(e)
 try:m.check(e)
 except AssertionError:checks+=1
 else:raise AssertionError('Invalid terminal set accepted')
print(checks,'synthetic count controls passed; no Go/OS execution')
