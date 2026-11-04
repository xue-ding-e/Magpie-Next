"""Verify tracked controls first, then restore one immutable derived ledger."""
import base64
import hashlib
import json
import os
import re
import stat
import subprocess
import sys
import tempfile
import zlib
from pathlib import Path, PurePosixPath

CONTROL = Path(__file__).resolve().parent
ROOT = CONTROL.parents[1]
PREFIX = '.github/gui-batch005/'
OUTPUT = 'prior-ledger.json'
SIZE = 835286
SHA256 = '09f5b0c0b824b8fac2b8fcaa21bc022172955d456bf622bc1bbe3c1aee3a666c'
GIT_BLOB = 'c7eb5b5acd5436c2f1e10958c4503582c66d4c1f'
PRODUCT = 'ff997c492a908293dc0de152ee20f83152b88aa9'
EXPECTED_META = {
    'version': 1, 'encoding': 'gzip+base64', 'output': OUTPUT,
    'output_size': SIZE, 'output_sha256': SHA256, 'output_git_blob': GIT_BLOB,
    'gzip_mtime': 0, 'gzip_size': 54309,
    'gzip_sha256': 'a20914c6084c1c606108f458cb26cb5025aaa96734759b90f5905160359cd180',
    'base64_size': 72412,
    'base64_sha256': '42a2ec0197f35bdd31b4f8322a40d8cd77805b4e32e0bb27e50d53c4abe3636b',
    'part_size_limit': 32768,
    'parts': [
        {'path': 'ledger-parts/part-000.b64', 'size': 32768, 'sha256': '1893d857389febd2285ae739936d3736c3d66b6c807f5c6bdc5233eb0a0b3805'},
        {'path': 'ledger-parts/part-001.b64', 'size': 32768, 'sha256': '343e5c10b83145530bd46845fc88fa09ac30227cb1eb77b67fd1a8d5c70a86cc'},
        {'path': 'ledger-parts/part-002.b64', 'size': 6876, 'sha256': 'ebec79c4b27602b91033c8a8b2a080ac64e5b0643e489a6a17a72d571efc0796'},
    ],
}


def strict_json(raw):
    def unique(pairs):
        value = {}
        for key, item in pairs:
            assert key not in value, 'Duplicate JSON key'
            value[key] = item
        return value
    return json.loads(raw, object_pairs_hook=unique)


def typed_equal(actual, expected):
    assert type(actual) is type(expected), 'Metadata type mismatch'
    if isinstance(expected, dict):
        assert set(actual) == set(expected), 'Missing or unknown metadata field'
        for key in expected:
            typed_equal(actual[key], expected[key])
    elif isinstance(expected, list):
        assert len(actual) == len(expected), 'Missing, duplicate or extra part'
        for value, target in zip(actual, expected):
            typed_equal(value, target)
    else:
        assert actual == expected, 'Immutable metadata mismatch'


def safe_relative(value):
    assert isinstance(value, str) and value and '\\' not in value
    assert not any(ord(ch) < 32 for ch in value)
    path = PurePosixPath(value)
    assert not path.is_absolute() and all(p not in ('', '.', '..') for p in value.split('/'))
    assert path.as_posix() == value
    return path


