// Runner contracts only: generated fixtures use node:test, never Playwright.
import assert from "node:assert/strict";
import { test } from "node:test";
import fs from "node:fs/promises";
import { createWriteStream } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { finished } from "node:stream/promises";
import { discoverInventory, selectShard, verifyReports, fileResults, executeTests, runCommand } from "./ui-test-shard.mjs";

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

const files = Array.from({ length: 9 }, (_, i) => `internal/gui/tests/${i}.test.cjs`);
const source = "a".repeat(40), digest = "b".repeat(64);
function reports() {
  return Array.from({ length: 4 }, (_, shardIndex) => {
    const selected = files.filter((_, i) => i % 4 === shardIndex);
    return { sourceSHA: source, expectedFiles: files.length, shardCount: 4, shardIndex,
      inventory: [...files], selected, inputSHA256: digest, assetsSHA256: digest,
      inputsUnchanged: true, exitCode: 0, results: selected.map((file) => ({ file, success: true })) };
  });
}

test("current complete inventory has exactly one owner for all 829 files", async () => {
  const repo = path.resolve(fileURLToPath(new URL("../", import.meta.url)));
  const inventory = await discoverInventory(repo);
  const selected = Array.from({ length: 4 }, (_, i) => selectShard(inventory, i));
  assert.deepEqual(selected.map((shard) => shard.length), [208, 207, 207, 207]);
  assert.equal(new Set(selected.flat()).size, 829);
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
  assert.throws(() => selectShard(files, 4), /shard index/);
});

test("aggregate requires all four distinct shards", () => {
  assert.equal(verifyReports(reports(), source, files.length).files, files.length);
  assert.throws(() => verifyReports(reports().slice(1), source, files.length), /missing shard/);
  const duplicate = reports(); duplicate[3] = structuredClone(duplicate[0]);
  assert.throws(() => verifyReports(duplicate, source, files.length), /duplicate shard/);
});

test("aggregate rejects missing or duplicate file results and mixed/failed evidence", () => {
  for (const mutate of [
    (r) => r[0].results.pop(),
    (r) => { r[0].results[1] = r[0].results[0]; },
    (r) => { r[0].exitCode = 7; },
    (r) => { r[0].inputsUnchanged = false; },
    (r) => { r[0].sourceSHA = "c".repeat(40); },
    (r) => { r[0].assetsSHA256 = "c".repeat(64); },
  ]) {
    const value = reports(); mutate(value);
    assert.throws(() => verifyReports(value, source, files.length));
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
