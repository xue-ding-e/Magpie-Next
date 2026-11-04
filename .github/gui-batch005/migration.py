"""Carry genuine same-source terminal states, keeping old-source history separate."""
import collections
import copy
import hashlib
import json
from pathlib import Path

CONTROL = Path(__file__).resolve().parent
OLD_SOURCE = '94e6698e22322ae4bfa09efdd3ab2bec9238919a'
PRODUCT = 'ff997c492a908293dc0de152ee20f83152b88aa9'
PRIOR_SHA = '09f5b0c0b824b8fac2b8fcaa21bc022172955d456bf622bc1bbe3c1aee3a666c'


def read(name):
    return json.loads((CONTROL / name).read_text())


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def verify_source_continuity():
    # Source has not migrated again. The prior independent complete-tree proof
    # remains a small hash-bound historical receipt rather than duplicated trees.
    source, proof, index = read('source.json'), read('source-migration.json'), read('historical-evidence-index.json')
    assert source['source'] == proof['source'] == PRODUCT
    assert source['tree'] == proof['current_tree'] == 'bc56abd9c642a1315ea3a3f32a30d9998e15137a'
    assert proof['old_source'] == OLD_SOURCE and proof['full_tree_hashes_reconstructed'] is True
    assert sha256(CONTROL / 'source-migration.json') == index['prior_full_tree_migration']['sha256']
    assert proof['accepted_current_source_passes'] == 0
    assert proof['identical_subtrees']['internal/gui/tests/']['identical_entries'] == 842
    assert proof['identical_subtrees']['internal/gui/assets/']['identical_entries'] == 218
    return proof


def validate_prior(manifest, node):
    assert sha256(CONTROL / 'prior-ledger.json') == PRIOR_SHA == manifest['prior_ledger']['ledger_sha256']
    prior, outcome = read('prior-ledger.json'), read('prior-outcome.json')
    expected = {f['path']: f for f in manifest['files']}
    assert len(expected) == 827 and set(prior['files']) == set(expected)
    assert prior['source'] == manifest['product'] == outcome['source'] == PRODUCT
    assert prior['batch'] == 'recovery-004'
    assert prior['node'] == node == 'v22.23.4' and prior['playwright'] == '1.62.1'
    assert prior['complete_suite_passed'] is False and prior['full_suite_gate'] == 'RED'
    assert prior['stop_reason'] is None
    assert collections.Counter(v['state'] for v in prior['files'].values()) == {'not_run': 763, 'passed': 57, 'failed': 7}
    all_totals, new_totals, new_states = collections.Counter(), collections.Counter(), collections.Counter()
    executed, new_executed, old_executions = set(), set(), 0
    failed = set()
    for path, row in prior['files'].items():
        assert row['source'] == PRODUCT and row['sha256'] == expected[path]['sha256']
        old = row['old_source_provenance']
        assert old['source'] == OLD_SOURCE and old['accepted_on_current_source'] is False
        old_executions += old['state'] != 'not_run'
        if row['state'] == 'not_run':
            assert row['executed_in_batch'] is False and row['carried_current_source_evidence'] is False
            continue
        executed.add(path)
        assert row['process_reaped'] is True and row['terminal_summary'] is True and row['counts_consistent'] is True
        assert row['file_sha256'] == expected[path]['sha256']
        counts = row['counts']
        assert counts['tests'] > 0 and sum(counts[k] for k in ('pass', 'fail', 'cancelled', 'skipped', 'todo')) == counts['tests']
        assert all(counts[k] == 0 for k in ('cancelled', 'skipped', 'todo'))
        if row['state'] == 'passed':
            assert row['passed'] is True and row['exit'] == 0 and counts['pass'] == counts['tests']
        else:
            failed.add(path)
            assert row['state'] == 'failed' and row['passed'] is False and row['exit'] == 1 and counts['fail'] > 0
        if row['executed_in_batch']:
            new_executed.add(path)
            new_states[row['state']] += 1
            new_totals.update(counts)
            assert row['carried_current_source_evidence'] is False
            assert row['command'] == ['node', '--test', '--test-reporter=tap', '--test-concurrency=1', path]
            if row['state'] == 'failed':
                assert outcome['failed_files'][path] == row
        else:
            assert row['carried_current_source_evidence'] is True
            receipt = row['last_execution']
            assert receipt['run'] == 41598441535 and receipt['batch'] == 'recovery-003'
            assert receipt['source'] == PRODUCT and receipt['ledger_sha256'] == '186c852350cf25b6eefd15f840b0e29ae70fefb7fa6a9b9c85bfc4a814580661'
            for key in ('state', 'file_sha256', 'counts', 'exit', 'process_reaped', 'elapsed_seconds'):
                assert receipt[key] == row[key]
            assert 'report' not in row and 'command' not in row
        all_totals.update(counts)
    assert old_executions == 24 and len(executed) == 64 and len(new_executed) == 32
    assert new_states == {'passed': 26, 'failed': 6}
    assert new_totals == outcome['counts'] == {'tests': 305, 'pass': 293, 'fail': 12, 'cancelled': 0, 'skipped': 0, 'todo': 0}
    assert all_totals == {'tests': 862, 'pass': 848, 'fail': 14, 'cancelled': 0, 'skipped': 0, 'todo': 0}
    assert failed == set(outcome['failed_files']) | {'internal/gui/tests/quota-count-wrap.test.cjs'}
    selected = manifest['selected']
    assert len(selected) == len(set(selected)) == 32 and not set(selected) & executed
    names = list(expected)
    start = names.index('internal/gui/tests/removed-hide.test.cjs') + 1
    assert selected == [p for p in names[start:] if prior['files'][p]['state'] == 'not_run'][:32]
    assert all(prior['files'][p]['previous_state'] == 'not-loaded' for p in selected)
    return prior


def seed_records(manifest, node):
    prior = validate_prior(manifest, node)
    records = copy.deepcopy(prior['files'])
    for path, row in records.items():
        terminal = row['state'] in ('passed', 'failed')
        if row['executed_in_batch']:
            # Only the newly executed32 receive their direct execution receipt.
            # Existing32 receipts retain their original batch003 source/run;
            # never nest an entire ledger or relabel old execution as batch004.
            row['last_execution'] = {
                'source': PRODUCT, 'run': 41604270125, 'batch': 'recovery-004',
                'ledger_sha256': PRIOR_SHA, 'report': row.pop('report'),
                'command': row.pop('command'), 'state': row['state'],
                'file_sha256': row['file_sha256'], 'counts': copy.deepcopy(row['counts']),
                'exit': row['exit'], 'process_reaped': row['process_reaped'],
                'elapsed_seconds': row['elapsed_seconds'],
            }
        row['executed_in_batch'] = False
        row['carried_current_source_evidence'] = terminal
    assert collections.Counter(v['state'] for v in records.values()) == {'not_run': 763, 'passed': 57, 'failed': 7}
    return records
