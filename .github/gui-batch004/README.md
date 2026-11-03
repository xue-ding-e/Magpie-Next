# Full GUI recovery batch004: local draft

Validation controls only. No CI, browser, product compilation, product source
change or external write has been performed while preparing this draft. Only the
24 text paths explicitly listed in the generated `STAGING-PATHS.txt` are
candidates for staging; use its exact list, not a guessed glob. The parent
must review the frozen result before a separately authorized activation.

## Same source, next unseen files

Product remains `ff997c492a908293dc0de152ee20f83152b88aa9`, parent
`a27f0957427b4ba86f7c140fa38068922adcc9ea`, tree
`bc56abd9c642a1315ea3a3f32a30d9998e15137a`.

The authoritative inventory still contains827 original test files. This batch
selects the next32 prior `not_run` files after `quota-curve.test.cjs`, starting at
`quota-dial.test.cjs` and ending at `removed-hide.test.cjs`. Their exact ordered
paths, SHA256s and Git blob identities are in `inventory.json`. All32 were also
not loaded in the older incomplete full-suite attempt. No failed file is rerun,
removed or skipped as a way to obtain a green batch.

Limits and original test semantics are unchanged:

- Two independent original test processes maximum;600 seconds per file.
-1800 seconds batch-controller limit; outer TERM1860 seconds, KILL after30 seconds.
- Execution step32 minutes; whole job55 minutes; same6GiB observed process RSS,
  300MiB reports and1GiB free-disk thresholds.
- Node22.23.4 and integrity-locked README-tested Playwright1.62.1. Original
  Chromium/WebKit and language loops remain inside each unchanged test file.
- No engine/channel/name filter, skip, fixture, asset, CSS or offline-guard edit.
  The copied guard and npm manifests are byte-identical to batch003.
- Ordinary test failures continue through independent queued files. Timeout or
  resource stops retain unstarted files as `not_run`. Owned process groups and
  temporary HOME/profile directories are always cleaned, including report errors.

## Carry real current-source evidence without relabeling

Prior run41598441535 used the same exact ff997 source and frozen runtime. Its
hash-bound827-file ledger records32 genuine terminal file executions:31 passed,
one failed,795 not run. The557 test cases were555 pass and2 fail, with no
cancelled/skipped/todo cases. All32 processes were reaped.

`migration.py` validates these identities, states and terminal counts. It carries
all31 passes and the failure unchanged. Each gets a direct `last_execution`
receipt naming run41598441535, original report key and prior ledger SHA, not a
nested copy of the entire previous ledger. `executed_in_batch` is false for every
carried record. The new artifact distinguishes newly executed file count from
overall current-source executed/terminal-pass counts. Original prior TAP and
screenshots remain in their original artifact; no current report is fabricated.

The unresolved current-source failure is `quota-count-wrap.test.cjs`: both
engines reported cut content at1100px. Its cause is not established and it is not
an accepted baseline. It stays failed even if all32 new files pass. With32 new
terminal files, current coverage would become64 files and763 not run, not a
complete827-file suite.

Old94e's24 executions remain provenance only. They are never promoted to ff997
coverage. The two separately observed positive auth-file witnesses remain
uncredited to this827 ledger; the intentional negative fixture is not a product
failure. Earlier105 incomplete-run failure observations, four old-source recovery
failures and the separate10 visual cases/unresolved10px issue remain recorded in
`open-obligations.json` and the hash-linked history.

The **full-suite gate remains RED** regardless of this batch's result. Code
standards, whole-suite, visual, release and merge requirements are not waived.

## Exact source proof without repetitive large evidence

No new source migration is occurring. Activation checks exact source HEAD,
parent/tree, clean checkout, full827 test inventory and every test's SHA256/Git
blob. The prior independently reconstructed full-tree migration receipt is kept
and hash-bound. It established that842 test/support entries and218 asset entries
were unchanged from94e to ff997, without asserting that tests reading other Go
files were equivalent.

Large historical complete-tree maps, original ZIPs, screenshot sets and old
partial outputs are not repeatedly copied into this Git draft. Their immutable
hashes and retained local archive locations appear in `historical-evidence-index.json`.
Only the necessary full827 current prior ledger, compact outcomes, official job
responses, inventory, provenance receipts and controls are staged. Priorbatch003
files and freeze are untouched.

## Cumulative budget

The preceding reviewed totals1045.758023034 test seconds /1512 job seconds are
bound inside the prior ledger. Batch003 adds actual controller wall time
740.1287714770001 seconds and official job797 seconds. That gives
**1785.886794511 test /2309 job seconds**.

A separate no-op scheduling sentinel, run41600206909, consumed0 product/browser
test seconds and35 official executed-job seconds. Its intentional failed-package
sentinel is scheduling evidence, not a product test failure. Skipped official job
records include anomalous negative timestamp pairs; they remain raw and contribute
no execution time. The sentinel is separately classified, and its pending review
status is not a new product acceptance claim.

The frozen admission total is therefore **1785.886794511 test /2344 job seconds**.
The original8h/12h ceilings remain **28800 /43200**. Available before batch004 is
**27014.113205489 test /40856 job seconds**. This one batch reserves1800/3300.

Batch test cost means controller wall time, not summed concurrent file/CPU/browser
time. The historical requiredGUI conservative32-second charge remains included
unchanged; its raw30.638664386 process seconds are not substituted to reduce debt.
Every actual attempted run, including failure or no-op scheduling overhead, stays
accounted. Missing, stale, altered, nonfinite or reduced costs fail admission.

No automatic retries, later batches or ceiling extension. If another diagnostic
runs before activation, update actual receipts and refreeze before using this
plan. The full suite is not guaranteed to fit the remaining budget; assess actual
throughput after each bounded batch and report any completion gap rather than
waiving tests or extending the caps silently.

## Verification and freeze

`selftest.py` runs only local synthetic Python child processes. It tests terminal
classification, exact source/runtime/selection, retention of the31 passes and
failure, old-source rejection, budget/source/receipt mutations and seven scheduler
scenarios: ordinary failure continuation, all pass, carried failure surviving a
green selected batch, timeout plus descendant cleanup, batch deadline, startup
failure and persistent simulated ENOSPC during report writing.

The local harness places a read-only `.git` marker under/tmp. Only the synthetic
Python tests substitute an ownership validator. Production still requires temp
HOME/TMP outside Git discovery; no browser proof is claimed locally.

Run `python3 -I -B .github/gui-batch004/selftest.py`, then
`python3 -I -B .github/gui-batch004/freeze.py`. The latter binds the exact text
controls and workflow hash pins, writes local `FROZEN-LOCAL.json`, and produces
`STAGING-PATHS.txt`. Those root receipts/logs are not staged. Any receipt/control
change requires new tests and freeze. Activation requires one validation-only
single-parent commit directly above the exact product source. No main/release
operation is part of this draft.
