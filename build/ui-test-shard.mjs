// Credential-free browser fixtures: fixed source, complete inventory and file results.
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { createReadStream, createWriteStream } from "node:fs";
import fs from "node:fs/promises";
import path from "node:path";
import { spawn, execFileSync } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";
import readline from "node:readline";
import { finished } from "node:stream/promises";

const SELF = fileURLToPath(import.meta.url);
export const EXPECTED_FILES = 1519;
export const SHARDS = 12;
const slash = (value) => value.split(path.sep).join("/");
const sha = (value) => createHash("sha256").update(value).digest("hex");
const json = (filename, value) => fs.writeFile(filename, JSON.stringify(value, null, 2) + "\n");

async function walk(directory) {
  const files = [];
  for (const entry of await fs.readdir(directory, { withFileTypes: true })) {
    const filename = path.join(directory, entry.name);
    if (entry.isSymbolicLink()) throw new Error(`symlink is not a sealed UI input: ${filename}`);
    if (entry.isDirectory()) files.push(...await walk(filename));
    else if (entry.isFile()) files.push(filename);
  }
  return files.sort();
}

function unique(files) {
  assert.equal(new Set(files).size, files.length, "duplicate inventory file");
  assert.deepEqual(files, [...files].sort(), "inventory must be sorted");
}

export async function discoverInventory(repo, expected = EXPECTED_FILES) {
  const directory = path.join(repo, "internal/gui/tests");
  const flat = (await fs.readdir(directory, { withFileTypes: true }))
    .filter((entry) => entry.isFile() && entry.name.endsWith(".test.cjs"))
    .map((entry) => `internal/gui/tests/${entry.name}`).sort();
  const recursive = (await walk(directory)).filter((name) => name.endsWith(".test.cjs"))
    .map((name) => slash(path.relative(repo, name))).sort();
  assert.deepEqual(flat, recursive, "recursive UI test omitted from flat inventory");
  unique(flat);
  assert.equal(flat.length, expected, "UI inventory count changed; review the complete inventory");
  return flat;
}

export function selectShard(files, index, count = SHARDS) {
  unique(files);
  assert(Number.isInteger(count) && count > 0, "invalid shard count");
  assert(Number.isInteger(index) && index >= 0 && index < count, "invalid shard index");
  const selected = files.filter((_, position) => position % count === index);
  assert(selected.length > 0, "empty shard");
  return selected;
}

export function chooseBaseline(sourceSHA, mainSHA, firstParentSHA, upstreamSnapshot = false) {
  for (const value of [sourceSHA, mainSHA]) assert.match(value, /^[a-f0-9]{40}$/, "invalid baseline selection SHA");
  const useParent = mainSHA === sourceSHA || upstreamSnapshot;
  const baselineSHA = useParent ? firstParentSHA : mainSHA;
  assert.match(baselineSHA || "", /^[a-f0-9]{40}$/, "main candidate needs its first parent baseline");
  assert.notEqual(baselineSHA, sourceSHA, "desktop baseline must differ from the candidate");
  return { sourceSHA, mainSHA, baselineSHA, policy: upstreamSnapshot ? "upstream-snapshot-parent" : useParent ? "source-first-parent" : "origin/main" };
}

function git(repo, ...args) {
  return execFileSync("git", ["--no-optional-locks", ...args], { cwd: repo, windowsHide: true, maxBuffer: 256 * 1024 * 1024 });
}

export function selectBaseline(repo, sourceSHA, mainSHA) {
  const commit = git(repo, "cat-file", "commit", sourceSHA).toString();
  const parents = commit.split("\n\n", 1)[0].match(/^parent [a-f0-9]{40}$/gm) || [];
  const parent = parents.length === 1 ? parents[0].slice(7) : undefined;
  let upstreamSnapshot = false;
  if (parent) {
    const parentText = git(repo, "cat-file", "commit", parent).toString();
    const upstreamCommit = parentText.match(/^Upstream-Commit: ([a-f0-9]{40})$/m)?.[1];
    const upstreamTree = parentText.match(/^Upstream-Tree: ([a-f0-9]{40})$/m)?.[1];
    const tree = parentText.match(/^tree ([a-f0-9]{40})$/m)?.[1];
    upstreamSnapshot = Boolean(upstreamCommit && upstreamTree && upstreamTree === tree);
  }
  return chooseBaseline(sourceSHA, mainSHA, parent, upstreamSnapshot);
}

