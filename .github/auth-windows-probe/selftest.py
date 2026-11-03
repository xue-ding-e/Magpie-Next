import copy,importlib.util
from pathlib import Path
s=importlib.util.spec_from_file_location('audit',Path(__file__).with_name('audit.py'));m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
import json
acl={'OwnerSID':'synthetic-owner','CurrentSID':'synthetic-owner','Rules':[],'SDDL':'synthetic-only','Protected':True,'NullDacl':False}
def fixture():
 events=[{'Test':n,'Action':'pass'} for n in m.TOP+m.SUB]
 for scenario in ['normal-inheritance','broad-synthetic-parent']:events.append({'Output':'AUTH_EXPORT_DACL_EVIDENCE '+json.dumps({'scenario':scenario,'parent':acl,'directory':acl,'file':acl})+'\n'})
 events.append({'Action':'pass','Package':'synthetic/gui'});return events,{'exit':0,'timedOut':False,'reaped':True}
checks=0
e,p=fixture();assert m.classify(e,p)=='focused-probes-passed';checks+=1
for edit in [lambda e,p:e.pop(0),lambda e,p:e.append(e[0]),lambda e,p:e[0].update(Action='skip'),lambda e,p:p.update(exit=1),lambda e,p:p.update(timedOut=True),lambda e,p:p.update(reaped=False),lambda e,p:e.pop(-1),lambda e,p:e.pop(-2)]:
 e,p=fixture();edit(e,p);assert m.classify(e,p)!='focused-probes-passed';checks+=1
e,p=fixture();e[0]['Action']='fail';e[len(m.TOP)]['Action']='fail';e[-1]['Action']='fail';e.append({'Output':'AUTH_EXPORT_DACL_NON_OWNER_ALLOW: directory SID=synthetic'});p['exit']=1
assert m.classify(e,p)=='measured-acl-policy-failure-not-effective-access-proof';checks+=1
e[-1]['Output']='PowerShell startup failed';assert m.classify(e,p)=='other-test-or-environment-failure';checks+=1
e[-1]['Output']='AUTH_EXPORT_DACL_NON_OWNER_ALLOW: directory SID=synthetic';e[1]['Action']='fail';assert m.classify(e,p)=='other-test-or-environment-failure';checks+=1
# Go test JSON splits long output independently of line/JSON boundaries.
e,p=fixture();original=e[len(m.TOP)+len(m.SUB)]['Output'];e[len(m.TOP)+len(m.SUB):len(m.TOP)+len(m.SUB)+1]=[{'Test':'split','Output':original[:80]},{'Test':'other','Output':'unrelated\n'},{'Test':'split','Output':original[80:]}]
assert m.classify(e,p)=='focused-probes-passed';checks+=1
for mutate in ['truncated','interleaved','duplicate','invalid']:
 e,p=fixture();original=e[len(m.TOP)+len(m.SUB)]['Output']
 if mutate=='truncated':e[len(m.TOP)+len(m.SUB)]['Output']=original[:80]
 elif mutate=='interleaved':e[len(m.TOP)+len(m.SUB):len(m.TOP)+len(m.SUB)+1]=[{'Test':'one','Output':original[:80]},{'Test':'two','Output':original[80:]}]
 elif mutate=='duplicate':e.append(copy.deepcopy(e[len(m.TOP)+len(m.SUB)]))
 else:e[len(m.TOP)+len(m.SUB)]['Output']='AUTH_EXPORT_DACL_EVIDENCE {not-json}\n'
 assert m.classify(e,p)!='focused-probes-passed';checks+=1
for name in m.TOP[3:]:
 for action in ['missing','skip','fail']:
  e,p=fixture();row=next(x for x in e if x.get('Test')==name)
  if action=='missing':e.remove(row)
  else:row['Action']=action
  assert m.classify(e,p)!='focused-probes-passed';checks+=1
assert m.source_matches({'source':m.SOURCE,'nativeUI':False});checks+=1
for bad in [{},{'source':'a27f0957427b4ba86f7c140fa38068922adcc9ea','nativeUI':False},{'source':m.SOURCE,'nativeUI':None},{'source':m.SOURCE,'nativeUI':True},{'source':[m.SOURCE],'nativeUI':False}]:
 assert not m.source_matches(bad);checks+=1
print(checks,'synthetic terminal/ACL classification controls passed; no Windows operations')
