import json,re,sys
from pathlib import Path
TOP=['TestCodexAuthExportWindowsACLProbe','TestCodexAuthWindowsClipboardAdapterProbe','TestCodexAuthWindowsACLMaskControls','TestCodexAuthExportAtomicFailureAndMode','TestCodexAuthExportCancelDuringFlush','TestCodexAuthExportWindowsPrivateAtCreation','TestCodexAuthExportWindowsCreationFailure']
SOURCE='ff997c492a908293dc0de152ee20f83152b88aa9'
def source_matches(metadata):
 return metadata.get('source')==SOURCE and metadata.get('nativeUI') is False
SUB=[TOP[0]+'/normal-inheritance',TOP[0]+'/broad-synthetic-parent']
def classify(events,process):
 if process.get('timedOut') or process.get('reaped') is not True:return 'timeout-or-unreaped'
 terminal={};output='';dacl=[];streams={}
 for e in events:
  output+=e.get('Output','')
  key=(e.get('Package',''),e.get('Test',''));streams[key]=streams.get(key,'')+e.get('Output','')
  if e.get('Test') and e.get('Action') in ('pass','fail','skip'):
   name=e['Test']
   if name in terminal:return 'duplicate-terminal'
   terminal[name]=e['Action']
 for stream in streams.values():
  for line in stream.splitlines():
   if 'AUTH_EXPORT_DACL_EVIDENCE ' in line:
    try:dacl.append(json.loads(line.split('AUTH_EXPORT_DACL_EVIDENCE ',1)[1]))
    except (ValueError,TypeError):return 'malformed-acl-evidence'
 if set(terminal)!=set(TOP+SUB) or 'skip' in terminal.values():return 'missing-or-skipped-tests'
 if len(dacl)!=2 or {x['scenario'] for x in dacl}!={'normal-inheritance','broad-synthetic-parent'}:return 'missing-acl-evidence'
 for x in dacl:
  for kind in ('parent','directory','file'):
   if not all(k in x[kind] for k in ('OwnerSID','CurrentSID','Rules','SDDL','Protected','NullDacl')):return 'incomplete-acl-evidence'
 packages=[e for e in events if not e.get('Test') and e.get('Action') in ('pass','fail','skip')]
 if len(packages)!=1:return 'missing-package-terminal'
 if all(v=='pass' for v in terminal.values()) and process['exit']==0 and packages[0]['Action']=='pass':return 'focused-probes-passed'
 failed={k for k,v in terminal.items() if v=='fail'}
 if process['exit']==1 and packages[0]['Action']=='fail' and terminal[TOP[1]]==terminal[TOP[2]]=='pass' and failed and failed<=set([TOP[0],*SUB]) and 'AUTH_EXPORT_DACL_NON_OWNER_ALLOW:' in output:return 'measured-acl-policy-failure-not-effective-access-proof'
 return 'other-test-or-environment-failure'
if __name__=='__main__':
 report=Path(sys.argv[1]);events=[json.loads(line) for line in (report/'go.jsonl').read_text(encoding='utf-8-sig').splitlines() if line.strip()];process=json.loads((report/'process-outcome.json').read_text(encoding='utf-8-sig'))
 metadata=json.loads((report/'process.json').read_text(encoding='utf-8-sig'))
 status=classify(events,process) if source_matches(metadata) else 'source-identity-mismatch'
 outcome={'source':SOURCE,'status':status,'expected_top':7,'expected_sub':2,'native_UI_covered':False,'actual_clipboard_scope':'host.Copy adapter only','full_GUI_passed':False,'vulnerability_proven':False}
 (report/'audit.json').write_text(json.dumps(outcome,indent=2)+'\n');print(json.dumps(outcome))
 if status!='focused-probes-passed':raise SystemExit(1)
