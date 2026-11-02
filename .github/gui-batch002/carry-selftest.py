import copy,importlib.util,json
from pathlib import Path
p=Path(__file__).resolve().parent;s=importlib.util.spec_from_file_location('carry',p/'carry.py');m=importlib.util.module_from_spec(s);s.loader.exec_module(m)
manifest=json.loads((p/'inventory.json').read_text());prior=json.loads((p/'prior-ledger.json').read_text());r=m.seed_records(manifest,prior['node']);assert sum(v['state']=='passed_prior' for v in r.values())==8 and sum(v['state']=='not_run' for v in r.values())==819;assert all(v['executed_in_batch'] is False for v in r.values());key=next(k for k,v in prior['files'].items() if v['state']=='passed')
def bad(edit):
 a=copy.deepcopy(prior);b=copy.deepcopy(manifest);edit(a,b)
 try:m.validate(a,b,prior['node'])
 except AssertionError:return
 raise AssertionError('Invalid prior evidence accepted')
for edit in [lambda a,b:a.update(source='wrong'),lambda a,b:a.update(node='v0'),lambda a,b:a.update(playwright='1.61.1'),lambda a,b:a.update(stop_reason='timeout'),lambda a,b:a['files'][key].update(exit=1),lambda a,b:a['files'][key].update(process_reaped=False),lambda a,b:a['files'][key].update(terminal_summary=False),lambda a,b:a['files'][key].update(sha256='wrong'),lambda a,b:a['files'][key]['counts'].update(skipped=1),lambda a,b:a['files'][key]['counts'].update(pass_=0,tests=0),lambda a,b:b['selected'].append(key),lambda a,b:a['files'].pop(key),lambda a,b:a['files'][key].update(state='not_run')]:bad(edit)
print('14 prior-evidence carry positive/negative controls passed')
