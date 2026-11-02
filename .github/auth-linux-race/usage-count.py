import json,sys
from pathlib import Path
EXPECTED={'TestLoginUsageClaudeAskedOnTheReadsTick','TestLoginUsageClaudeAskedMeanwhile','TestLoginUsageViaWaitsForReadingsThroughTheOld','TestLoginUsageEachAccount','TestLoginUsageMixedCache','TestSubscriptionUsageEachAccount','TestLoginUsageSharedWithUsagePage','TestLoginUsageReadOnceAtATime'}
def check(events):
 assert events and not any(e.get('Action') in ('fail','skip') for e in events)
 passed=[e['Test'] for e in events if e.get('Action')=='pass' and e.get('Test')]
 assert {t for t in passed if '/' not in t}==EXPECTED
 assert len(passed)==len(set(passed))
 assert all(t.split('/')[0] in EXPECTED for t in passed)
 packages=[e for e in events if e.get('Action')=='pass' and not e.get('Test')]
 assert len(packages)==1 and packages[0]['Package']=='github.com/yetone/magpie/internal/provider'
 assert all(e.get('Package')=='github.com/yetone/magpie/internal/provider' for e in events)
 return {'top_level':8,'subtests':sum('/' in t for t in passed),'skips':0,'scope':'original account-usage race selection only'}
if __name__=='__main__':print(json.dumps(check([json.loads(l) for l in Path(sys.argv[1]).read_text().splitlines() if l.strip()]),indent=2))
