# 跟随上游（upstream）的维护流程

本仓库 [xue-ding-e/Magpie-Next](https://github.com/xue-ding-e/Magpie-Next) 是
[yetone/magpie](https://github.com/yetone/magpie) 的下游：上游在持续快速前进，
Next 在其之上做自己的改动。这份文档说清楚**遇到上游领先时该怎么做**，以及
**为什么不能靠一条 main 一直 cherry-pick 下去**。

写这份文档的直接原因：2026-11-13 发现 `main` 落后上游 3734 个提交、缺少 3850
个上游文件，而一次「同步原版」的整树替换把一项已修好的脱敏修复（`874b86653`，
脱敏模型最多等 15 秒、读到多少用多少）连带删掉了。表现是网关日志里 3108 条
`in 2s` 超时，以及大量请求被「脱敏失败，什么都没有发出」拦下。

## 一、核心判断：先量树，别看 rev-list

`git rev-list --left-right --count` 在整树重建的分支上没有意义：Next 用上游
**完整文件树**建立基线（见 `30698715e` 的提交说明与 `Upstream-Tree` 行），
旧历史被有意丢弃，所以 behind 永远是大数字，即使内容已经对齐。

判断「落后多少」要看**内容**：

```powershell
# 上游有、我们没有的文件数（这个才是落后量）
git diff --diff-filter=D --name-only upstream/main main | Measure-Object

# 我们改了多少上游文件（这才是我们的改动量）
git diff --name-only upstream/main main | Measure-Object

# 我们独有的文件（Next 自己新增的能力）
git diff --diff-filter=A --name-only upstream/main main
```

2026-11-13 对齐后的实测：缺失上游文件 **0**，差异文件 **191**，Next 独有
**108**。对齐前缺失 **3850**。

## 二、怎么做：找现成的上游对齐分支，别硬 merge

**不要**把 `upstream/main` merge 进 `main`。理由是 Next 的基线是整树快照，
两条历史没有共同的近期祖先，merge 会产生数千个冲突，且把上游历史带进来后再
也分不清哪条是自己的改动。

**也不要**逐个 `cherry-pick`：这次实测 12 个 main-only 提交，12 个全部冲突，
因为它们的旧基线文件在上游已被重写或删除。

正确做法是**找一条已经建在新上游基线上的分支**，把 main 独有但那条分支还没有
的工作补过去，然后让 main 指向它。

```powershell
# 1. 找候选：谁的树离上游最近，且已经包含上游
foreach ($b in (git for-each-ref --format='%(refname:short)' refs/heads refs/remotes/origin)) {
  $missing = (git diff --diff-filter=D --name-only upstream/main $b | Measure-Object).Count
  $differs = (git diff --name-only upstream/main $b | Measure-Object).Count
  "{0,-46} missing={1,-5} differs={2}" -f $b, $missing, $differs
}
# 挑 missing=0 且 differs 最小的那条
```

2026-11-13 的结果：`codex/upstream-ui-20261112` 满足 missing=0、differs=191，
并且**已经包含** `874b86653` 的脱敏修复（`FindContext` + `strictWait = 15s`）。
main 则是 missing=3850。所以最优解是让 main 指向这条分支线，而不是修 main。

## 三、怎么把 main 独有的工作补过去

先列出 main 独有、候选分支没有的提交与文件：

```powershell
$b = 'codex/upstream-ui-20261112'
$shas = @()
foreach ($c in (git rev-list main --not upstream/main)) {
  git merge-base --is-ancestor $c $b 2>$null
  if ($LASTEXITCODE -ne 0) { $shas += $c }
}
# 这些提交创建、而候选分支没有的文件
foreach ($c in $shas) { git show --diff-filter=A --name-only --format='' $c }
```

然后**逐个文件判断这项能力是否仍然缺失**——很多能力在上游那棵更新的树里已经
以别的名字实现了：

```powershell
# 这个函数/变量在候选分支里有没有等价物？
git grep -n '关键函数名' HEAD -- 'internal/相关包/*.go'
# 上游自己的测试是否已经覆盖了这个行为？
go test -tags nogui -count=1 -run '上游对应测试名' ./internal/相关包/
```

这次的关键发现：main 的 `tool_arguments.go`（工具参数按 JSON 脱敏）在上游已有
`walk`/`keepIn`/`namedSecret` 机制 + `TestEveryToolCallsArgumentsAreMasked` 覆盖
同一类问题，**测试通过**。硬搬 `tool_arguments.go` 反而会失败，因为上游的
`MaskJSON` 已经重写。所以那一项不用搬。

真正需要搬的只有 `b3a5efe04` 的 Codex 请求体字节预算（上游没有等价物）：
`codex_body_budget.go` + `responses_wire.go` 及其调用点。

**判断标准**：搬过去的文件必须有调用点，否则就是死代码。用这个检查：

```powershell
# 搬来的函数真的有 caller 吗？callers=0 说明搬错了
git grep -l '\bfuncName\b' HEAD -- 'internal/**/*.go'
```

## 四、执行对齐

```powershell
# 0. 先备份（force 之前必须）
git branch "backup/main-before-upstream-align-$(Get-Date -Format yyyyMMdd-HHmmss)" main
git tag    "backup-main-$(Get-Date -Format yyyyMMdd-HHmmss)" main

# 1. 建工作分支 + 独立 worktree（别在 main 的 worktree 里折腾）
git branch -f codex/align-upstream-<日期> <候选分支>
git worktree add D:\_code\_codex_worktrees\magpie-align-upstream-<日期> codex/align-upstream-<日期>

# 2. 在 worktree 里补齐缺失工作，逐项验证
go build ./... && go vet ./internal/受影响包/
go test -tags nogui -count=1 -run '<相关测试>' ./internal/受影响包/

# 3. 让 main 指向对齐结果（main 被 worktree 占用时先 checkout 再 reset）
git checkout main
git reset --hard <对齐后的提交>

# 4. force push（用 --force-with-lease，lease 值从变量取，别硬编码）
$expected = git rev-parse origin/main
git push --force-with-lease="main:$expected" origin main
```

> `--force-with-lease` 的值必须从 `git rev-parse` 取变量。直接把 40 位 sha
> 写进命令行会被本机的脱敏层改写，导致参数解析失败。

## 五、对齐后必须核对的三件事

1. **上游文件缺失数 = 0**：`git diff --diff-filter=D --name-only upstream/main main`
2. **关键修复还在**：这次丢失的是脱敏 15s 上限，用
   `git grep -c 'FindContext' HEAD -- internal/pii/find.go` 和
   `git grep -n 'strictWait = 15' HEAD -- internal/masking/masking.go` 确认。
3. **运行时行为对**：装上新 build 后，网关日志应出现 `in 15s` 而不是 `in 2s`：
   ```powershell
   Select-String -Path "$env:LOCALAPPDATA\magpie\logs\app.log" -Pattern 'in 15s' | Select-Object -Last 3
   ```

第 3 条是唯一能证明「修复真的进了二进制」的证据，前两条只证明源码里有。

## 六、构建与安装（本机）

```powershell
# 版本号用 git describe（含 -next 的发布号，或纯 hash 的源码构建号）
$v = (git describe --tags --always) -replace '[^A-Za-z0-9._+-]',''

# 资源包必须先打，否则 packedassets 构建报 assets.zip 缺失
go run ./build/assetpack internal/gui/assets internal/gui/assets.zip

go build -tags "production packedassets" -trimpath -ldflags="-s -w -X main.version=$v" -o magpie-new.exe .
```

安装：停 `magpie` / `magpie-bun` → 备份 `D:\Program Files\magpie\magpie.exe`
→ 覆盖 → 重新启动。

## 七、更新器为什么不会把我们自己构建的版本降级

这一条容易被误解，代码依据在 `internal/update`：

- **更新源已经是本仓库**，不是上游：`distribution_next.go` 里
  `nextFeed = https://api.github.com/repos/xue-ding-e/Magpie-Next/releases/latest`，
  `Site` 同仓库。源码构建同样走这里（`nextDistribution = true` 无条件生效）。
- **只有落后才更新**：`update.go` 的 `Newer(a, b)` 比较远端版本与运行版本，
  `gui/update.go:500` 的 `!update.Newer(rel.Version, Version)` 为真就置
  `state = "latest"` 直接返回；远端不比本地新就**不会下载**。
- **源码构建永远不会被覆盖**：`update.Released(v)` 对含 `-next` 但
  `VersionSequence == 0` 的版本返回 false，`gui/update.go:497` 于是置
  `state = "source"` 返回。所以本地 `go build` 出来的产物既不会误报落后，
  也不会被自动替换。

即：本地构建与 CI 构建走同一条更新链路，判定标准一致——**比远端旧才更新**。

## 八、下次再遇到上游领先时，按这个顺序做

1. `git fetch upstream`，用第一节的命令量**内容**差距（别信 rev-list）。
2. 用第二节的循环找一条 `missing=0` 且 `differs` 最小的现成分支。
3. 有——就到第三节，把 main 独有但候选缺的补过去；没有——才需要新建一条
   以上游整树为基线的分支。
4. 补的时候**逐项验证是否真的缺失**（上游常已用别的实现覆盖同一行为）。
5. 备份 → 对齐 → 核对三件事 → force push → 构建安装 → 看运行时日志。
6. 把这次踩到的新坑补进本文档，别只写在提交信息里。