export async function baselineSnapshot(repo, sourceSHA, mainSHA, baselineSHA) {
  const selected = selectBaseline(repo, sourceSHA, mainSHA);
  assert.equal(baselineSHA, selected.baselineSHA, "baseline differs from the pinned main/first-parent policy");
  assert.equal(git(repo, "rev-parse", "--verify", `${baselineSHA}^{commit}`).toString().trim(), baselineSHA, "baseline commit missing");
  const dir = "internal/gui/assets/";
  const entries = git(repo, "ls-tree", "-r", "-z", baselineSHA, "--", dir).toString().split("\0").filter(Boolean)
    .map((entry) => {
      const match = entry.match(/^(100644|100755) blob ([a-f0-9]{40})\t(.+)$/);
      assert(match, "baseline assets must be regular Git blobs");
      return { blob: match[2], file: match[3] };
    });
  assert(entries.length > 0, "baseline has no UI assets");
  const out = execFileSync("git", ["--no-optional-locks", "cat-file", "--batch"], {
    cwd: repo, windowsHide: true, input: entries.map((entry) => entry.blob + "\n").join(""), maxBuffer: 256 * 1024 * 1024 });
  const files = {};
  let offset = 0;
  for (const entry of entries) {
    const end = out.indexOf(10, offset), header = out.subarray(offset, end).toString().split(" ");
    const size = Number(header[2]);
    assert(end >= offset && header[0] === entry.blob && header[1] === "blob" && Number.isSafeInteger(size) && size >= 0, "invalid baseline blob response");
    const body = out.subarray(end + 1, end + 1 + size);
    assert.equal(body.length, size, "truncated baseline blob");
    files[entry.file] = sha(body);
    offset = end + size + 2;
    assert.equal(out[offset - 1], 10, "baseline blob missing trailing newline");
  }
  assert.equal(offset, out.length, "unexpected trailing baseline blob bytes");
  for (const name of ["index.html", "app.js", "app.css"]) assert(files[dir + name], `baseline missing ${name}`);
  const ordered = Object.entries(files).sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0);
  return { ...selected, assetsTreeSHA: git(repo, "rev-parse", `${baselineSHA}:internal/gui/assets`).toString().trim(),
    files, assetsSHA256: sha(JSON.stringify(ordered)) };
}

export async function inputSnapshot(repo, baseline) {
  const files = {};
  const roots = ["internal/gui/tests", "internal/gui/assets"];
  for (const root of roots) {
    for (const filename of await walk(path.join(repo, root))) {
      files[slash(path.relative(repo, filename))] = sha(await fs.readFile(filename));
    }
  }
  for (const relative of ["build/ui-test-shard.mjs", "build/ui-test-shard.test.mjs", ".github/ui-preview/package.json", ".github/workflows/test-ui.yml"]) {
    files[relative] = sha(await fs.readFile(path.join(repo, relative)));
  }
  const ordered = Object.entries(files).sort(([a], [b]) => a < b ? -1 : a > b ? 1 : 0);
  return { files, baseline, inputSHA256: sha(JSON.stringify({ files: ordered, baseline })),
    assetsSHA256: sha(JSON.stringify(ordered.filter(([name]) => name.startsWith("internal/gui/assets/")))) };
}

