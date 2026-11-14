// Runner contracts only: generated fixtures use node:test, never Playwright.
import assert from "node:assert/strict";
import { test } from "node:test";
import fs from "node:fs/promises";
import { createWriteStream } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { finished } from "node:stream/promises";
import { execFileSync, spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { discoverInventory, selectShard, verifyReports, fileResults, executeTests, runCommand,
  chooseBaseline, selectBaseline, baselineSnapshot, inputSnapshot, SHARDS } from "./ui-test-shard.mjs";

async function fixture(t, names = ["a", "b"]) {
  const root = await fs.mkdtemp(path.join(os.tmpdir(), "magpie-ui-shard-"));
  t.after(async () => {
    assert(path.isAbsolute(root) && path.dirname(root) === path.resolve(os.tmpdir()));
    await fs.rm(root, { recursive: true, force: true, maxRetries: 5, retryDelay: 50 });
  });
  await fs.mkdir(path.join(root, "internal/gui/tests"), { recursive: true });
  for (const name of names) await fs.writeFile(path.join(root, `internal/gui/tests/${name}.test.cjs`), "require('node:test')('synthetic fixture', () => {});\n");
  return root;
}

async function shallowFixture(t, upstream = false) {
  const root = await fixture(t, ["baseline"]);
  const env = Object.fromEntries(Object.entries(process.env).filter(([name]) => !name.toUpperCase().startsWith("GIT_")));
  const config = path.join(root, "empty-git-config");
  await fs.writeFile(config, "");
  Object.assign(env, { GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: config, GIT_CONFIG_SYSTEM: config,
    GIT_TERMINAL_PROMPT: "0", GCM_INTERACTIVE: "never" });
  const hooks = path.join(root, "no-hooks");
  await fs.mkdir(hooks);
  const git = (cwd, ...args) => execFileSync("git", ["-c", `core.hooksPath=${hooks}`, "-c", "credential.helper=",
    "-c", "commit.gpgsign=false", "-c", "core.autocrlf=false", "-c", "user.name=UI fixture",
    "-c", "user.email=ui-fixture@example.invalid", ...args], { cwd, env, windowsHide: true, maxBuffer: 256 * 1024 * 1024 });
  for (const name of ["internal/gui/assets/index.html", "internal/gui/assets/app.js", "internal/gui/assets/app.css",
    "build/ui-test-shard.mjs", "build/ui-test-shard.test.mjs", ".github/ui-preview/package.json", ".github/workflows/test-ui.yml"]) {
    const file = path.join(root, name);
    await fs.mkdir(path.dirname(file), { recursive: true });
    await fs.writeFile(file, `synthetic ${name}\n`);
  }
  git(root, "init", "--template=", "-b", "candidate");
  git(root, "add", "."); git(root, "commit", "-m", "synthetic baseline");
  const baselineSHA = git(root, "rev-parse", "HEAD").toString().trim();
  await fs.writeFile(path.join(root, "internal/gui/assets/app.js"), "synthetic candidate parent\n");
  if (upstream) {
    git(root, "add", "internal/gui/assets/app.js");
    const tree = git(root, "write-tree").toString().trim();
    git(root, "commit", "-m", "synthetic upstream snapshot",
      "-m", `Upstream-Commit: ${baselineSHA}\nUpstream-Tree: ${tree}`);
  } else {
    git(root, "commit", "-am", "synthetic candidate parent");
  }
  const parentSHA = git(root, "rev-parse", "HEAD").toString().trim();
  await fs.writeFile(path.join(root, "internal/gui/assets/app.js"), "synthetic candidate\n");
  git(root, "commit", "-am", "synthetic candidate");
  const sourceSHA = git(root, "rev-parse", "HEAD").toString().trim();
  const clone = path.join(root, "shallow");
  git(root, "clone", "--no-hardlinks", "--depth=1", "--single-branch", "--branch", "candidate", pathToFileURL(root).href, clone);
  assert.equal(git(clone, "rev-parse", "--is-shallow-repository").toString().trim(), "true");
  assert.throws(() => git(clone, "cat-file", "-e", `${baselineSHA}^{commit}`));
  assert.throws(() => git(clone, "rev-parse", "--verify", "origin/main"));
  return { repo: clone, git: (...args) => git(clone, ...args), baselineSHA, parentSHA, sourceSHA };
}

const files = Array.from({ length: 9 }, (_, i) => `internal/gui/tests/${i}.test.cjs`);
const source = "a".repeat(40), digest = "b".repeat(64);
const pinned = "c".repeat(40);
function reports() {
  return Array.from({ length: 4 }, (_, shardIndex) => {
    const selected = files.filter((_, i) => i % 4 === shardIndex);
    return { sourceSHA: source, expectedFiles: files.length, shardCount: 4, shardIndex,
      inventory: [...files], selected, inputSHA256: digest, assetsSHA256: digest,
      baseline: { sourceSHA: source, mainSHA: pinned, baselineSHA: pinned, policy: "origin/main",
        assetsTreeSHA: "d".repeat(40), assetsSHA256: digest },
      inputsUnchanged: true, exitCode: 0, results: selected.map((file) => ({ file, success: true })) };
  });
}

test("current complete inventory has exactly one owner for all 1519 files", async () => {
  const repo = path.resolve(fileURLToPath(new URL("../", import.meta.url)));
  const inventory = await discoverInventory(repo);
  const selected = Array.from({ length: SHARDS }, (_, i) => selectShard(inventory, i));
  assert.deepEqual(selected.map((shard) => shard.length), [127, 127, 127, 127, 127, 127, 127, 126, 126, 126, 126, 126]);
  assert.equal(new Set(selected.flat()).size, 1519);
  assert.deepEqual(selected.flat().sort(), inventory);
});

test("inventory rejects a missing file and an otherwise unselected nested fixture", async (t) => {
  const root = await fixture(t);
  await fs.unlink(path.join(root, "internal/gui/tests/b.test.cjs"));
  await assert.rejects(discoverInventory(root, 2), /inventory count/);
  await fs.mkdir(path.join(root, "internal/gui/tests/nested"));
  await fs.writeFile(path.join(root, "internal/gui/tests/nested/b.test.cjs"), "");
  await assert.rejects(discoverInventory(root, 2), /omitted from flat/);
});

test("selection rejects duplicate inventory, empty shard and out-of-range index", () => {
  assert.throws(() => selectShard([files[0], files[0]], 0), /duplicate/);
  assert.throws(() => selectShard([files[0]], 3), /empty shard/);
  assert.throws(() => selectShard(files, SHARDS), /shard index/);
});

test("aggregate requires all four distinct shards", () => {
  assert.equal(verifyReports(reports(), source, files.length, 4).files, files.length);
  assert.throws(() => verifyReports(reports().slice(1), source, files.length, 4), /missing shard/);
  const duplicate = reports(); duplicate[3] = structuredClone(duplicate[0]);
  assert.throws(() => verifyReports(duplicate, source, files.length, 4), /duplicate shard/);
});

test("aggregate rejects missing or duplicate file results and mixed/failed evidence", () => {
  for (const mutate of [
    (r) => r[0].results.pop(),
    (r) => { r[0].results[1] = r[0].results[0]; },
    (r) => { r[0].exitCode = 7; },
    (r) => { r[0].inputsUnchanged = false; },
    (r) => { r[0].sourceSHA = "c".repeat(40); },
    (r) => { r[0].assetsSHA256 = "c".repeat(64); },
    (r) => { r[0].baseline.baselineSHA = source; },
    (r) => { r[0].baseline.assetsSHA256 = "e".repeat(64); },
  ]) {
    const value = reports(); mutate(value);
    assert.throws(() => verifyReports(value, source, files.length, 4));
  }
});

test("file summaries cannot omit or duplicate a selected file", async (t) => {
  const root = await fixture(t), eventFile = path.join(root, "events.jsonl");
  const selected = ["internal/gui/tests/a.test.cjs", "internal/gui/tests/b.test.cjs"];
  const event = (file) => JSON.stringify({ type: "test:summary", data: { file: path.join(root, file), success: true,
    counts: { tests: 1, passed: 1, failed: 0, cancelled: 0 } } });
  await fs.writeFile(eventFile, event(selected[0]) + "\n");
  await assert.rejects(fileResults(root, selected, eventFile), /no summary/);
  await fs.writeFile(eventFile, event(selected[0]) + "\n" + event(selected[0]) + "\n");
  await assert.rejects(fileResults(root, selected, eventFile), /duplicate file summary/);
});

test("real Node child retains each file result and the complete failure log", async (t) => {
  const root = await fixture(t);
  await fs.writeFile(path.join(root, "internal/gui/tests/b.test.cjs"), "require('node:test')('deliberate fixture failure', () => require('node:assert/strict').fail('visible sentinel'));\n");
  const env = { ...process.env };
  delete env.NODE_TEST_CONTEXT; delete env.NODE_OPTIONS; delete env.BROWSER;
  const result = await executeTests({ repo: root, output: path.join(root, "results"),
    selected: ["internal/gui/tests/a.test.cjs", "internal/gui/tests/b.test.cjs"], env });
  assert.equal(result.exitCode, 1);
  assert.equal(result.results.length, 2);
  assert.deepEqual(result.results.map((r) => r.success), [true, false]);
  assert.match(await fs.readFile(path.join(root, "results/spec.log"), "utf8"), /visible sentinel/);
});

test("native child exit code is preserved instead of turning failure green", async (t) => {
  const root = await fixture(t, []), log = createWriteStream(path.join(root, "exit.log"));
  const code = await runCommand(process.execPath, ["-e", "process.exit(7)"], { cwd: root, env: process.env, log });
  log.end(); await finished(log);
  assert.equal(code, 7);
});

test("explicit preload reaches both real test children and preserves a deliberate failure", async (t) => {
  const root = await fixture(t);
  const setup = path.join(root, "fixture-setup.cjs"), receipt = path.join(root, "setup-receipts.jsonl");
  await fs.writeFile(setup, `globalThis.fixtureReady = true; require('node:fs').appendFileSync(process.env.FIXTURE_SETUP_RECEIPT,
    JSON.stringify({ pid:process.pid, entry:process.argv[1], context:process.env.NODE_TEST_CONTEXT })+'\\n');\n`);
  for (const name of ["a", "b"]) await fs.writeFile(path.join(root, `internal/gui/tests/${name}.test.cjs`),
    `const assert=require('node:assert/strict'); require('node:test')('${name}',()=>{ assert.equal(globalThis.fixtureReady,true); ${name === "b" ? "assert.fail('preloaded failure sentinel');" : ""} });\n`);
  const env = { ...process.env, FIXTURE_SETUP_RECEIPT: receipt };
  delete env.NODE_TEST_CONTEXT; delete env.NODE_OPTIONS; delete env.BROWSER;
  const selected = ["internal/gui/tests/a.test.cjs", "internal/gui/tests/b.test.cjs"];
  const result = await executeTests({ repo: root, selected, output: path.join(root, "results"), env, browserSetup: setup });
  assert.equal(result.exitCode, 1);
  assert.deepEqual(result.results.map((r) => r.success), [true, false]);
  assert.match(await fs.readFile(path.join(root, "results/spec.log"), "utf8"), /preloaded failure sentinel/);
  const entries = (await fs.readFile(receipt, "utf8")).trim().split("\n").map(JSON.parse).filter((r) => r.context);
  assert.deepEqual(entries.map((r) => path.basename(r.entry)).sort(), ["a.test.cjs", "b.test.cjs"]);
  assert.equal(new Set(entries.map((r) => r.pid)).size, 2, "both isolated test-file processes received the preload");
});

test("baseline policy pins main and uses its first parent when main is the candidate", () => {
  assert.deepEqual(chooseBaseline(source, pinned), { sourceSHA: source, mainSHA: pinned,
    baselineSHA: pinned, policy: "origin/main" });
  assert.deepEqual(chooseBaseline(source, source, pinned), { sourceSHA: source, mainSHA: source,
    baselineSHA: pinned, policy: "source-first-parent" });
  assert.throws(() => chooseBaseline(source, source), /first parent/);
  assert.throws(() => chooseBaseline(source, source, source), /differ from/);
});

test("a real shallow checkout fetches the missing baseline and seals its exact bytes", async (t) => {
  const { repo, git, sourceSHA, baselineSHA, parentSHA } = await shallowFixture(t);
  await assert.rejects(baselineSnapshot(repo, sourceSHA, baselineSHA, baselineSHA));
  git("fetch", "--no-tags", "--depth=1", "origin", baselineSHA);
  assert.throws(() => git("cat-file", "-e", `${parentSHA}^{commit}`));
  git("fetch", "--no-tags", "--depth=1", "origin", parentSHA);
  const baseline = await baselineSnapshot(repo, sourceSHA, baselineSHA, baselineSHA);
  assert.equal(baseline.baselineSHA, baselineSHA);
  assert.equal(baseline.files["internal/gui/assets/app.js"], createHash("sha256")
    .update(git("show", `${baselineSHA}:internal/gui/assets/app.js`)).digest("hex"));
  await assert.rejects(baselineSnapshot(repo, sourceSHA, sourceSHA, sourceSHA), /policy/);
  assert.equal(baseline.policy, "origin/main");
  assert.equal((await baselineSnapshot(repo, sourceSHA, sourceSHA, parentSHA)).policy, "source-first-parent");
  assert.equal(git("rev-parse", "HEAD").toString().trim(), sourceSHA);
  const before = await inputSnapshot(repo, baseline);
  const changed = await inputSnapshot(repo, { ...baseline, assetsSHA256: "0".repeat(64) });
  assert.notEqual(before.inputSHA256, changed.inputSHA256);
  assert.equal(before.assetsSHA256, changed.assetsSHA256);
});

test("an upstream refresh compares with its sealed upstream parent on a candidate branch", async (t) => {
  const { repo, git, sourceSHA, baselineSHA, parentSHA } = await shallowFixture(t, true);
  git("fetch", "--no-tags", "--depth=1", "origin", parentSHA);
  assert.deepEqual(selectBaseline(repo, sourceSHA, baselineSHA), {
    sourceSHA, mainSHA: baselineSHA, baselineSHA: parentSHA, policy: "upstream-snapshot-parent",
  });
  await assert.rejects(baselineSnapshot(repo, sourceSHA, baselineSHA, baselineSHA), /policy/);
  const snapshot = await baselineSnapshot(repo, sourceSHA, baselineSHA, parentSHA);
  assert.equal(snapshot.files["internal/gui/assets/app.js"], createHash("sha256")
    .update(git("show", `${parentSHA}:internal/gui/assets/app.js`)).digest("hex"));
});

test("real child receives the pinned baseline instead of an inherited missing main ref", async (t) => {
  const root = await fixture(t, ["baseline"]);
  await fs.writeFile(path.join(root, "internal/gui/tests/baseline.test.cjs"),
    `require('node:test')('baseline reaches the fixture', () => require('node:assert/strict').equal(process.env.BASE_REF, '${pinned}'));\n`);
  const env = { ...process.env, BASE_REF: "origin/main" };
  delete env.NODE_TEST_CONTEXT; delete env.NODE_OPTIONS; delete env.BROWSER;
  const result = await executeTests({ repo: root, output: path.join(root, "results"), env,
    selected: ["internal/gui/tests/baseline.test.cjs"], baselineSHA: pinned });
  assert.equal(result.exitCode, 0);
  assert.equal(result.results[0].success, true);
});

test("mobile fixture rejects a missing main ref before loading or launching browsers", async (t) => {
  const { repo } = await shallowFixture(t);
  const name = "internal/gui/tests/mobile-web.test.cjs";
  await fs.copyFile(fileURLToPath(new URL("../" + name, import.meta.url)), path.join(repo, name));
  const env = { ...process.env, BASE_REF: "origin/main" };
  delete env.NODE_TEST_CONTEXT; delete env.NODE_OPTIONS; delete env.NODE_PATH;
  const result = spawnSync(process.execPath, ["--test", "--test-concurrency=1", name], {
    cwd: repo, env, windowsHide: true, encoding: "utf8", timeout: 10000 });
  assert.equal(result.status, 1);
  assert.match(result.stdout + result.stderr, /origin\/main/);
  assert.doesNotMatch(result.stdout + result.stderr, /Cannot find module|#nav button/);
});
