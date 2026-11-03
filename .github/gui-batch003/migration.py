"""Verify full Git tree identities; archive old runs without promoting coverage."""
import copy
import hashlib
import json
from pathlib import Path

CONTROL = Path(__file__).resolve().parent
OLD_SOURCE = '94e6698e22322ae4bfa09efdd3ab2bec9238919a'
PRODUCT = 'ff997c492a908293dc0de152ee20f83152b88aa9'
PRIOR_SHA = 'fe24a4b69aa13c74835036d29e87f19772952ab4e2e2259e2f6ad04bfd0876fa'


def read(name):
    return json.loads((CONTROL / name).read_text())


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def tree_sha(leaves):
    """Reconstruct recursive Git trees, including Git's directory sort suffix."""
    root = {}
    for path, value in leaves.items():
        parts = path.split('/')
        assert all(p and p not in ('.', '..') for p in parts)
        node = root
        for part in parts[:-1]:
            node = node.setdefault(part, {})
            assert not {'sha', 'mode', 'type'} <= set(node)
        assert parts[-1] not in node
        assert value['type'] in ('blob', 'commit')
        assert value['mode'] in ('100644', '100755', '120000', '160000')
        node[parts[-1]] = value

    def calculate(node):
        entries = []
        for name, value in node.items():
            leaf = {'sha', 'mode', 'type'} <= set(value)
            mode = value['mode'] if leaf else '40000'
            digest = value['sha'] if leaf else calculate(value)
            key = name.encode() + (b'' if leaf else b'/')
            entries.append((key, mode.encode() + b' ' + name.encode() + b'\0' + bytes.fromhex(digest)))
        body = b''.join(entry for _, entry in sorted(entries))
        return hashlib.sha1(b'tree ' + str(len(body)).encode() + b'\0' + body).hexdigest()

    return calculate(root)


def verify_trees():
    source = read('source.json')
    old, current = read('old-source-tree.json'), read('current-source-tree.json')
    assert source['source'] == PRODUCT and source['old_source'] == OLD_SOURCE
    assert tree_sha(old['leaves']) == old['official_tree'] == source['old_tree']
    assert tree_sha(current['leaves']) == current['official_tree'] == source['tree']
    subsets = {}
    for prefix in ('internal/gui/tests/', 'internal/gui/assets/'):
        before = {p: v for p, v in old['leaves'].items() if p.startswith(prefix)}
        after = {p: v for p, v in current['leaves'].items() if p.startswith(prefix)}
        assert before and before == after
        subsets[prefix] = {'identical_entries': len(after), 'path_mode_type_blob_equal': True}
    differences = [p for p in sorted(old['leaves'].keys() | current['leaves'].keys())
                   if old['leaves'].get(p) != current['leaves'].get(p)]
    assert len(differences) == 13
    return {
        'old_source': OLD_SOURCE, 'source': PRODUCT,
        'old_tree': old['official_tree'], 'current_tree': current['official_tree'],
        'full_tree_hashes_reconstructed': True,
        'old_leaf_count': len(old['leaves']), 'current_leaf_count': len(current['leaves']),
        'identical_subtrees': subsets, 'changed_paths': differences,
        'coverage_policy': 'Archive only. Identical GUI test/assets bytes do not prove unchanged external Go or other files read by arbitrary static tests. No prior execution is a pass on the new source.',
        'accepted_current_source_passes': 0,
    }


def seed_records(manifest, node):
    assert sha256(CONTROL / 'prior-ledger.json') == PRIOR_SHA == manifest['prior_ledger']['ledger_sha256']
    prior = read('prior-ledger.json')
    expected = {f['path']: f for f in manifest['files']}
    assert len(expected) == 827 and set(prior['files']) == set(expected)
    assert prior['source'] == OLD_SOURCE and manifest['product'] == PRODUCT
    assert prior['node'] == node == 'v22.23.4'
    assert prior['playwright'] == '1.62.1' and prior['complete_suite_passed'] is False
    assert prior['stop_reason'] is None
    states = {}
    for name, evidence in prior['files'].items():
        assert evidence['sha256'] == expected[name]['sha256']
        states[evidence['state']] = states.get(evidence['state'], 0) + 1
    assert states == {'not_run': 803, 'passed_prior': 8, 'passed': 12, 'failed': 4}
    selected = manifest['selected']
    assert len(selected) == len(set(selected)) == 32
    names = list(expected)
    offset = names.index('internal/gui/tests/provider-many-keys.test.cjs') + 1
    next_unseen = [p for p in names[offset:] if prior['files'][p]['state'] == 'not_run'][:32]
    assert selected == next_unseen
    records = {}
    for name, item in expected.items():
        old = prior['files'][name]
        records[name] = {
            'state': 'not_run', 'source': PRODUCT, 'sha256': item['sha256'],
            'executed_in_batch': False, 'previous_state': item['previous_state'],
            'old_source_provenance': {
                'source': OLD_SOURCE, 'ledger_sha256': PRIOR_SHA,
                'ledger_run': 41544763327, 'state': old['state'],
                'evidence': copy.deepcopy(old), 'accepted_on_current_source': False,
            },
        }
    assert all(prior['files'][p]['state'] == 'not_run' and
               prior['files'][p]['historical_state'] == 'not-loaded' for p in selected)
    return records


if __name__ == '__main__':
    print(json.dumps(verify_trees(), indent=2))
