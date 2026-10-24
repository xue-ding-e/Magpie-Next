"""One-shot validation of the exact budget patch; never a production runner."""

import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import signal
import socket
import stat
import subprocess
import tempfile
import time

BASE = "5f2981588de38cb7e828c6d0b82f3d7277203692"
REPO = "xue-ding-e/Magpie-Next"
REF = "refs/heads/validation/budget-window-20261024"
PACKAGE = "github.com/yetone/magpie/internal/gateway"
TEST = "TestKeyLimitWindowsThatStartTogetherCountACallOnce"
NEIGHBORS = ["TestKeyLimitSeveralLimits", "TestKeyLimitReservationsStayInTheirWindow",
             "TestKeyLimitHoldsParallelRequests"]
FILES = {
    "internal/budget/budget.go": "e3f9386f1ff042eaa294885dcfb6c29ef69f6e578635476b4534a6aadfcd53e9",
    "internal/gateway/key_limit_test.go": "043aaca664409d1a702c0c8ba6b99a72938d9f1ed1166e470822028f83e32b3c",
    "go.mod": "0c0311c407e44af64cae000bfdc263eddd70f9409f5ef8afcaf06ec44d64f2a3",
    "go.sum": "ab546982274c64a9217affdea602f176d66d3202a7c7d54a99430ccdb0839254",
}
CHANGED = {"internal/budget/budget.go", "internal/gateway/key_limit_test.go",
           ".github/workflows/test.yml", "build/budget_window_validation.py"}
BASE_BUDGET_HASH = "73ee0ed353370587b35490eabbe6d3403df377ed7ca28389430d35e3dc330b85"
NEW_CASES = {TEST + "/" + day + "/" + mode
             for day in ("monday", "the_1st", "monday_the_1st")
             for mode in ("tokens", "cost")}
RESERVATION_CASES = {NEIGHBORS[1] + "/" + period + "/" + mode
                     for period in ("day", "week", "month", "days", "hours")
                     for mode in ("tokens", "cost")}
CANCEL_SIGNAL = 0
CLEANUP_SAFE = True


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def git(*args):
    return subprocess.check_output(["git", *args], timeout=10).decode().strip()


def verify_files():
    actual = {name: digest(Path(name)) for name in FILES}
    require(actual == FILES, "candidate or frozen dependency hash changed")
    require(not git("status", "--porcelain=v1"), "validation worktree is not clean")
    return actual


def validate_events(path, red):
    events = [json.loads(line) for line in path.read_text().splitlines()
              if line.startswith("{")]
    require(events and all(e.get("Package") == PACKAGE for e in events),
            "missing or unexpected package test events")
    terminals = [e for e in events if e.get("Test") and
                 e.get("Action") in {"pass", "fail", "skip"}]
    expected = {TEST, *NEW_CASES} if red else {
        TEST, *NEW_CASES, *NEIGHBORS, *RESERVATION_CASES}
    require(len(terminals) == len(expected) and
            {e["Test"] for e in terminals} == expected,
            "missing, duplicate, skipped, or unexpected tests")
    action = "fail" if red else "pass"
    require(all(e["Action"] == action for e in terminals), "unexpected test outcome")
    package_ends = [e["Action"] for e in events if not e.get("Test") and
                    e.get("Action") in {"pass", "fail", "skip"}]
    require(package_ends == [action], "missing or unexpected package result")
    if red:
        for case in NEW_CASES:
            output = "".join(e.get("Output", "") for e in events if e.get("Test") == case)
            message = ("call 3 refused with 200 of 400 used; it counts 400"
                       if "/monday_the_1st/" in case else
                       "call 4 refused with 300 of 400 used; it counts 500")
            require(message in output, "RED is not the repeated-count behavior: " + case)
    return {"top_level": 1 if red else 4, "subtests": 6 if red else 16,
            "result": "behavioral_RED" if red else "GREEN"}


