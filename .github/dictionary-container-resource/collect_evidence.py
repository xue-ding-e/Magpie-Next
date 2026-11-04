"""Bounded, dirfd-anchored, allowlist-only synthetic evidence collection.

One upload directory avoids the runner's multiline INPUT_PATH prefix bug.
Never follow substituted parent directories, upload caches or replace a prior
stage failure. A killed/incomplete collector never creates an acceptance claim.
"""
from contextlib import ExitStack
import hashlib
import json
import os
from pathlib import Path
import signal
import stat
import sys
import time

PHASES = ('native', 'go-env', 'compile', '100k', '16mib', 'held', 'released')
REPORTS = 'dictionary-container-resource-reports'
BUNDLE = 'dictionary-container-resource-evidence'
MAX_FILE = 16 << 20
MAX_TOTAL = 64 << 20
DIR_FLAGS = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW | os.O_CLOEXEC


def names():
    required = {'dictionary-container-env-proof.json', 'dictionary-container-budget.json'}
    report_required = {'native-cases.json', 'native-complete.json', 'home-cleanup.json',
        'workload-complete.json', 'acceptance.json', 'workload-final-memory.json', 'acceptance-final-memory.json'}
    optional = {'workload-final-rejected.json', 'acceptance-final-rejected.json'}
    for phase in PHASES:
        report_required.update({phase + '.log', phase + '-owned.json', phase + '-before-memory.json', phase + '-after-memory.json'})
        optional.update({phase + '-exception.json', phase + '-before-rejected.json', phase + '-after-rejected.json'})
    required.update(REPORTS + '/' + name for name in report_required)
    return required, required | {REPORTS + '/' + name for name in optional}


def check_time(deadline):
    if time.monotonic() >= deadline:
        raise TimeoutError('synthetic evidence collection soft deadline')


def absolute_directory(path):
    """Walk every component without symlinks; caller owns the returned fd."""
    path = Path(path)
    if not path.is_absolute() or '..' in path.parts:
        raise RuntimeError('evidence path must be absolute without traversal')
    descriptor = os.open('/', DIR_FLAGS)
    try:
        for part in path.parts[1:]:
            next_descriptor = os.open(part, DIR_FLAGS, dir_fd=descriptor)
            os.close(descriptor)
            descriptor = next_descriptor
        return descriptor
    except BaseException:
        os.close(descriptor)
        raise


def directory(descriptor):
    meta = os.fstat(descriptor)
    if not stat.S_ISDIR(meta.st_mode) or meta.st_uid != os.geteuid():
        raise RuntimeError('evidence directory not an owned ordinary directory')


def empty_directory(descriptor):
    directory(descriptor)
    if os.listdir(descriptor):
        raise RuntimeError('new evidence directory was not empty at capture')


def identity(metadata):
    return metadata.st_dev, metadata.st_ino, stat.S_IFMT(metadata.st_mode), metadata.st_uid


def same_child(parent, name, descriptor):
    if identity(os.stat(name, dir_fd=parent, follow_symlinks=False)) != identity(os.fstat(descriptor)):
        raise RuntimeError('evidence directory identity changed: ' + name)


def same_path(path, descriptor):
    current = absolute_directory(path)
    try:
        if identity(os.fstat(current)) != identity(os.fstat(descriptor)):
            raise RuntimeError('evidence root identity changed')
    finally:
        os.close(current)


def atomic_json(descriptor, name, value):
    temporary = '.' + name + '.pending'
    created = False
    payload = (json.dumps(value, indent=2) + '\n').encode('utf-8')
    try:
        fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600, dir_fd=descriptor)
        created = True
        with os.fdopen(fd, 'wb') as stream:
            stream.write(payload)
        os.link(temporary, name, src_dir_fd=descriptor, dst_dir_fd=descriptor, follow_symlinks=False)
    finally:
        if created:
            os.unlink(temporary, dir_fd=descriptor)
    return {'path': name, 'bytes': len(payload), 'sha256': hashlib.sha256(payload).hexdigest()}


