import hashlib, json, os, pathlib, re, signal, subprocess, sys, time, shutil, importlib.util
ROOT = pathlib.Path.cwd()
CONTROL = ROOT / '.github/validation'
OUT = ROOT / 'validation-evidence'
MANIFEST = json.loads((CONTROL / 'magpie-lock.json').read_text(encoding='utf-8'))
CONTROLS = ['.github/workflows/test.yml', '.github/validation/magpie-lock.py', '.github/validation/magpie-lock.json', '.github/validation/magpie-lock.patch', '.github/validation/magpie-supervisor.py']

def digest(path): return hashlib.sha256(path.read_bytes()).hexdigest()
def git(*args): return subprocess.check_output(['git', *args], text=True).strip()
def write(name, value):
    OUT.mkdir(exist_ok=True)
    (OUT / name).write_text(json.dumps(value, indent=2) + '\n', encoding='utf-8')
def check_products():
    for p, h in (MANIFEST['products'] | MANIFEST['unchanged']).items():
        if digest(ROOT / p) != h: raise ValueError('source hash mismatch: ' + p)

def prepare():
    expected = os.environ['EXPECTED_SHA']
    if not re.fullmatch('[0-9a-f]{40}', expected): raise ValueError('invalid expected SHA')
    if os.environ['GITHUB_REPOSITORY'] != MANIFEST['repository']: raise ValueError('wrong repository')
    if git('rev-parse', 'HEAD') != expected or os.environ['GITHUB_SHA'] != expected: raise ValueError('wrong commit')
    if git('show', '-s', '--format=%P', 'HEAD') != MANIFEST['base']: raise ValueError('wrong parent')
    if set(git('diff', '--name-only', MANIFEST['base'], 'HEAD').splitlines()) != set(CONTROLS): raise ValueError('not control-only commit')
    if git('status', '--porcelain'): raise ValueError('dirty checkout')
    patch = CONTROL / 'magpie-lock.patch'
    if digest(patch) != MANIFEST['patch_sha256']: raise ValueError('wrong product patch')
    subprocess.run(['git', 'apply', '--check', str(patch)], check=True)
    subprocess.run(['git', 'apply', str(patch)], check=True)
    check_products()
    # Existing files plus the two deliberately new tests, no other source edits.
    changed = set(git('diff', '--name-only').splitlines()) | set(git('ls-files', '--others', '--exclude-standard').splitlines())
    if changed != set(MANIFEST['products']): raise ValueError('unexpected product changes: ' + repr(changed))
    OUT.mkdir(exist_ok=True)
    shutil.copyfile(patch, OUT/'candidate.patch')
    shutil.copyfile(CONTROL/'magpie-lock.json', OUT/'manifest.json')
    write('prepare.json', {'head': expected, 'base': MANIFEST['base'], 'manifest': MANIFEST, 'control_hashes': {p: digest(ROOT/p) for p in CONTROLS}})

def completion(events, code):
    passed = [e['Test'] for e in events if e.get('Action') == 'pass' and 'Test' in e]
    roots = [n for n in passed if '/' not in n]
    subs = [n for n in passed if '/' in n]
    fails = [e for e in events if e.get('Action') in ('fail', 'skip')]
    packages = [e['Package'] for e in events if e.get('Action') == 'pass' and 'Test' not in e]
    expected_packages = ['github.com/yetone/magpie/internal/filelock', 'github.com/yetone/magpie/internal/sessions']
    ok = code == 0 and not fails and sorted(roots) == sorted(MANIFEST['tests']) and len(subs) == 11 and sorted(subs) == sorted(MANIFEST['subtests']) and sorted(packages) == sorted(expected_packages)
    return ok, roots, subs, fails, packages

