"""Carry only terminal prior-file evidence bound to identical source/runtime."""
import copy,hashlib,json
from pathlib import Path
CONTROL=Path(__file__).resolve().parent
PRIOR_SHA='e7b830c23fb295d04b9753e5a9baeeec784582e921f072ce87992f02d5f602cb'
def validate(prior,manifest,node):
 expected={f['path']:f for f in manifest['files']}
 assert len(expected)==827 and set(prior['files'])==set(expected)
 assert prior['source']==manifest['product']==manifest['accepted_prior']['source']=='94e6698e22322ae4bfa09efdd3ab2bec9238919a'
 assert prior['playwright']==manifest['accepted_prior']['playwright']=='1.62.1'
 assert prior['node']==manifest['accepted_prior']['node']==node
 assert prior['stop_reason'] is None and prior['complete_suite_passed'] is False
 accepted={p for p,v in prior['files'].items() if v['state']=='passed'}
 assert len(accepted)==8 and accepted==set(manifest['accepted_prior']['files'])
 assert not accepted & set(manifest['selected'])
 total=0
 for p,v in prior['files'].items():
  assert v['sha256']==expected[p]['sha256']
  if p not in accepted:
   assert v['state']=='not_run'
   continue
  assert v['source']==manifest['product'] and v['file_sha256']==expected[p]['sha256']
  assert v['exit']==0 and v['process_reaped'] is True and v['terminal_summary'] is True and v['counts_consistent'] is True and v['passed'] is True
  c=v['counts'];assert c['tests']>0 and c['tests']==c['pass'] and all(c[k]==0 for k in ('fail','cancelled','skipped','todo'));total+=c['tests']
 assert total==114
 records={p:{'state':'not_run','historical_state':v['previous_state'],'sha256':v['sha256'],'executed_in_batch':False} for p,v in expected.items()}
 for p in accepted:
  records[p].update(state='passed_prior',counts=copy.deepcopy(prior['files'][p]['counts']),prior_evidence={'run':41542521718,'ledger_sha256':PRIOR_SHA,'source':prior['source'],'playwright':prior['playwright'],'node':prior['node'],'report':prior['files'][p]['report']})
 return records

def seed_records(manifest,node):
 raw=(CONTROL/'prior-ledger.json').read_bytes()
 assert hashlib.sha256(raw).hexdigest()==PRIOR_SHA==manifest['accepted_prior']['ledger_sha256']
 return validate(json.loads(raw),manifest,node)