def inventory(destination, expected, reports_created, deadline):
    """Audit exactly what directory upload will see, not just what we copied."""
    by_name = {entry['path']: entry for entry in expected}
    if len(by_name) != len(expected):
        raise RuntimeError('duplicate expected evidence inventory')
    root_names = {name for name in by_name if '/' not in name}
    report_names = {name.split('/')[1] for name in by_name if name.startswith(REPORTS + '/')}
    if reports_created:
        root_names.add(REPORTS)
    elif report_names:
        raise RuntimeError('unanchored report evidence in inventory')
    check_time(deadline)
    if set(os.listdir(destination)) != root_names:
        raise RuntimeError('unexpected or missing bundle inventory entry')
    with ExitStack() as descriptors:
        report_fd = None
        if reports_created:
            report_fd = os.open(REPORTS, DIR_FLAGS, dir_fd=destination)
            descriptors.callback(os.close, report_fd)
            directory(report_fd)
            if set(os.listdir(report_fd)) != report_names:
                raise RuntimeError('unexpected or missing reports inventory entry')
        for relative, expected_file in sorted(by_name.items()):
            check_time(deadline)
            parent = report_fd if relative.startswith(REPORTS + '/') else destination
            name = relative.split('/')[-1]
            meta = os.stat(name, dir_fd=parent, follow_symlinks=False)
            if not stat.S_ISREG(meta.st_mode) or meta.st_uid != os.geteuid() or meta.st_nlink != 1 or meta.st_size != expected_file['bytes']:
                raise RuntimeError('invalid evidence inventory file: ' + relative)
            descriptor = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=parent)
            digest = hashlib.sha256()
            size = 0
            with os.fdopen(descriptor, 'rb') as stream:
                before = os.fstat(stream.fileno())
                if (identity(before), before.st_size, before.st_mtime_ns, before.st_nlink) != \
                   (identity(meta), meta.st_size, meta.st_mtime_ns, 1):
                    raise RuntimeError('inventory file changed before reading: ' + relative)
                while True:
                    check_time(deadline)
                    chunk = stream.read(65536)
                    if not chunk:
                        break
                    size += len(chunk)
                    if size > expected_file['bytes']:
                        raise RuntimeError('inventory file grew: ' + relative)
                    digest.update(chunk)
                after = os.fstat(stream.fileno())
            current = os.stat(name, dir_fd=parent, follow_symlinks=False)
            if any((identity(item), item.st_size, item.st_mtime_ns, item.st_nlink) !=
                   (identity(meta), meta.st_size, meta.st_mtime_ns, 1) for item in (after, current)) or \
               size != expected_file['bytes'] or digest.hexdigest() != expected_file['sha256']:
                raise RuntimeError('inventory content or identity changed: ' + relative)
        check_time(deadline)
        if set(os.listdir(destination)) != root_names or \
           (report_fd is not None and set(os.listdir(report_fd)) != report_names):
            raise RuntimeError('bundle inventory changed during verification')
        if report_fd is not None:
            same_child(destination, REPORTS, report_fd)