function childEnvironment(repo, home) {
  // Do not inherit preview/provider credentials, browser filters or NODE_OPTIONS.
  const allowed = new Set(["SYSTEMROOT", "WINDIR", "COMSPEC", "PATH", "PATHEXT", "OS", "PROCESSOR_ARCHITECTURE", "NUMBER_OF_PROCESSORS", "PLAYWRIGHT_BROWSERS_PATH"]);
  const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => allowed.has(key.toUpperCase())));
  Object.assign(env, { NODE_PATH: path.join(repo, ".github/ui-preview/node_modules"),
    CI: "1", NO_COLOR: "1", HOME: home, USERPROFILE: home,
    APPDATA: path.join(home, "roaming"), LOCALAPPDATA: path.join(home, "local"),
    TEMP: path.join(home, "tmp"), TMP: path.join(home, "tmp"), TMPDIR: path.join(home, "tmp"),
    XDG_CONFIG_HOME: path.join(home, "config"), XDG_CACHE_HOME: path.join(home, "cache"),
    XDG_DATA_HOME: path.join(home, "data"), XDG_STATE_HOME: path.join(home, "state"),
    MAGPIE_NO_ACCOUNTS: "1", MAGPIE_NO_STATS: "1", MAGPIE_NO_SHORTCUT: "1", DO_NOT_TRACK: "1" });
  return env;
}

export function runCommand(executable, args, { cwd, env, log }) {
  return new Promise((resolve) => {
    const child = spawn(executable, args, { cwd, env, windowsHide: true, stdio: ["ignore", "pipe", "pipe"] });
    child.stdout.on("data", (data) => log.write(data));
    child.stderr.on("data", (data) => log.write(data));
    child.once("error", (error) => { log.write(`spawn failed: ${error.code}\n`); });
    child.once("close", (code) => resolve(Number.isInteger(code) && code >= 0 ? code : 1));
  });
}

// The same file is the second Node reporter; no additional reporter dependency.
export default async function* fileEvents(events) {
  for await (const event of events) {
    if (["test:summary", "test:complete", "test:fail"].includes(event.type)) {
      yield JSON.stringify({ type: event.type, data: event.data }) + "\n";
    }
  }
}

export async function fileResults(repo, selected, eventFile) {
  const found = new Map();
  const lines = readline.createInterface({ input: createReadStream(eventFile), crlfDelay: Infinity });
  for await (const line of lines) {
    const event = JSON.parse(line);
    if (event.type !== "test:summary") continue;
    const name = event.data.file || event.data.entryFile;
    if (!name) continue; // One global summary follows the individual files.
    const relative = slash(path.relative(repo, path.resolve(repo, name)));
    assert(selected.includes(relative), `unexpected file summary: ${relative}`);
    assert(!found.has(relative), `duplicate file summary: ${relative}`);
    const counts = event.data.counts;
    assert(counts && Number.isInteger(counts.tests), "missing file test counts");
    found.set(relative, { file: relative, counts, success: event.data.success === true && counts.tests > 0 && counts.passed > 0 && counts.failed === 0 && counts.cancelled === 0 });
  }
  assert.deepEqual([...found.keys()].sort(), [...selected].sort(), "selected file produced no summary");
  return selected.map((file) => found.get(file));
}

