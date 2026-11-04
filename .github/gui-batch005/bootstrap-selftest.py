"""Lightweight byte-equivalence, bounded-decoder and strict-bootstrap controls."""
import base64
import copy
import gzip
import hashlib
import importlib.util
import io
import json
import os
import shutil
import tempfile
import zlib
from pathlib import Path

CONTROL = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('bootstrap', CONTROL / 'bootstrap.py')
b = importlib.util.module_from_spec(spec)
spec.loader.exec_module(b)
negative_controls = 0


def reject(action):
    global negative_controls
    try:
        action()
    except (AssertionError, ValueError, KeyError, OSError, zlib.error):
        negative_controls += 1
        return
    raise AssertionError('Expected strict rejection')


def deterministic_gzip(raw):
    output = io.BytesIO()
    with gzip.GzipFile(filename='', mode='wb', fileobj=output, compresslevel=9, mtime=0) as stream:
        stream.write(raw)
    return output.getvalue()


raw = b.load_bundle()
assert len(raw) == b.SIZE == 835286
assert hashlib.sha256(raw).hexdigest() == b.SHA256
assert hashlib.sha1(b'blob 835286\0' + raw).hexdigest() == b.GIT_BLOB
encoded = b''.join((CONTROL / part['path']).read_bytes() for part in b.EXPECTED_META['parts'])
compressed = base64.b64decode(encoded, validate=True)
assert deterministic_gzip(raw) == deterministic_gzip(raw) == compressed
assert len(compressed) == 54309 and len(encoded) == 72412
assert [p['size'] for p in b.EXPECTED_META['parts']] == [32768, 32768, 6876]
print('PASS: real835286-byte reconstruction, exact SHA256/Gitblob; deterministic mtime0 gzip54309 -> base6472412; three bounded parts')

for bad in [compressed[:-1], compressed + b'junk', compressed + deterministic_gzip(b''),
            deterministic_gzip(b'x' * (b.SIZE + 1)), deterministic_gzip(raw[:-1]),
            deterministic_gzip(b'x' + raw[1:]), b'not-gzip', b'x' * (100 * 1024 + 1)]:
    reject(lambda bad=bad: b.inflate_one(bad))
reject(lambda: b.strict_json(b'{"a":1,"a":2}'))
reject(lambda: b.verify_bytes(raw + b'\n'))
for path in ['../escape', '/absolute', 'ledger-parts/../escape', 'ledger-parts//part-000.b64',
             'ledger-parts\\part-000.b64', './ledger-parts/part-000.b64', 'bad\npath']:
    reject(lambda path=path: b.safe_relative(path))

with tempfile.TemporaryDirectory(prefix='ledger-controls-', dir='/tmp') as temporary:
    root = Path(temporary)
    fixture = root / 'bundle'
    fixture.mkdir()

    def reset_bundle():
        for child in fixture.iterdir():
            if child.is_dir() and not child.is_symlink():
                shutil.rmtree(child)
            else:
                child.unlink()
        shutil.copyfile(CONTROL / 'ledger-encoding.json', fixture / 'ledger-encoding.json')
        shutil.copytree(CONTROL / 'ledger-parts', fixture / 'ledger-parts')

    reset_bundle()
    assert b.load_bundle(fixture) == raw
    mutations = [
        lambda m: m.update(output='../escape'),
        lambda m: m.update(output_size=b.SIZE + 1),
        lambda m: m.update(output_git_blob='0' * 40),
        lambda m: m.update(version=True),
        lambda m: m.update(extra='unrecognized'),
        lambda m: m['parts'].pop(),
        lambda m: m['parts'].append(copy.deepcopy(m['parts'][0])),
        lambda m: m['parts'].__setitem__(1, copy.deepcopy(m['parts'][0])),
        lambda m: m['parts'].reverse(),
        lambda m: m['parts'][0].update(path='../../escape'),
        lambda m: m['parts'][0].update(size=32769),
    ]
    for mutation in mutations:
        reset_bundle()
        metadata = copy.deepcopy(b.EXPECTED_META)
        mutation(metadata)
        (fixture / 'ledger-encoding.json').write_text(json.dumps(metadata))
        reject(lambda: b.load_bundle(fixture))
    for mutation in ['missing', 'extra', 'oversized', 'corrupt', 'bad_base64', 'symlink', 'directory']:
        reset_bundle()
        part = fixture / 'ledger-parts/part-000.b64'
        if mutation == 'missing':
            part.unlink()
        elif mutation == 'extra':
            (fixture / 'ledger-parts/part-003.b64').write_text('AAAA')
        elif mutation == 'oversized':
            part.write_bytes(part.read_bytes() + b'A')
        elif mutation == 'corrupt':
            data = part.read_bytes()
            part.write_bytes((b'A' if data[:1] != b'A' else b'B') + data[1:])
        elif mutation == 'bad_base64':
            part.write_bytes(b'!' + part.read_bytes()[1:])
        elif mutation == 'symlink':
            part.unlink()
            part.symlink_to(CONTROL / 'ledger-parts/part-000.b64')
        elif mutation == 'directory':
            part.unlink()
            part.mkdir()
        reject(lambda: b.load_bundle(fixture))
    reset_bundle()
    parts = fixture / 'ledger-parts'
    shutil.rmtree(parts)
    parts.symlink_to(CONTROL / 'ledger-parts', target_is_directory=True)
    reject(lambda: b.load_bundle(fixture))
    reset_bundle()
    (fixture / 'ledger-encoding.json').write_text('{"version":1,"version":1}')
    reject(lambda: b.load_bundle(fixture))

    # Materialization writes only the fixed output name, atomically and without
    # replacing anything existing. The real restored bytes are checked again.
    target_dir = root / 'output'
    target_dir.mkdir()
    target = target_dir / b.OUTPUT
    assert b.materialize(raw, target_dir) is True and target.read_bytes() == raw
    before = target.stat()
    assert b.materialize(raw, target_dir) is False
    after = target.stat()
    assert (before.st_ino, before.st_mtime_ns) == (after.st_ino, after.st_mtime_ns)
    assert not list(target_dir.glob('.ledger-restore-*'))
    target.write_bytes(b'corrupt-existing')
    reject(lambda: b.materialize(raw, target_dir))
    assert target.read_bytes() == b'corrupt-existing'
    target.unlink()
    outside = root / 'untouched'
    outside.write_bytes(b'outside remains untouched')
    target.symlink_to(outside)
    reject(lambda: b.materialize(raw, target_dir))
    assert outside.read_bytes() == b'outside remains untouched'
    target.unlink()
    target.symlink_to(root / 'missing')
    reject(lambda: b.materialize(raw, target_dir))
    target.unlink()
    target.mkdir()
    reject(lambda: b.materialize(raw, target_dir))
    target.rmdir()
    outside.write_bytes(raw)
    os.link(outside, target)
    reject(lambda: b.materialize(raw, target_dir))
    assert outside.read_bytes() == raw
    target.unlink()
    reject(lambda: b.materialize(raw + b'extra', target_dir))
    assert not target.exists()