def bundle_fds(root, destination, deadline, check_destination):
    required, allowed = names()
    found, errors = [], []
    copied = 0
    source_reports = None
    output_reports = None
    output_reports_attempted = False
    safe_path = True
    with ExitStack() as descriptors:
        try:
            check_destination()
            try:
                source_reports = os.open(REPORTS, DIR_FLAGS, dir_fd=root)
            except FileNotFoundError:
                pass
            if source_reports is not None:
                descriptors.callback(os.close, source_reports)
                directory(source_reports)
                for name in os.listdir(source_reports):
                    check_time(deadline)
                    if REPORTS + '/' + name not in allowed:
                        errors.append('unexpected report entry refused: ' + name)
                output_reports_attempted = True
                os.mkdir(REPORTS, mode=0o700, dir_fd=destination)
                output_reports = os.open(REPORTS, DIR_FLAGS, dir_fd=destination)
                descriptors.callback(os.close, output_reports)
                empty_directory(output_reports)

            def check_directories():
                check_destination()
                if source_reports is not None:
                    same_child(root, REPORTS, source_reports)
                    same_child(destination, REPORTS, output_reports)

            for relative in sorted(allowed):
                check_time(deadline)
                check_directories()
                is_report = relative.startswith(REPORTS + '/')
                parent = source_reports if is_report else root
                target_parent = output_reports if is_report else destination
                if parent is None:
                    continue
                name = relative.split('/')[-1]
                try:
                    metadata = os.stat(name, dir_fd=parent, follow_symlinks=False)
                except FileNotFoundError:
                    continue
                if not stat.S_ISREG(metadata.st_mode) or metadata.st_nlink != 1 or metadata.st_uid != os.geteuid():
                    errors.append('nonregular, linked or nonowned evidence refused: ' + relative)
                    continue
                if metadata.st_size > MAX_FILE or copied + metadata.st_size > MAX_TOTAL:
                    errors.append('oversized evidence refused: ' + relative)
                    continue
                temporary = '.' + name + '.pending'
                created = False
                digest = hashlib.sha256()
                size = 0
                try:
                    descriptor = os.open(name, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC, dir_fd=parent)
                    with os.fdopen(descriptor, 'rb') as reader:
                        actual = os.fstat(reader.fileno())
                        if (identity(actual), actual.st_size, actual.st_mtime_ns, actual.st_nlink) != \
                           (identity(metadata), metadata.st_size, metadata.st_mtime_ns, 1):
                            raise RuntimeError('evidence changed before copy: ' + relative)
                        output = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW,
                            0o600, dir_fd=target_parent)
                        created = True
                        with os.fdopen(output, 'wb') as writer:
                            while True:
                                check_time(deadline)
                                chunk = reader.read(65536)
                                if not chunk:
                                    break
                                size += len(chunk)
                                if size > metadata.st_size or size > MAX_FILE or copied + size > MAX_TOTAL:
                                    raise RuntimeError('growing evidence refused: ' + relative)
                                digest.update(chunk)
                                writer.write(chunk)
                        after = os.fstat(reader.fileno())
                        if size != metadata.st_size or (after.st_size, after.st_mtime_ns) != (metadata.st_size, metadata.st_mtime_ns):
                            raise RuntimeError('evidence changed during copy: ' + relative)
                    check_directories()
                    os.link(temporary, name, src_dir_fd=target_parent, dst_dir_fd=target_parent, follow_symlinks=False)
                    copied += size
                    found.append({'path': relative, 'bytes': size, 'sha256': digest.hexdigest()})
                finally:
                    if created:
                        os.unlink(temporary, dir_fd=target_parent)
            check_directories()
        except (OSError, RuntimeError, TimeoutError) as error:
            errors.append(type(error).__name__ + ': ' + str(error))
        # Revalidate every named directory even after copy failed. Directory
        # substitution suppresses the upload path, not just the success flag.
        try:
            check_destination()
            if output_reports_attempted and output_reports is None:
                raise RuntimeError('output reports directory was never anchored')
            if source_reports is not None:
                same_child(root, REPORTS, source_reports)
            if output_reports is not None:
                same_child(destination, REPORTS, output_reports)
        except (OSError, RuntimeError) as error:
            safe_path = False
            errors.append('unsafe upload path: ' + str(error))
    present = {entry['path'] for entry in found}
    return {'schema': 1, 'files': found, 'bytes': copied, 'errors': errors,
        'missing_required_for_success': sorted(required - present),
        'absent_optional_diagnostics': sorted((allowed - required) - present),
        'complete_for_success': not errors and required <= present,
        'safe_upload_path': safe_path,
        'reports_directory_created': output_reports is not None,
        'scope': 'exact synthetic evidence allowlist only; not product acceptance',
        'no_retry_or_gate_relaxation': True}


