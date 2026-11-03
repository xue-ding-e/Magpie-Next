# Full GUI recovery batch003: local draft

This directory is validation-only. Preparing it does not schedule CI, compile
Magpie, launch a browser, change product tests/assets/guards, or permit a release.
Actual requiredGUI receipts are now frozen. Admission still fails closed on missing,
pending, mismatched or unverified timing evidence. No CI has been activated.

## Exact scope

- Product `ff997c492a908293dc0de152ee20f83152b88aa9`, parent
  `a27f0957427b4ba86f7c140fa38068922adcc9ea`, tree
  `bc56abd9c642a1315ea3a3f32a30d9998e15137a`.
- Whole current-source inventory remains **827 original files**. Select the next
  **32** previously `not_run` files, in inventory order, immediately after
  `provider-many-keys.test.cjs`: `provider-max-output.test.cjs` through
  `quota-curve.test.cjs`. `inventory.json` names all 32 exactly.
- At most **2** independent original test processes, **600 seconds per file**,
  **1800 seconds per batch**, external TERM at **1860 seconds**, KILL after another
  **30 seconds**, execution step **32 minutes**, whole job **55 minutes**.
- Node **22.23.4**, integrity-locked README-tested Playwright **1.62.1**. The root
  preview package is not substituted. Browser revisions are copied to artifacts.
- Preserve original per-file engine/language loops. No `BROWSER` or channel/name
  filter, skip, CSS, diagnostic click/geometry hook, fixture or guard edit.
  `offline.cjs`, package.json and lock are byte-identical to batch002.
- Ordinary test failures never stop the independent queue. Deadlines/resource
  limits stop it; unstarted files remain `not_run`, never failed or passed by
  inference. Every started process has its own session/process group and temporary
  HOME/profile. Cleanup only targets those owned groups and directories.

## Honest source migration

`migration.py` independently reconstructs both Git root-tree SHA identities from
all leaves, not only a few named paths. Old tree has5701 leaves, current5711.
All842 entries under `internal/gui/tests/` (827 tests plus helpers/fixtures) and
all218 assets entries have identical path, type, mode and Git blob hashes. The
13 other changes are listed in `source-migration.json`, including Go exporter
files and build/CI controls.

Identical GUI bytes do **not** prove arbitrary tests that read Go or other files
are equivalent. The24 old94e executions are archived with their original source,
counts, run and ledger hash, not credited as ff997 passes. All827 new-source ledger
rows start `not_run`. There are803 old recovery `not_run` files, not803 remaining
current-source files. Those quantities must not be confused.

Any genuine positive ff997 auth-file executions from separate requiredGUI proof
can be preserved in `external-current-witnesses.json` as uncredited witnesses.
They are not erased or called old-source passes. Acceptance requires a separate
review of runtime, guard, file hashes, original complete engine/language loops and
terminal counts. This draft never imports them as827-suite passes. The intentional
negative injected fixture is control evidence and must never enter that ledger.

## Unresolved obligations

`open-obligations.json` retains the earlier105 observed failures from incomplete
run41438741797 and the separate10 masked-preview visual cases/unresolved10px issue.
Those105 are observations, not a complete-suite total. The four latest batch002
failures are still unproven as a baseline:

- provider-detect-draft.test.cjs
- provider-draft-picks.test.cjs
- provider-gemini.test.cjs
- provider-levels-save.test.cjs

The full-suite gate stays **RED**, including when these32 files all pass. No
whole-suite/code-standards waiver, shipping permission, main integration or release
follows from this bounded batch. No old failure or visual obligation is dropped.

## Cumulative accounting: actual receipts supplied

Preserve recovery base **1013.758023034 test seconds /1274 job seconds**. Failed
scheduling proof41584387550 had zero browser execution and26 executed-job seconds:
plan14 + zero_package3 + audit9. Official skipped records are retained, including
GitHub's anomalous negative timestamp pairs; they are not actual execution.
Known intermediate base was **1013.758023034 test seconds /1300 job seconds**.

