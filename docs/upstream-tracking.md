# Magpie Next 上游同步与 Windows 发版手册

Magpie Next 的发布仓库是 [xue-ding-e/Magpie-Next](https://github.com/xue-ding-e/Magpie-Next)，上游是 [yetone/magpie](https://github.com/yetone/magpie)。本手册以 Windows x64 稳定版为发版目标，也可以直接交给维护代理执行。仓库的 [AGENTS.md](../AGENTS.md) 和 [代码标准](code-standards.md) 仍适用于源码修改。

## 维护代理的工作范围

- 当前批次先固定一个上游标签和完整 SHA。完成这批的验证和发版，再处理后来出现的上游版本。
- 持续值守由维护者安排。同一时间只维护一个待发布批次。发现新标签时记录 SHA 和变更摘要。没有待处理差异时，结束本轮并等待下一次调度。
- 本机只验证当前操作系统。Windows 跑 Windows 编译和原生检查，Linux 跑 Linux 检查，macOS 跑 macOS 检查。运行代理无需模拟、跨编译或手工复测另外两个平台。Windows 发布包由 GitHub 的 Windows runner 产出。
- 本机验证与仓库发布门禁各自生效。当前 [发布工作流](../.github/workflows/next-windows-package.yml) 依赖既有 [Go 平台矩阵](../.github/workflows/test.yml) 和 [完整 Windows 双浏览器分片](../.github/workflows/test-ui.yml)。由 GitHub Actions 执行这些作业；如果将来调整发布门禁，应让工作流和本手册同步更新。
- 在独立 worktree 操作，保留别人的分支、暂存区和测试记录。Git 历史保持线性，禁止 git merge、会隐式合并的 git pull、改写远端历史和强推。

## 1. 固定上游和 Next 主线

在仓库根目录检查 remote。缺少 upstream 时再添加，已有同名 remote 时先核对地址。

~~~sh
git remote -v
# 只有缺少 upstream 时才执行下一行
git remote add upstream https://github.com/yetone/magpie
git fetch --no-tags origin refs/heads/main:refs/remotes/origin/main
git ls-remote --tags upstream 'refs/tags/v0.1.*'
~~~

选择本轮发布标签后，从上游取该标签，解析为 commit，再记录两边 SHA 和上游树。下文的 v0.1.4104 是本轮示例，下一批换成当次选定标签。

~~~bash
set -euo pipefail
UPSTREAM_TAG=v0.1.4104
git fetch --no-tags upstream "refs/tags/$UPSTREAM_TAG"
UPSTREAM_SHA=$(git rev-parse --verify 'FETCH_HEAD^{commit}')
UPSTREAM_TREE=$(git rev-parse --verify 'FETCH_HEAD^{tree}')
MAIN_SHA=$(git rev-parse --verify 'origin/main^{commit}')
printf '%s\n' "$UPSTREAM_TAG" "$UPSTREAM_SHA" "$UPSTREAM_TREE" "$MAIN_SHA"
~~~

从最近的完整同步提交说明取 Upstream-Commit 和 Upstream-Tree。用上游两个 SHA 比较差异，查看被修改的路径、调用点和已有测试。Next 使用整树快照，两个仓库的提交计数不能直接表示落后多少。git diff --diff-filter=D --name-only <上游SHA> <候选SHA> 用来找完整同步中缺失的上游文件；缺失项须能逐项解释。

先检查更新是否已被 Next 或上游其他实现覆盖。保留 Next 的本地词库及屏蔽优先级、账号编辑器、更新源和完整性校验、发布工作流，以及 main 上尚无上游等价实现的修复。曾有整树替换丢失 pii.FindContext 和脱敏 15 秒上限的事故。继续保留 [上游能力检查](../.github/workflows/upstream-alignment.yml)，并用实际回归确认行为。

## 2. 生成单父候选

**完整同步**时，从记录的 origin/main SHA 新开独立 worktree。将已审查上游 commit 的完整树做成单父快照，唯一父提交是记录的 Next main。提交说明写明 Upstream-Repository、Upstream-Commit、Upstream-Tree。下面的 Bash 命令沿用上一步的变量。Windows 可在 Git Bash 执行 Git 命令，并用 PowerShell 运行 Windows 验证。

~~~bash
set -euo pipefail
CANDIDATE_DIR=../magpie-next-candidate
CANDIDATE_BRANCH="codex/next-$UPSTREAM_TAG"
git worktree add --detach "$CANDIDATE_DIR" "$MAIN_SHA"
SNAPSHOT_SHA=$(git -C "$CANDIDATE_DIR" commit-tree "$UPSTREAM_TREE" -p "$MAIN_SHA" \
  -m "chore(upstream): 同步原版至 $UPSTREAM_TAG" \
  -m "Upstream-Repository: https://github.com/yetone/magpie
Upstream-Commit: $UPSTREAM_SHA
Upstream-Tree: $UPSTREAM_TREE")
git -C "$CANDIDATE_DIR" switch -c "$CANDIDATE_BRANCH" "$SNAPSHOT_SHA"
test "$(git -C "$CANDIDATE_DIR" rev-parse 'HEAD^{tree}')" = "$UPSTREAM_TREE"
test "$(git -C "$CANDIDATE_DIR" rev-parse HEAD^)" = "$MAIN_SHA"
~~~

在快照上逐项应用 Next 的差异并审核冲突。比较上一轮的完整基线和远端 main 来找下游改动，将仍有必要的改动应用到新树。已有对齐分支可以提供实现和测试，但仍须核对最终候选的调用路径、可执行文件模式、嵌入资源和完整测试清单。把 Next 改动作为快照的直接子提交。浏览器工作流使用该父提交作为独立的桌面截图基准。应用完改动后，在候选 worktree 中检查并提交明确的文件清单，禁止将构建产物或隔离测试目录加入提交。

~~~bash
cd "$CANDIDATE_DIR"
git status --short
# 把下面的文件名换成本轮审查过的实际路径
git add -- path/to/reviewed-file
git diff --cached --check
git diff --cached --stat
git commit -m 'chore(next): 保留 Next 改动并更新发版基线'
~~~

**只同步部分上游改动**时，从已核对的 Next 基线开始，只应用选中的提交或文件级补丁。记录完整基线、选入的提交以及尚待审查的提交。候选的版本基数和快照说明应对应实际使用的完整上游树。新选入的改动经过独立验证后，再进入候选。全量基线升级时同步更新发版工作流的 NEXT_BASE_VERSION。新增或删除 UI 测试文件时，从完整文件清单重新核算分片数量，在工作流、分片器及其契约测试中同步更新总数。

推送前确认每条新增提交只有一个父提交，且远端 main 仍是祖先。git merge-base 仅用于查询祖先关系。

~~~bash
set -euo pipefail
CANDIDATE_SHA=$(git rev-parse --verify 'HEAD^{commit}')
git diff --check "$MAIN_SHA" "$CANDIDATE_SHA"
git log --format='%H %P %s' "$MAIN_SHA..$CANDIDATE_SHA"
git merge-base --is-ancestor "$MAIN_SHA" "$CANDIDATE_SHA"
test -z "$(git rev-list --merges "$MAIN_SHA..$CANDIDATE_SHA")"
test "$(git ls-remote origin refs/heads/main | cut -f1)" = "$MAIN_SHA"
~~~

祖先查询需退出 0，新增合并提交列表应为空。远端 main 已变化时，先把新增的下游工作纳入新候选，再按新 SHA 验证。

## 3. 本机验证

Go 版本以 go.mod 为准。浏览器依赖读 .github/ui-preview/package.json，Node 版本读 .github/workflows/test-ui.yml。测试前记录源码 SHA 和树，使用临时 HOME、配置和状态目录。Windows 同时隔离 USERPROFILE、APPDATA、LOCALAPPDATA 和 TEMP。Go 缓存路径先通过 go env 取得再注入测试进程。带真实桌面焦点、遮挡或 WebView2 重启的测试需要对应环境，记录它实际执行的子用例及结果。

| 代理当前环境 | 本机做的事 |
| --- | --- |
| Windows | 本机 go vet、受影响 Go 测试、Windows 包编译和对应原生检查。UI 改动跑受影响的 Chromium 与 WebKit 用例。需要图形会话的重启绘制用例在受控的交互桌面运行。 |
| Linux | 验证受影响的 Linux 包和功能。涉及原生 GUI 时按工作流安装 GTK 3 和 WebKitGTK 4.1，使用 gtk3 标签。无图形环境可跑 nogui 范围，记录原生 GUI 尚待哪项检查。 |
| macOS | 验证受影响的 macOS 包和功能。涉及原生 GUI 时使用本机工具链和 WebKit。需要真实桌面时使用受控的交互会话。 |

以本次 UI 和词库改动为例，在已隔离的当前平台执行以下用例。Windows 的更新安装和回滚用例使用临时二进制与合成服务。

~~~sh
go test -count=1 ./internal/gui -run 'TestCoreScriptBudget|TestLocalDictionary'
go test -count=1 ./internal/localdictionary ./internal/redact
go test -count=1 ./internal/gateway ./internal/backup -run 'TestLocalDictionary'
node --test --test-concurrency=1 build/ui-test-shard.test.mjs
~~~

~~~powershell
# Windows x64 原生发布管道夹具。在隔离的 PowerShell 子进程内执行。
go test -mod=readonly -tags 'nogui,next' -count=1 -list '^TestNextNativeUpdatePipeline$' ./internal/update
go test -mod=readonly -tags 'nogui,next' -count=1 -run '^TestNext' ./internal/update
go test -mod=readonly -tags 'nogui,next' -count=1 ./internal/update/nextrelease
~~~

Windows 本机浏览器示例。安装前读仓库声明的版本。将 BASE_REF 设为已核对的基准提交，完整同步使用上游快照父提交，普通候选使用固定的远端 main 快照。CI 的选择逻辑见 [分片器](../build/ui-test-shard.mjs)。命令使用 Chromium 和 WebKit 两个引擎，保留真实点击、拖动和原始布局断言。

~~~powershell
npm install --prefix .github/ui-preview --ignore-scripts --no-audit --no-fund --package-lock=false
& .github/ui-preview/node_modules/.bin/playwright.cmd install chromium webkit
$env:NODE_PATH = (Resolve-Path .github/ui-preview/node_modules).Path
$env:BASE_REF = '填入独立截图基准的完整SHA'
node --test --test-concurrency=2 internal/gui/tests/local-dictionary.test.cjs internal/gui/tests/surface-resize.test.cjs internal/gui/tests/account-models.test.cjs internal/gui/tests/mobile-web.test.cjs
~~~

本地试构建可以用独立的 -next.local 版本，正式版本号由工作流生成。

~~~powershell
$taskBuild = Join-Path ([System.IO.Path]::GetTempPath()) ('magpie-next-' + [guid]::NewGuid())
New-Item -ItemType Directory -Path $taskBuild | Out-Null
go build -mod=readonly -trimpath -tags 'production,next' -ldflags '-s -w -X main.version=0.1.4104-next.local -H windowsgui' -o (Join-Path $taskBuild 'Magpie-Next.exe') .
if ($LASTEXITCODE -ne 0) { throw 'Desktop build failed' }
go build -mod=readonly -trimpath -tags 'nogui,next' -ldflags '-s -w -X main.version=0.1.4104-next.local' -o (Join-Path $taskBuild 'magpie-next-cli.exe') .
if ($LASTEXITCODE -ne 0) { throw 'CLI build failed' }
& (Join-Path $taskBuild 'magpie-next-cli.exe') --version
~~~

当前发布流程不需要生成 packedassets 包，也不要求覆盖用户机器已安装的程序。当前界面验收包括白名单与屏蔽词重叠时只保留白名单片段、保存后重启仍可读回、两种编辑器均可拖动和键盘缩放，以及中文账号三项限额在 420px 下同排。任何平台跳过、异常或合成测试的边界都应写入批次记录。

## 4. 推候选并触发 CI/CD

有仓库写入权限和本批次发布授权的维护者，使用新候选分支进行普通推送。执行前检查有效的 pre-push 钩子及 remote，执行后用 ls-remote 核对远端的完整 SHA。

~~~bash
set -euo pipefail
NEXT_REPO=xue-ding-e/Magpie-Next
git push origin "$CANDIDATE_SHA:refs/heads/$CANDIDATE_BRANCH"
test "$(git ls-remote origin "refs/heads/$CANDIDATE_BRANCH" | cut -f1)" = "$CANDIDATE_SHA"
gh workflow run next-windows-package.yml -R "$NEXT_REPO" --ref "$CANDIDATE_BRANCH" -f publish=false
gh run list -R "$NEXT_REPO" --workflow next-windows-package.yml --branch "$CANDIDATE_BRANCH" --commit "$CANDIDATE_SHA"
# 从列表取本次 workflow_dispatch 的 ID，再跟踪该运行
read -r RUN_ID
gh run watch "$RUN_ID" -R "$NEXT_REPO" --exit-status
~~~

也可以在 GitHub Actions 选择 **Next Windows portable package → Run workflow**，选择候选分支并保持 Publish 关闭。候选运行可验证和打包，正式发布仅发生在 main。普通候选分支 push 本身不会触发此发布工作流。首次引入工作流时须先核对默认分支是否已注册该工作流的手动入口。

记录 run ID、head SHA 和截图基准 SHA。工作流可能先排队，再按配置并行或串行跑分片。轮询同一个 run ID；超时、没有新日志或 pending 本身不能证明失败。通过的依据是当前 SHA 的作业结论和产物，其中包括：

- impact 识别本批次的改动类别。
- validation 执行仓库 Go 检查。UI 工作流校验每个分片和完整清单，截图使用已固定的独立基准。
- package 校验 Next 原生更新和发布契约，构建 Windows x64 桌面程序及 CLI，上传同一 SHA 的制品。
- 失败时下载同一 run 的日志和分片报告，修正原因后用新 SHA 重验。保留原断言、文件清单、签名及来源检查。

候选通过且远端 main 仍是其祖先时，获授权的发布者可做普通快进推送。推之前再确认 main 和候选 SHA。不要用 GitHub 的合并按钮、强推或重置替代快进。

~~~bash
set -euo pipefail
CURRENT_MAIN_SHA=$(git ls-remote origin refs/heads/main | cut -f1)
test "$CURRENT_MAIN_SHA" = "$MAIN_SHA"
git merge-base --is-ancestor "$CURRENT_MAIN_SHA" "$CANDIDATE_SHA"
git push origin "$CANDIDATE_SHA:refs/heads/main"
test "$(git ls-remote origin refs/heads/main | cut -f1)" = "$CANDIDATE_SHA"
gh run list -R "$NEXT_REPO" --workflow next-windows-package.yml --branch main --commit "$CANDIDATE_SHA"
~~~

main 的 push 自动触发正式工作流。产品输入发生变更时，impact 允许打包和发布；纯文档及已归类的界面测试改动仍验证，但通常不会发布新包。同一 SHA 如果需要获授权的手动发版，可选 main 并传 publish=true。此入口同样经过验证和打包。

~~~sh
gh workflow run next-windows-package.yml -R xue-ding-e/Magpie-Next --ref main -f publish=true
~~~

## 5. 核对 Windows 发布产物

候选制品可用于试用。正式发版完成时，验证 main 上对应 SHA 的 publish 作业成功，以及 GitHub Releases 上的发布条目已经公开且标签解析到同一个 commit。工作流会在发布前回读 main，main 已前进时可能保留上一版或留下草稿，应以 Release 的真实状态为准。

~~~bash
NEXT_TAG="next-${CANDIDATE_SHA:0:12}"
# 填入 main 的正式发布运行 ID
read -r RELEASE_RUN_ID
gh run view "$RELEASE_RUN_ID" -R "$NEXT_REPO" --json headSha,status,conclusion,jobs,url
gh release view "$NEXT_TAG" -R "$NEXT_REPO" --json tagName,isDraft,assets,url
git ls-remote origin "refs/tags/$NEXT_TAG" "refs/tags/$NEXT_TAG^{}"
RELEASE_DIR=$(mktemp -d)
gh release download "$NEXT_TAG" -R "$NEXT_REPO" --dir "$RELEASE_DIR"
~~~

核对 Windows x64 ZIP、Magpie-Next.exe、magpie-next-cli.exe、manifest.json 和 SHA256SUMS。用 Get-FileHash 或 sha256sum -c SHA256SUMS 复算，校对压缩包内外的两个可执行文件及 manifest。manifest 的 source_sha、repository、platform、version 和 sequence 应与发布提交、仓库、平台和工作流编号吻合。版本形如 0.1.4104-next.<运行编号>，标签形如 next-<源码SHA前12位>。当前 Windows 制品以清单 SHA-256 校验，manifest 标注 sha256-unsigned。Windows 上的真实打开、升级和重启如果需要验收，使用隔离的 Windows 环境并记录结果。用户数据和正式安装由使用者独立管理。

Next 更新源由 [distribution_next.go](../internal/update/distribution_next.go) 固定为本仓库。更新顺序使用发布 sequence，源码试构建的 -next.local 版本不参与自动替换。

## 6. 交接记录

每批保留下列字段即可交给下一位代理。记录可附在变更说明中，详细日志和截图使用 CI 制品或本批测试目录。

~~~text
上游完整标签 / commit / tree
已审查的上游截止 SHA / 已选入的变更 / 待审查的变更
Next 原 main SHA / 快照 SHA / 候选 SHA / 截图基准 SHA
本机平台 / 执行的检查 / 失败、跳过和对应日志
候选运行 ID / main 运行 ID / 对应 SHA
Windows Release 链接 / manifest 版本 / 两个可执行文件 SHA-256
~~~

已交付的 SHA 后面有新上游版本时，沿用本手册开下一批，当前批次的发布记录和源码身份可以独立复核。
