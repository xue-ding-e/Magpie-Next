"""Compile the resource worker within the same owned-tree and job budget guard."""
import importlib.util
import json
import os
from pathlib import Path
import sys

CONTROL=Path(__file__).resolve().parent
ROOT=CONTROL.parents[1]
spec=importlib.util.spec_from_file_location('dictionary_owned_compile',CONTROL/'owned_process.py')
owned=importlib.util.module_from_spec(spec)
spec.loader.exec_module(owned)
reports=ROOT/'.validation-runtime/reports'
reports.mkdir(parents=True,exist_ok=True)
binary=Path(os.environ['RUNNER_TEMP']).resolve(strict=True)/'dictionary-resource.test'
result=owned.run_owned(['go','test','-mod=readonly','-tags','nogui','-p=1','-c','-o',str(binary),'./internal/localdictionary'],
    cwd=ROOT,environment=dict(os.environ),log_path=reports/'resource-compile.log',timeout=120)
with (reports/'resource-compile.log').open('r',encoding='utf-8') as stream:
    sys.stdout.write(stream.read((16<<20)+1))
(reports/'resource-compile.json').write_text(json.dumps(result,indent=2)+'\n')
if result['returncode']!=0 or result['timed_out'] or result['orphaned'] or not result['clean']:
    raise SystemExit(1)
