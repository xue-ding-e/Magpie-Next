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
e,p=fixture();e[0]['Action']='fail';e[3]['Action']='fail';e[-1]['Action']='fail';e.append({'Output':'AUTH_EXPORT_DACL_NON_OWNER_ALLOW: directory SID=synthetic'});p['exit']=1
assert m.classify(e,p)=='measured-acl-policy-failure-not-effective-access-proof';checks+=1
e[-1]['Output']='PowerShell startup failed';assert m.classify(e,p)=='other-test-or-environment-failure';checks+=1
e[-1]['Output']='AUTH_EXPORT_DACL_NON_OWNER_ALLOW: directory SID=synthetic';e[1]['Action']='fail';assert m.classify(e,p)=='other-test-or-environment-failure';checks+=1
# Go test JSON splits long output independently of line/JSON boundaries.
e,p=fixture();original=e[5]['Output'];e[5:6]=[{'Test':'split','Output':original[:80]},{'Test':'other','Output':'unrelated\n'},{'Test':'split','Output':original[80:]}]
assert m.classify(e,p)=='focused-probes-passed';checks+=1
for mutate in ['truncated','interleaved','duplicate','invalid']:
 e,p=fixture();original=e[5]['Output']
 if mutate=='truncated':e[5]['Output']=original[:80]
 elif mutate=='interleaved':e[5:6]=[{'Test':'one','Output':original[:80]},{'Test':'two','Output':original[80:]}]
 elif mutate=='duplicate':e.append(copy.deepcopy(e[5]))
 else:e[5]['Output']='AUTH_EXPORT_DACL_EVIDENCE {not-json}\n'
 assert m.classify(e,p)!='focused-probes-passed';checks+=1
print(checks,'synthetic terminal/ACL classification controls passed; no Windows operations')
