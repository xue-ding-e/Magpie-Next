"""Temp paths are children of the home already owned by ci_home cleanup."""
import json,os
from pathlib import Path
records=[line.removeprefix('record=') for line in Path(os.environ['GITHUB_OUTPUT']).read_text().splitlines() if line.startswith('record=')]
assert len(records)==1
record=json.loads(records[0]);home=Path(record['home']);root=Path(os.environ['RUNNER_TEMP']).resolve(strict=True)
assert home.parent==root and home.name.startswith('magpie-ci-home.')
assert home.lstat().st_ino==record['home_ino'] and home.lstat().st_dev==record['home_dev']
assert all(not (p/'.git').exists() for p in [home,*home.parents])
tmp=home/'tmp';tmp.mkdir(mode=0o700)
with open(os.environ['GITHUB_ENV'],'a',encoding='utf-8') as f:
 for key in ('TMPDIR','TMP','TEMP'):print(f'{key}={tmp}',file=f)