print('PASS: strict metadata/chunks, bounded inflation, missing/duplicate/corrupt/oversized/tail rejection; fixed exclusive output and idempotency')

# The fixture below mocks Git responses only. Real HEAD checks remain mandatory
# in CI; local tests do not claim that a validation commit has been activated.
controls = b.strict_json((CONTROL / 'control-hashes.json').read_bytes())
allowed = set(controls) | {b.PREFIX + 'control-hashes.json'}
head = 'a' * 40
responses = {
    ('rev-parse', 'HEAD'): head,
    ('rev-parse', 'HEAD^'): b.PRODUCT,
    ('rev-list', '--parents', '-n', '1', 'HEAD'): head + ' ' + b.PRODUCT,
    ('diff', '--name-only', 'HEAD^', 'HEAD'): '\n'.join(sorted(allowed | {'.github/workflows/gui-batch005.yml'})),
    ('ls-files', '--', b.PREFIX): '\n'.join(sorted(allowed)),
    ('diff', '--name-only', 'HEAD', '--', b.PREFIX, '.github/workflows/gui-batch005.yml'): '',
}
old_git = b.git
keys = ['GITHUB_REPOSITORY', 'GITHUB_EVENT_NAME', 'GITHUB_REF', 'GITHUB_SHA']
old_env = {key: os.environ.get(key) for key in keys}
os.environ.update(GITHUB_REPOSITORY='xue-ding-e/Magpie-Next', GITHUB_EVENT_NAME='push',
                  GITHUB_REF='refs/heads/validation/auth-gui-batch005-20261103', GITHUB_SHA=head)
try:
    b.git = lambda *args: responses[args]
    assert b.validate_tracked() == controls
    for key, value in [
        (('rev-parse', 'HEAD^'), 'b' * 40),
        (('rev-list', '--parents', '-n', '1', 'HEAD'), head + ' ' + b.PRODUCT + ' ' + 'b' * 40),
        (('diff', '--name-only', 'HEAD^', 'HEAD'), '\n'.join(sorted(allowed))),
        (('diff', '--name-only', 'HEAD^', 'HEAD'), '\n'.join(sorted(allowed | {'.github/workflows/gui-batch005.yml', b.PREFIX + b.OUTPUT}))),
        (('ls-files', '--', b.PREFIX), '\n'.join(sorted(allowed | {b.PREFIX + b.OUTPUT}))),
        (('diff', '--name-only', 'HEAD', '--', b.PREFIX, '.github/workflows/gui-batch005.yml'), b.PREFIX + 'control-hashes.json'),
    ]:
        saved = responses[key]
        responses[key] = value
        reject(b.restore)
        responses[key] = saved
    # An invalid Git gate must fail before decoding or materializing any bytes.
    order = []
    originals = b.validate_tracked, b.load_bundle, b.materialize, b.verify_existing
    try:
        b.validate_tracked = lambda: (order.append('tracked'), controls)[1]
        b.load_bundle = lambda: (order.append('decode'), raw)[1]
        b.materialize = lambda data: order.append('materialize')
        b.verify_existing = lambda: order.append('verify')
        assert b.restore() == controls
        assert order == ['tracked', 'decode', 'materialize', 'verify']
        order.clear()
        def fail_tracked():
            order.append('tracked')
            raise AssertionError('synthetic invalid HEAD')
        b.validate_tracked = fail_tracked
        reject(b.restore)
        assert order == ['tracked']
    finally:
        b.validate_tracked, b.load_bundle, b.materialize, b.verify_existing = originals
finally:
    b.git = old_git
    for key, value in old_env.items():
        if value is None:
            os.environ.pop(key, None)
        else:
            os.environ[key] = value
assert negative_controls >= 45
print(f'PASS: {negative_controls} rejection controls; tracked/derived separation, Git gate first, no overwrite and no alternate bootstrap bypass')
print('LOCAL CODEC/FILE/MOCK CONTROLS ONLY; no browser, source test, compilation or remote action.')