def peek_exit(process):
    # WNOWAIT is essential: the unreaped leader owns this PID/PGID until
    # group cleanup finishes. Never call poll()/wait() before that point.
    return os.waitid(os.P_PID, process.pid, os.WEXITED | os.WNOHANG | os.WNOWAIT)


def live_group_members(pgid):
    live = []
    for entry in Path("/proc").iterdir():
        if not entry.name.isdecimal():
            continue
        try:
            # A comm may contain spaces and parentheses; fields start after
            # its last ')'. Z/X members cannot keep writing test files.
            fields = (entry / "stat").read_text().rsplit(")", 1)[1].split()
        except FileNotFoundError:
            continue
        if int(fields[2]) == pgid and fields[0] not in {"Z", "X"}:
            live.append(int(entry.name))
    return live


def stop_group(process):
    # ChildProcessError means the ownership proof is gone. Fail without
    # signaling a possibly reused identifier or deleting the private tree.
    require(process.returncode is None, "leader was reaped before group cleanup")
    peek_exit(process)
    for sig, grace in ((signal.SIGTERM, 2), (signal.SIGKILL, 5)):
        try:
            os.killpg(process.pid, sig)
        except ProcessLookupError:
            pass
        until = time.monotonic() + grace
        clear_checks = 0
        while time.monotonic() < until:
            ended = peek_exit(process)
            if ended is not None and not live_group_members(process.pid):
                if sig == signal.SIGTERM:
                    break  # Always perform the final group KILL while held.
                clear_checks += 1
                if clear_checks == 2:
                    # Only now may waitpid release the leader's PID/PGID.
                    process.wait(timeout=1)
                    return
            else:
                clear_checks = 0
            time.sleep(0.05)
    raise RuntimeError("process group termination unconfirmed; retain private tree")


def run(command, env, log, deadline, maximum):
    global CLEANUP_SAFE
    started = time.monotonic()
    end = min(deadline, started + maximum)
    require(end > started, "supervisor deadline exhausted")
    require(not CANCEL_SIGNAL, "supervisor cancellation requested")
    metadata = {"command": command, "stage_limit_seconds": maximum,
                "remaining_total_seconds": end - started}
    with log.open("wb") as out:
        process = subprocess.Popen(command, env=env, stdout=out,
                                   stderr=subprocess.STDOUT, start_new_session=True)
        try:
            while (ended := peek_exit(process)) is None:
                require(not CANCEL_SIGNAL, "supervisor cancellation requested")
                require(time.monotonic() < end, "stage deadline exceeded: " + log.name)
                require(log.stat().st_size <= 64 * 1024 * 1024, "stage log exceeded 64 MiB")
                time.sleep(0.2)
            require(not CANCEL_SIGNAL, "supervisor cancellation requested")
            require(log.stat().st_size <= 64 * 1024 * 1024, "stage log exceeded 64 MiB")
            return ended.si_status if ended.si_code == os.CLD_EXITED else -ended.si_status
        finally:
            try:
                try:
                    stop_group(process)
                    metadata["group_cleanup"] = "confirmed before leader reap"
                except Exception:
                    CLEANUP_SAFE = False
                    metadata["group_cleanup"] = "unconfirmed; retain private tree"
                    raise
                # Descendants may have written after the leader exited.
                require(log.stat().st_size <= 64 * 1024 * 1024, "stage log exceeded 64 MiB")
            finally:
                metadata.update(exit_code=process.returncode,
                                elapsed_seconds=time.monotonic() - started)
                log.with_suffix(log.suffix + ".metadata.json").write_text(
                    json.dumps(metadata, indent=2) + "\n")


def remove_owned(path, identity, parent):
    current = path.lstat()
    require(path.parent == parent and path.name.startswith("budget-window-owned-") and
            stat.S_ISDIR(current.st_mode) and not path.is_symlink() and
            (current.st_dev, current.st_ino) == identity, "cleanup identity mismatch")
    # Read-only module directories belong only to this private tree. Never
    # touch a shared cache or follow directory symlinks while cleaning it.
    for folder, dirs, _ in os.walk(path, followlinks=False):
        os.chmod(folder, 0o700)
        for name in dirs:
            child = Path(folder) / name
            if not child.is_symlink():
                os.chmod(child, 0o700)
    shutil.rmtree(path)


