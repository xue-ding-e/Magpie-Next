"""Copy reviewed test-only overlays; never overwrite a product or existing test."""
from pathlib import Path
import sys

ROOT = Path(__file__).resolve().parents[2]
CONTROL = ROOT / '.github/dictionary-core-validation'
FILES = {
    'core': {'request_scope_test.go': 'internal/redact/dictionary_validation_test.go',
             'gui_offline_test.go': 'internal/gui/dictionary_validation_offline_test.go',
             'redact_offline_test.go': 'internal/redact/dictionary_validation_offline_test.go',
             'localdictionary_offline_test.go': 'internal/localdictionary/dictionary_validation_offline_test.go',
             'backup_offline_test.go': 'internal/backup/dictionary_validation_offline_test.go',
             'gateway_offline_test.go': 'internal/gateway/dictionary_validation_offline_test.go'},
    'resources': {'resource_test.go': 'internal/localdictionary/dictionary_validation_resource_test.go',
                  'localdictionary_offline_test.go': 'internal/localdictionary/dictionary_validation_offline_test.go'},
}
for source, dest in FILES[sys.argv[1]].items():
    with (ROOT / dest).open('xb') as stream:
        stream.write((CONTROL / 'fixtures' / source).read_bytes())