Terminal proof41584865002 at control head
`fb99fa33a5f56174bc279098deec43d9ea84ed1c` has original positive files9+10 passed,
intentional negative assertion1 failed as expected, and all child processes reaped.
All non-skipped jobs consumed212 seconds. Raw actual GUI process times sum
**30.638664386000016 seconds**, preserved separately. The conservative budget
charge is **32 test seconds**: official positive execution step31 seconds plus
ceil(actual negative process0.5155913270000099 seconds)=1 second.

The charged cumulative total is now **1045.758023034 test seconds /1512 job
seconds**. `budget.py` derives and verifies those values from frozen official jobs,
raw artifact summaries, source hashes and the outcome receipt, not a placeholder.
`required-gui-receipt.json` is `verified_terminal`. Its original pending state
remains tested by a mutation control and cannot pass admission.

Historical recovery batches and this batch count **batch-controller wall time**,
not the sum of concurrent file process time. RequiredGUI proof uses the conservative
charge described above. Keep raw observed process time separate; the cumulative
test figure is not total browser/CPU time. Non-browser plan/setup/audit work counts
as job seconds. The intentional negative fixture charges resources but is never
reported as a failed product file in the827 ledger.

The two current positive original-file witnesses remain separately preserved and
uncredited as827-suite coverage. A separate acceptance decision is still needed
before importing witnesses across runner configurations, even though the required
proof itself was independently reviewed. Current-source inventory therefore
starts with all827 `not_run` rows; the next32 alone are selected here.

Original maximums remain **28800 test seconds /43200 job seconds** (8h/12h).
Remaining before batch003 is **27754.241976966 test seconds /41688 job seconds**.
Admission reserves1800/3300 for this one batch and rejects an exhausted budget.
There is no automatic retry, next batch, time extension or reset after migration.
If another run or diagnostic occurs before activation, add its actual debt and
receipts and refreeze. Never replace a failed run with a later successful one.

All827 at the worst-case600 seconds/file and concurrency2 require248100 seconds,
well beyond remaining budget. Full-suite completion inside the budget is not
guaranteed; reassess actual throughput after this batch and report a gap if it
cannot finish. Bounded batches never waive uncompleted files or visual obligations.

Necessary JSON/TAP evidence is in `required-gui-evidence/`; original ZIP binaries
remain in the complete local artifact archive, with their verified official
digests recorded in `archive-digests.json`. Raw jobs/run responses and the
derived timing receipt are alongside it. Changing any receipt requires rerunning
`selftest.py`, then `freeze.py`, and reviewing the new frozen manifest before any
separately authorized activation. This draft does not activate anything.

## Lightweight checks and freeze

`selftest.py` covers terminal classification, root-tree reconstruction and omitted
leaves, source/runtime/selection rejection, actual receipt accounting and pending/invalid/NaN/reset budget
rejection, and harmless synthetic Python subprocesses: success, failure continued
through later files, incomplete output, per-file timeout with descendant cleanup,
batch deadline with unstarted rows, startup failure and persistent simulated
ENOSPC while writing per-file summaries. The latter confirms every owned child is
cleaned even when one cleanup/report path fails. No GUI or compilation.

The local harness puts a read-only .git marker in/tmp. Synthetic Python process
tests use a test-only ownership validator there; production's strict outside-Git
HOME/TMP check is unchanged and still mandatory. This is not local browser proof.

`freeze.py` binds every staged control file, fills workflow hash pins and writes
`FROZEN-LOCAL.json` at the draft root. Stage only paths listed there and in `STAGING-PATHS.txt`, after parent
review; the freeze file itself is local receipt, not a product change. Changing an
actual receipt requires rerunning controls and freezing again. The draft must be
one single-parent validation-only commit directly above the exact product source.