def interrupted(signum, _frame):
    # Do not interrupt TERM/wait/KILL or private-tree cleanup with another
    # exception. The supervised loop observes this flag and always stops
    # the process group before the outer finally can remove its files.
    global CANCEL_SIGNAL
    CANCEL_SIGNAL = signum


def main():
    expected = os.environ.get("EXPECTED_SHA", "")
    require(re.fullmatch("[0-9a-f]{40}", expected), "expected_sha must be a full SHA")
    require(os.environ.get("GITHUB_REPOSITORY") == REPO and
            os.environ.get("GITHUB_REF") == REF and
            os.environ.get("GITHUB_EVENT_NAME") == "workflow_dispatch" and
            os.environ.get("GITHUB_SHA") == expected and
            os.environ.get("GITHUB_WORKFLOW_SHA") == expected,
            "not the reviewed repository, validation ref, or workflow SHA")
    require(git("rev-parse", "HEAD") == expected, "checkout SHA mismatch")
    require(git("rev-list", "--parents", "-n", "1", "HEAD").split() == [expected, BASE],
            "validation commit must have exactly the fixed base as its sole parent")
    require(set(git("diff", "--name-only", BASE, "HEAD").splitlines()) == CHANGED,
            "validation diff is not exactly the four reviewed files")
    before = verify_files()
    runner_temp = Path(os.environ["RUNNER_TEMP"]).resolve(strict=True)
    evidence = runner_temp / "budget-window-evidence"
    evidence.mkdir(exist_ok=False)
    summary = {"head": expected, "base_commit": BASE, "candidate_hashes": before,
               "full_suite": "NOT_RUN", "real_gateway_key": "NOT_TRIED",
               "vet": "NOT_RUN", "race": "NOT_RUN", "cross_platform": "NOT_RUN",
               "toolchain": "go1.26.10 linux/amd64", "supervisor_limit_seconds": 570,
               "network": "online setup; synthetic loopback fixtures, not a process-level network sandbox"}
    owned = None
    deadline = time.monotonic() + 570
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    try:
        go = shutil.which("go")
        require(go is not None, "Go toolchain missing")
        require(shutil.disk_usage(runner_temp).free >= 8 * 1024 ** 3, "less than 8 GiB free disk")
        mem = re.search(r"^MemAvailable:\s+(\d+) kB$", Path("/proc/meminfo").read_text(), re.M)
        require(mem and int(mem.group(1)) >= 4 * 1024 ** 2, "less than 4 GiB available memory")
        owned = Path(tempfile.mkdtemp(prefix="budget-window-owned-", dir=runner_temp))
        st = owned.stat()
        identity = (st.st_dev, st.st_ino)
        for name in ("home", "tmp", "gotmp", "gopath", "modcache", "gocache"):
            (owned / name).mkdir()
        # Check both capabilities before dependency acquisition or compilation.
        with socket.socket(socket.AF_INET) as tcp, socket.socket(socket.AF_UNIX) as unix:
            tcp.bind(("127.0.0.1", 0))
            tcp.listen(1)
            unix.bind(str(owned / "socket-probe"))
        home = owned / "home"
        # Do not pass GitHub runtime tokens, caller credentials, agent config
        # variables, or a caller-supplied stand-in path into Go/test processes.
        env = {"PATH": os.environ["PATH"], "LANG": "C.UTF-8", "TZ": "UTC",
               "HOME": str(home), "USERPROFILE": str(home),
               "XDG_CONFIG_HOME": str(home / ".config"), "XDG_CACHE_HOME": str(home / ".cache"),
               "XDG_DATA_HOME": str(home / ".local/share"), "APPDATA": str(home / "AppData/Roaming"),
               "LOCALAPPDATA": str(home / "AppData/Local"),
               "GOPATH": str(owned / "gopath"), "GOMODCACHE": str(owned / "modcache"),
               "GOCACHE": str(owned / "gocache"), "GOTMPDIR": str(owned / "gotmp"),
               "TMPDIR": str(owned / "tmp"), "TMP": str(owned / "tmp"), "TEMP": str(owned / "tmp"),
               "SQLITE_TMPDIR": str(owned / "tmp"), "GOENV": "off", "GOWORK": "off",
               "GOTOOLCHAIN": "local", "GOFLAGS": "-mod=readonly", "CGO_ENABLED": "0",
               "GOMAXPROCS": "2", "GOMEMLIMIT": "2GiB",
               "GOPROXY": "https://proxy.golang.org", "GOSUMDB": "sum.golang.org"}
        version_log = evidence / "toolchain.log"
        require(run([go, "version"], env, version_log, deadline, 10) == 0 and
                version_log.read_text().strip() == "go version go1.26.10 linux/amd64",
                "wrong toolchain")
        # Only resolve this test package's pinned dependencies. No go get/tidy,
        # direct VCS fallback, dependency updates, or package-wide test run.
        require(run([go, "list", "-deps", "-test", "./internal/gateway"], env,
                    evidence / "dependency-setup.log", deadline, 150) == 0, "dependency setup failed")
        require(run([go, "mod", "verify"], env, evidence / "module-verify.log", deadline, 30) == 0,
                "module verification failed")
        verify_files()
        env.update(GOPROXY="off", GOSUMDB="off")
        base_source = owned / "base-budget.go"
        base_source.write_bytes(subprocess.check_output(
            ["git", "show", BASE + ":internal/budget/budget.go"], timeout=10))
        require(digest(base_source) == BASE_BUDGET_HASH, "base source hash mismatch")
        overlay = owned / "base-overlay.json"
        overlay.write_text(json.dumps({"Replace": {
            str(Path("internal/budget/budget.go").resolve()): str(base_source)}}))
        common = [go, "test", "-p=1", "-vet=off", "-count=1", "-json"]
        red_log = evidence / "base-red.jsonl"
        rc = run(common + ["-overlay=" + str(overlay), "-timeout=60s", "-run", "^" + TEST + "$",
                           "./internal/gateway"], env, red_log, deadline, 330)
        require(rc == 1, "base must fail behaviorally, not pass or terminate")
        summary["base"] = validate_events(red_log, True)
        green_log = evidence / "candidate-green.jsonl"
        rc = run(common + ["-timeout=90s", "-run", "^(" + "|".join([TEST, *NEIGHBORS]) + ")$",
                           "./internal/gateway"], env, green_log, deadline, 180)
        require(rc == 0, "candidate tests failed")
        summary["candidate"] = validate_events(green_log, False)
        summary["result"] = "PASS"
    except Exception as error:
        summary.update(result="FAIL", error=str(error))
    finally:
        try:
            summary["final_hashes"] = verify_files()
        except Exception as error:
            summary.update(result="FAIL", hash_error=str(error))
        if owned is not None:
            try:
                if CLEANUP_SAFE:
                    remove_owned(owned, identity, runner_temp)
                    summary["cleanup"] = "removed owned temporary tree"
                else:
                    summary.update(result="FAIL", cleanup="retained: group termination unconfirmed")
            except Exception as error:
                summary.update(result="FAIL", cleanup_error=str(error))
        if CANCEL_SIGNAL:
            summary.update(result="FAIL", cancellation_signal=CANCEL_SIGNAL)
        summary["logs_sha256"] = {p.name: digest(p) for p in evidence.iterdir() if p.is_file()}
        (evidence / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
        print(json.dumps(summary, indent=2), flush=True)
    return 0 if summary["result"] == "PASS" and not CANCEL_SIGNAL else 1


if __name__ == "__main__":
    raise SystemExit(main())