def regular_read(root, relative, limit):
    relative = safe_relative(relative)
    # Resolve every component via directory descriptors without following
    # symlinks. O_NONBLOCK also prevents a malicious FIFO from hanging preflight.
    directory = os.open(root, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
    descriptor = None
    try:
        for component in relative.parts[:-1]:
            next_directory = os.open(component, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=directory)
            os.close(directory)
            directory = next_directory
        descriptor = os.open(relative.parts[-1], os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK, dir_fd=directory)
        before = os.fstat(descriptor)
        assert stat.S_ISREG(before.st_mode) and before.st_nlink == 1, 'Non-regular file or hard link'
        assert 0 <= before.st_size <= limit, 'File size limit'
        chunks, total = [], 0
        while True:
            chunk = os.read(descriptor, min(65536, limit + 1 - total))
            if not chunk:
                break
            chunks.append(chunk)
            total += len(chunk)
            assert total <= limit, 'File grew past limit'
        after = os.fstat(descriptor)
        assert (before.st_size, before.st_mtime_ns, before.st_ctime_ns) == (after.st_size, after.st_mtime_ns, after.st_ctime_ns), 'File changed while reading'
        data = b''.join(chunks)
        assert len(data) == before.st_size, 'File size changed while reading'
        return data
    finally:
        if descriptor is not None:
            os.close(descriptor)
        os.close(directory)


def verify_bytes(raw):
    assert len(raw) == SIZE, 'Decoded ledger size mismatch'
    assert hashlib.sha256(raw).hexdigest() == SHA256, 'Decoded ledger SHA256 mismatch'
    blob = b'blob ' + str(len(raw)).encode('ascii') + b'\0' + raw
    assert hashlib.sha1(blob).hexdigest() == GIT_BLOB, 'Decoded Git blob mismatch'
    return raw


def inflate_one(compressed):
    # A max_length of SIZE+1 detects bombs without allocating unbounded output.
    # Do not use gzip.decompress: it accepts concatenated members/trailing bytes.
    assert len(compressed) <= 100 * 1024, 'Compressed input limit'
    decoder = zlib.decompressobj(16 + zlib.MAX_WBITS)
    raw = decoder.decompress(compressed, SIZE + 1)
    assert len(raw) <= SIZE, 'Decoded size limit exceeded'
    assert decoder.eof, 'Truncated or nonterminal gzip stream'
    assert not decoder.unconsumed_tail and not decoder.unused_data, 'Extra gzip member or trailing bytes'
    return verify_bytes(raw)


def load_bundle(control=CONTROL):
    meta = strict_json(regular_read(control, 'ledger-encoding.json', 8192))
    typed_equal(meta, EXPECTED_META)
    directory = control / 'ledger-parts'
    assert not directory.is_symlink() and directory.is_dir()
    assert {p.name for p in directory.iterdir()} == {Path(p['path']).name for p in meta['parts']}, 'Unexpected or missing chunk'
    chunks = []
    for item in meta['parts']:
        chunk = regular_read(control, item['path'], 32768)
        assert len(chunk) == item['size']
        assert hashlib.sha256(chunk).hexdigest() == item['sha256']
        chunks.append(chunk)
    encoded = b''.join(chunks)
    assert len(encoded) == meta['base64_size']
    assert hashlib.sha256(encoded).hexdigest() == meta['base64_sha256']
    assert re.fullmatch(rb'[A-Za-z0-9+/]*={0,2}', encoded), 'Noncanonical base64 characters'
    compressed = base64.b64decode(encoded, validate=True)
    assert base64.b64encode(compressed) == encoded, 'Noncanonical base64 padding'
    assert len(compressed) == meta['gzip_size']
    assert hashlib.sha256(compressed).hexdigest() == meta['gzip_sha256']
    assert compressed[:10] == b'\x1f\x8b\x08\x00\x00\x00\x00\x00\x02\xff', 'Non-deterministic gzip header'
    return inflate_one(compressed)


def verify_existing(control=CONTROL):
    return verify_bytes(regular_read(control, OUTPUT, SIZE))


def materialize(raw, control=CONTROL):
    # Output is a code constant, never a path supplied by compressed content.
    verify_bytes(raw)
    target = control / OUTPUT
    try:
        target.lstat()
    except FileNotFoundError:
        pass
    else:
        verify_existing(control)
        return False
    assert control.is_dir() and not control.is_symlink()
    fd, temporary = tempfile.mkstemp(prefix='.ledger-restore-', dir=control)
    try:
        with os.fdopen(fd, 'wb') as stream:
            stream.write(raw)
            stream.flush()
            os.fsync(stream.fileno())
        # Atomic, exclusive installation. An unexpected existing target is
        # never overwritten, including a symlink created during a race.
        try:
            os.link(temporary, target)
        except FileExistsError:
            pass
    finally:
        os.unlink(temporary)
    verify_existing(control)
    return True


def git(*args):
    return subprocess.check_output(['git', *args], cwd=ROOT, text=True).strip()


def validate_tracked():
    assert os.environ['GITHUB_REPOSITORY'] == 'xue-ding-e/Magpie-Next'
    assert os.environ['GITHUB_EVENT_NAME'] == 'push'
    assert os.environ['GITHUB_REF'] == 'refs/heads/validation/auth-gui-batch005-20261103'
    manifest_raw = regular_read(CONTROL, 'control-hashes.json', 128 * 1024)
    controls = strict_json(manifest_raw)
    assert isinstance(controls, dict) and controls
    assert PREFIX + OUTPUT not in controls, 'Derived ledger must never be a staged payload'
    for name, checksum in controls.items():
        safe_relative(name)
        assert name.startswith(PREFIX) and isinstance(checksum, str) and re.fullmatch('[0-9a-f]{64}', checksum)
    allowed = set(controls) | {PREFIX + 'control-hashes.json'}
    expected_dirs = {str(parent) for name in allowed for parent in PurePosixPath(name).parents if str(parent).startswith(PREFIX.rstrip('/'))}
    actual = set()
    for directory, dirs, files in os.walk(CONTROL, followlinks=False):
        directory = Path(directory)
        if directory == CONTROL and 'node_modules' in dirs:
            assert not (directory / 'node_modules').is_symlink()
            dirs.remove('node_modules')
        for name in dirs:
            assert not (directory / name).is_symlink(), 'Symlink directory in controls'
            assert str((directory / name).relative_to(ROOT)) in expected_dirs, 'Unexpected directory in controls'
        for name in files:
            path = directory / name
            assert not path.is_symlink(), 'Symlink file in controls'
            actual.add(str(path.relative_to(ROOT)))
    assert actual <= allowed | {PREFIX + OUTPUT}, 'Unknown control or derived file'
    assert allowed <= actual, 'Missing staged control file'
    for name, checksum in controls.items():
        data = regular_read(ROOT, name, 1024 * 1024)
        assert hashlib.sha256(data).hexdigest() == checksum, name
    # Bind the hash manifest itself to the checked-out commit, in addition to
    # the workflow's fixed manifest/bootstrap hashes. A derived untracked output
    # is allowed, but can never silently become a tracked product/control file.
    assert git('rev-parse', 'HEAD') == os.environ['GITHUB_SHA']
    assert git('rev-parse', 'HEAD^') == PRODUCT
    assert len(git('rev-list', '--parents', '-n', '1', 'HEAD').split()) == 2
    expected = allowed | {'.github/workflows/gui-batch005.yml'}
    assert set(git('diff', '--name-only', 'HEAD^', 'HEAD').splitlines()) == expected
    assert set(git('ls-files', '--', PREFIX).splitlines()) == allowed
    assert not git('diff', '--name-only', 'HEAD', '--', PREFIX, '.github/workflows/gui-batch005.yml')
    source = strict_json(regular_read(CONTROL, 'source.json', 16384))
    assert source['source'] == PRODUCT
    return controls


def restore():
    controls = validate_tracked()
    raw = load_bundle()
    materialize(raw)
    verify_existing()
    return controls


if __name__ == '__main__':
    assert not sys.argv[1:], 'No alternate bootstrap modes are supported'
    restore()
    print('Immutable derived prior-ledger restored/verified after tracked hashes and exact single-parent Git gate; no coverage or budget change.')