def test():
    check_products()
    names = MANIFEST['tests']
    if len(names) != 12 or len(set(names)) != 12 or 'TestLockHolder' in names: raise ValueError('bad exact selection')
    pattern = '^(' + '|'.join(re.escape(n) for n in names) + ')$'
    cmd = ['go', 'test'] + (['-race'] if sys.platform != 'win32' else []) + ['-tags', 'nogui', '-mod=readonly', '-p=1', '-count=1', '-timeout=60s', '-json', './internal/filelock', './internal/sessions', '-run', pattern]
    version = subprocess.check_output(['go', 'version'], text=True, timeout=10).strip()
    if not version.startswith('go version go1.26.10 '): raise ValueError('unexpected Go toolchain')
    OUT.mkdir(exist_ok=True)
    remaining = 480 - (time.time() - float(os.environ['VALIDATION_START']))
    if remaining < 5: raise TimeoutError('shared job verification budget exhausted')
    start = time.monotonic()
    code = None
    timed_out = False
    supervision = {}
    if sys.platform != 'win32':
        spec = importlib.util.spec_from_file_location('reviewed_supervisor', CONTROL/'magpie-supervisor.py')
        sup = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(sup)
        sup.EVIDENCE = OUT
        sup.PHASE = 'native'
        sup.JOB_DEADLINE = time.monotonic() + remaining
        for sig in (signal.SIGTERM, signal.SIGINT): signal.signal(sig, sup.interrupted)
        try:
            sup.run('tests', cmd, min(420, remaining), ROOT, dict(os.environ))
        except RuntimeError:
            if not sup.RECORDS: raise
        finally:
            for source, dest in [('native-tests.stdout.log','tests.jsonl'), ('native-tests.stderr.log','tests.stderr')]:
                if (OUT/source).exists(): shutil.copyfile(OUT/source, OUT/dest)
        supervision = sup.RECORDS[-1]
        code = supervision['exit_code']
        timed_out = supervision['reason'] == 'timeout'
    else:
        # Known selected Go tests own their explicit children (Kill + Wait).
        # Normal complete success is NOT a general Windows tree-containment proof.
        # On failure/timeout/cancel only the retained process HANDLE may be killed;
        # descendants are uncertain and the synthetic home must be preserved.
        proc = None
        with (OUT/'tests.jsonl').open('w', encoding='utf-8') as stdout, (OUT/'tests.stderr').open('w', encoding='utf-8') as stderr:
            try:
                proc = subprocess.Popen(cmd, stdout=stdout, stderr=stderr)
                try:
                    code = proc.wait(timeout=min(420, remaining))
                except subprocess.TimeoutExpired:
                    timed_out = True
                    code = 124
            finally:
                if proc is not None and proc.poll() is None:
                    proc.kill()  # Windows TerminateProcess uses Popen's retained HANDLE.
                    proc.wait(timeout=5)
        supervision = {'scope': 'known-test-owned-children-only', 'generic_tree_containment_verified': False,
                       'known_child_contract': 'TestLockAcrossProcesses explicit Kill+Wait and t.Cleanup',
                       'uncertain_on_failure': True}
    events = [json.loads(line) for line in (OUT/'tests.jsonl').read_text(encoding='utf-8').splitlines() if line.strip()]
    ok, roots, subs, fails, packages = completion(events, code)
    check_products()
    if sys.platform != 'win32': ok = ok and supervision.get('reason') == 'completed' and not supervision.get('survivors')
    write('result.json', {'command': cmd, 'version': version, 'platform': sys.platform, 'exit': code, 'timed_out': timed_out, 'elapsed': time.monotonic()-start, 'root_pass': roots, 'subcase_pass': subs, 'fail_or_skip': fails, 'package_pass': packages, 'accepted': ok, 'product_hashes': {p: digest(ROOT/p) for p in MANIFEST['products']}, 'log_sha256': digest(OUT/'tests.jsonl'), 'supervision': supervision, 'home_cleanup_permitted': ok, 'cleanup_limit': 'Windows success relies on verified selected Go/test-owned child lifecycle; any incomplete or uncertain result preserves home; not generic tree containment'})
    if not ok: raise RuntimeError('validation did not satisfy exact completion gate; preserve synthetic home')
    with open(os.environ['GITHUB_OUTPUT'], 'a', encoding='utf-8') as stream: stream.write('cleanup_permitted=true\n')

if __name__ == '__main__':
    try:
        if sys.argv[1:] == ['prepare']: prepare()
        elif sys.argv[1:] == ['test']: test()
        else: raise ValueError('expected prepare or test')
    except Exception as error:
        write('error.json', {'type': type(error).__name__, 'error': str(error)})
        raise