def bundle(root, destination, deadline):
    # Pure local self-check entry; real CI holds these same fds through setup,
    # index publication and output publication rather than reopening by path.
    if Path(destination) != Path(root) / BUNDLE:
        raise RuntimeError('unexpected bundle destination')
    with ExitStack() as descriptors:
        root_fd = absolute_directory(root); descriptors.callback(os.close, root_fd)
        destination_fd = os.open(BUNDLE, DIR_FLAGS, dir_fd=root_fd); descriptors.callback(os.close, destination_fd)
        directory(root_fd); empty_directory(destination_fd)
        def check_destination():
            same_path(root, root_fd)
            same_child(root_fd, BUNDLE, destination_fd)
        result = bundle_fds(root_fd, destination_fd, deadline, check_destination)
        try:
            inventory(destination_fd, result['files'], result['reports_directory_created'], deadline)
        except (OSError, RuntimeError) as error:
            result['safe_upload_path'] = result['complete_for_success'] = False
            result['errors'].append('inventory rejected: ' + str(error))
        return result


def main():
    # Eight seconds for copying; two seconds for index/path publication.
    # Hard stop means no upload path. Missing collection evidence fails closed;
    # phase diagnostics remain on stdout as the independent second channel.
    signal.signal(signal.SIGALRM, lambda *_: os._exit(124))
    signal.alarm(10)
    try:
        root = Path(os.environ['RUNNER_TEMP'])
        output = Path(os.environ['GITHUB_OUTPUT'])
        if not output.is_absolute() or output.name in ('', '.', '..'):
            raise RuntimeError('missing runner output file')
        with ExitStack() as descriptors:
            root_fd = absolute_directory(root); descriptors.callback(os.close, root_fd); directory(root_fd)
            os.mkdir(BUNDLE, mode=0o700, dir_fd=root_fd)
            destination_fd = os.open(BUNDLE, DIR_FLAGS, dir_fd=root_fd); descriptors.callback(os.close, destination_fd)
            empty_directory(destination_fd)
            def check_destination():
                same_path(root, root_fd)
                same_child(root_fd, BUNDLE, destination_fd)
            start = atomic_json(destination_fd, 'collection-start.json', {'started': True, 'complete': False,
                'hard_seconds': 10, 'allowlist_files': len(names()[1])})
            deadline = time.monotonic() + 8
            result = bundle_fds(root_fd, destination_fd, deadline, check_destination)
            try:
                inventory(destination_fd, result['files'] + [start], result['reports_directory_created'], deadline)
            except (OSError, RuntimeError) as error:
                result['safe_upload_path'] = result['complete_for_success'] = False
                result['errors'].append('inventory rejected: ' + str(error))
            index = atomic_json(destination_fd, 'collection-index.json', result)
            if result['safe_upload_path']:
                # The final index is part of the same exact inventory. This
                # second audit still uses the original soft/hard deadline.
                inventory(destination_fd, result['files'] + [start, index], result['reports_directory_created'], deadline)
                check_destination()
                output_parent = absolute_directory(output.parent); descriptors.callback(os.close, output_parent)
                descriptor = os.open(output.name, os.O_WRONLY | os.O_APPEND | os.O_NOFOLLOW, dir_fd=output_parent)
                with os.fdopen(descriptor, 'w', encoding='utf-8') as stream:
                    meta = os.fstat(stream.fileno())
                    if not stat.S_ISREG(meta.st_mode) or meta.st_uid != os.geteuid() or meta.st_nlink != 1:
                        raise RuntimeError('runner output is not an owned ordinary file')
                    print('path=' + str(root / BUNDLE), file=stream)
            print(json.dumps({'diagnostic': 'synthetic-evidence-collection', 'files': len(result['files']),
                'bytes': result['bytes'], 'complete_for_success': result['complete_for_success'],
                'safe_upload_path': result['safe_upload_path'], 'errors': result['errors'],
                'missing_required_for_success': result['missing_required_for_success']}, sort_keys=True), flush=True)
            return 0 if result['complete_for_success'] else 1
    finally:
        signal.alarm(0)


if __name__ == '__main__':
    sys.exit(main())