export async function executeTests({ repo, selected, output, home = path.join(output, "home"), env, baselineSHA, browserSetup }) {
  await fs.mkdir(output, { recursive: true });
  const childEnv = { ...(env || childEnvironment(repo, home)), ...(baselineSHA ? { BASE_REF: baselineSHA } : {}) };
  if (!env) {
    for (const key of ["HOME", "APPDATA", "LOCALAPPDATA", "TEMP", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"]) {
      await fs.mkdir(childEnv[key], { recursive: true });
    }
  }
  const spec = path.join(output, "spec.log"), events = path.join(output, "events.jsonl");
  await fs.writeFile(spec, "");
  await fs.writeFile(events, "");
  const log = createWriteStream(path.join(output, "process.log"));
  let exitCode;
  try {
    const setup = browserSetup ? ["--require", browserSetup] : [];
    exitCode = await runCommand(process.execPath, [...setup, "--test", "--test-concurrency=2",
      "--test-reporter=spec", `--test-reporter-destination=${spec}`,
      `--test-reporter=${pathToFileURL(SELF).href}`, `--test-reporter-destination=${events}`,
      ...selected.map((name) => path.join(repo, name))], { cwd: repo, env: childEnv, log });
  } finally {
    log.end();
    await finished(log);
  }
  try {
    const results = await fileResults(repo, selected, events);
    return { exitCode: exitCode || (results.every((r) => r.success) ? 0 : 1), results };
  } catch (error) {
    return { exitCode: exitCode || 1, results: [], error: error.message };
  }
}

export function verifyReports(reports, sourceSHA, expected = EXPECTED_FILES, count = SHARDS, baselineSHA) {
  assert.equal(reports.length, count, "missing shard report");
  const indices = new Set(), files = new Set();
  const inventory = reports[0].inventory;
  unique(inventory);
  assert.equal(inventory.length, expected, "aggregate inventory count mismatch");
  for (const report of reports) {
    assert.equal(report.sourceSHA, sourceSHA, "mixed source SHAs");
    assert.equal(report.expectedFiles, expected, "shard expected-count mismatch");
    assert.equal(report.shardCount, count, "shard-count mismatch");
    assert(!indices.has(report.shardIndex), "duplicate shard index");
    indices.add(report.shardIndex);
    assert.deepEqual(report.inventory, inventory, "mixed file inventories");
    assert.match(report.baseline?.baselineSHA || "", /^[a-f0-9]{40}$/, "missing baseline SHA");
    assert.notEqual(report.baseline.baselineSHA, sourceSHA, "desktop baseline equals candidate");
    assert.equal(report.baseline.sourceSHA, sourceSHA, "baseline source mismatch");
    assert.match(report.baseline.mainSHA, /^[a-f0-9]{40}$/, "missing fetched main SHA");
    assert.match(report.baseline.assetsTreeSHA, /^[a-f0-9]{40}$/, "missing baseline asset tree");
    assert.match(report.baseline.assetsSHA256, /^[a-f0-9]{64}$/, "missing baseline asset seal");
    if (baselineSHA) assert.equal(report.baseline.baselineSHA, baselineSHA, "unexpected baseline SHA");
    assert.deepEqual(report.baseline, reports[0].baseline, "mixed desktop baselines");
    assert.match(report.inputSHA256, /^[a-f0-9]{64}$/);
    assert.match(report.assetsSHA256, /^[a-f0-9]{64}$/);
    assert.equal(report.inputSHA256, reports[0].inputSHA256, "mixed UI inputs");
    assert.equal(report.assetsSHA256, reports[0].assetsSHA256, "mixed assets");
    assert.equal(report.inputsUnchanged, true, "inputs changed during shard");
    assert.equal(report.exitCode, 0, "shard failed");
    assert.deepEqual(report.selected, selectShard(inventory, report.shardIndex, count), "incorrect modulo selection");
    assert.deepEqual(report.results.map((r) => r.file).sort(), [...report.selected].sort(), "missing or duplicated file result");
    for (const result of report.results) {
      assert.equal(result.success, true, "file did not pass");
      assert(!files.has(result.file), "file executed by more than one shard");
      files.add(result.file);
    }
  }
  assert.deepEqual([...indices].sort(), Array.from({ length: count }, (_, i) => i), "shard indices incomplete");
  assert.deepEqual([...files].sort(), inventory, "combined UI coverage is incomplete");
  return { sourceSHA, files: files.size, shards: count, baseline: reports[0].baseline,
    inputSHA256: reports[0].inputSHA256, assetsSHA256: reports[0].assetsSHA256 };
}

function options(argv) {
  const result = { repo: process.cwd(), expected: EXPECTED_FILES, count: SHARDS };
  const names = { "--repo": "repo", "--output": "output", "--shard-index": "index",
    "--shard-count": "count", "--expected-files": "expected", "--source-sha": "sourceSHA", "--verify-artifacts": "verify",
    "--baseline-sha": "baselineSHA", "--baseline-main-sha": "mainSHA", "--resolve-baseline-main": "resolveMain" };
  for (let i = 0; i < argv.length; i += 2) {
    assert(names[argv[i]] && argv[i + 1], "unknown option or missing value");
    result[names[argv[i]]] = argv[i + 1];
  }
  for (const name of ["index", "count", "expected"]) if (result[name] !== undefined) result[name] = Number(result[name]);
  result.repo = path.resolve(result.repo);
  result.sourceSHA ||= process.env.GITHUB_SHA;
  assert.match(result.sourceSHA || "", /^[a-f0-9]{40}$/, "explicit source SHA required");
  if (result.resolveMain) assert.match(result.resolveMain, /^[a-f0-9]{40}$/);
  else {
    assert.match(result.baselineSHA || "", /^[a-f0-9]{40}$/, "explicit baseline SHA required");
    if (!result.verify) assert.match(result.mainSHA || "", /^[a-f0-9]{40}$/, "explicit fetched main SHA required");
  }
  assert(result.output, "--output required");
  result.output = path.resolve(result.output);
  return result;
}

function verifyCheckout(repo, sourceSHA) {
  const git = (...args) => execFileSync("git", args, { cwd: repo, encoding: "utf8", windowsHide: true }).trim();
  assert.equal(git("rev-parse", "HEAD"), sourceSHA, "checkout differs from requested source SHA");
  assert.equal(git("status", "--porcelain", "--untracked-files=no"), "", "tracked checkout is dirty");
}

async function main(argv) {
  const o = options(argv);
  await fs.mkdir(o.output, { recursive: true });
  verifyCheckout(o.repo, o.sourceSHA);
  if (o.resolveMain) {
    const selected = selectBaseline(o.repo, o.sourceSHA, o.resolveMain);
    await json(path.join(o.output, "baseline-selection.json"), selected);
    console.log(JSON.stringify(selected));
    return 0;
  }
  if (o.verify) {
    const filenames = (await walk(path.resolve(o.verify))).filter((name) => path.basename(name) === "shard-result.json");
    const reports = await Promise.all(filenames.map(async (name) => JSON.parse(await fs.readFile(name, "utf8"))));
    const coverage = verifyReports(reports, o.sourceSHA, o.expected, o.count, o.baselineSHA);
    await json(path.join(o.output, "coverage.json"), coverage);
    console.log(JSON.stringify(coverage));
    return 0;
  }
  const report = { sourceSHA: o.sourceSHA, node: process.version, expectedFiles: o.expected,
    shardCount: o.count, shardIndex: o.index, inventory: [], selected: [], results: [],
    inputsUnchanged: false, exitCode: 1 };
  try {
    report.baseline = await baselineSnapshot(o.repo, o.sourceSHA, o.mainSHA, o.baselineSHA);
    await json(path.join(o.output, "baseline.json"), report.baseline);
    report.inventory = await discoverInventory(o.repo, o.expected);
    report.selected = selectShard(report.inventory, o.index, o.count);
    await json(path.join(o.output, "inventory.json"), { ...report, results: undefined });
    const before = await inputSnapshot(o.repo, report.baseline);
    Object.assign(report, { inputSHA256: before.inputSHA256, assetsSHA256: before.assetsSHA256 });
    await json(path.join(o.output, "inputs-before.json"), before);
    console.log(`UI shard ${o.index}/${o.count}: ${report.selected.length}/${report.inventory.length} files; both default engines; concurrency 2`);
    const execution = await executeTests({ repo: o.repo, selected: report.selected, output: o.output,
      home: path.join(path.dirname(o.output), `ui-home-${o.index}`), baselineSHA: report.baseline.baselineSHA });
    Object.assign(report, execution);
    const after = await inputSnapshot(o.repo, await baselineSnapshot(o.repo, o.sourceSHA, o.mainSHA, o.baselineSHA));
    await json(path.join(o.output, "inputs-after.json"), after);
    report.inputsUnchanged = before.inputSHA256 === after.inputSHA256;
    if (!report.inputsUnchanged) report.exitCode ||= 1;
    verifyCheckout(o.repo, o.sourceSHA);
  } catch (error) {
    report.error = error.stack;
    report.exitCode ||= 1;
  } finally {
    await json(path.join(o.output, "shard-result.json"), report);
  }
  return report.exitCode;
}

if (process.argv[1] && path.resolve(process.argv[1]) === SELF) {
  main(process.argv.slice(2)).then((code) => { process.exitCode = code; }, (error) => {
    console.error(error);
    process.exitCode = 1;
  });
}
