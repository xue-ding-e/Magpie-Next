import json,sys
from pathlib import Path
EXPECTED={
 'github.com/yetone/magpie/internal/provider':{'TestSavedCodexAuthJSONPreservesOriginal','TestSavedCodexAuthJSONMasksUnknownNestedFields','TestSavedCodexAuthJSONRejectsWrongModeAndIdentity','TestSavedCodexAuthJSONRejectsChangedSnapshot'},
 'github.com/yetone/magpie/internal/gui':{'TestCodexAuthExportAtomicFailureAndMode','TestCodexAuthExportWebAuthenticationAndPreview','TestCodexAuthExportCancelDuringFlush','TestCodexAuthExportRequiresTrustedTransport'},
}
def check(events):
 assert events and not any(e.get('Action') in ('fail','skip') for e in events)
 passed=[(e.get('Package'),e['Test']) for e in events if e.get('Action')=='pass' and e.get('Test')]
 tops={(p,t) for p,t in passed if '/' not in t}
 assert tops=={(p,t) for p,tests in EXPECTED.items() for t in tests}
 assert len(passed)==18 and len(set(passed))==18
 assert len([t for p,t in passed if '/' in t])==10
 assert all(p in EXPECTED and t.split('/')[0] in EXPECTED[p] for p,t in passed)
 assert {e.get('Package') for e in events if e.get('Action')=='pass' and not e.get('Test')}==set(EXPECTED)
 return {'top_level':8,'subtests':10,'packages':2,'skips':0,'scope':'synthetic native auth only; no desktop UI/full-suite acceptance'}
if __name__=='__main__':print(json.dumps(check([json.loads(l) for l in Path(sys.argv[1]).read_text().splitlines() if l.strip()]),indent=2))
