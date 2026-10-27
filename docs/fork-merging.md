# Fork 与上游的合并（codex-images 维护手册）

本 fork（qq33357486/magpie-pr，分支 codex-images）长期跟随上游 yetone/magpie。
每晚 03:00（北京时间）`merge-upstream.yml` 自动把 upstream/main 合进来，
测试绿了才推，推送后 docker.yml 自动出镜像 `ghcr.io/qq33357486/magpie:codex-images`。

**目标：让夜间合并尽量自动过关。** 冲突越少，CI 越能自己推；每次冲突都要人工解，
拖一天就落后一天。本手册是为此定的写法约束和解冲突流程。

## 挂载原则：能挂载就挂载

fork 的改动分三层写，层次越靠前，将来越不冲突：

**第一层：fork 专属文件（零冲突）。**
新函数、新常量、新测试一律放上游不存在的文件里（如 `internal/gateway/fork_errors.go`、
`draw_lane.go`）。上游永远不会碰这些文件，合并时 git 直接跳过。
同一 package 内跨文件调用没有任何运行时代价。

**第二层：共享文件里的单行钩子（低冲突）。**
必须在共享文件的流程里接线时，把改动收敛成一行调用，函数体放第一层文件。
一行钩子旁边的上游改动能自动合并过去；整块插入才会撞。
范本：`gateway.go` 里的 `cappedErrorTold(...)`、`sealedTaskTold(...)` 调用点。

**第三层：原地字符串替换（接受偶发冲突）。**
上游重写客户可见的报错原文、我们必须跟着重新中文化时，只换字符串字面量，
不动结构。这类冲突解法固定：采纳上游新结构，把文案翻成中文。

**红线（冲突的主要来源，历史教训）：**

- 不要在共享文件里成块插入逻辑——挪去第一层文件，留一行钩子。
- 不要重排、重格式化（gofmt）上游的代码块；每一行无谓的 diff 都是未来的冲突面。
- 不要改上游函数的内部实现来达到我们的目的——包一层 twin（见下）。

## 客户文案：Told 双胞胎模式

本 fork 的产品要求：付钱的陌生客户只读中文、且看不到任何内部信息
（账号地址、供应商名、配额百分比、Settings 页面指引、magpie 这个名字本身）。
上游的报错是英文的、且写给操作者。两者用 twin 桥接，全部住在
`internal/gateway/fork_errors.go`：

```go
// 上游函数保持英文原样（细节给日志）；twin 负责中文给客户
func cappedErrorTold(model string, ws []Weighed, now time.Time) (string, time.Time) {
    held, soonest := heldReasons(ws, now)      // 复用上游的结构化数据
    log.Printf("usage cap: %s: %s", ...)        // 细节进日志
    return fmt.Sprintf("模型 %s 的服务额度暂时用尽%s，请稍后重试", ...)  // 中文给客户
}
```

- 上游改英文文案/内部逻辑 → twin 不受影响，自动合并。
- 上游新增客户可见报错 → 测试会红（上游测试断言英文），此时加一个 twin、
  调用点换一行，再把对应测试断言改成中文。
- 咽喉点 `writeErrorSaid`（所有错误出口）统一过 `toldOutside` 脱敏网
  （屏蔽邮箱与 magpie 字样）；协议事件出口 `streamError` 同样过网。

## CI 链路与三个坑（2026-10-17 修）

1. **workflows 权限**：上游会改自己的 workflow（如 release.yml）。合并提交里
   带上 workflow 变更时 GITHUB_TOKEN 会被拒推（10-14 夜间合并就是这么断的，
   且表面看不出来）。注意 `permissions:` 块里**写不进** `workflows` 键——
   那不是 GITHUB_TOKEN 的合法 scope（10-17 写进去后整个 workflow 解析失败，
   push 触发 0 秒报红、schedule 停注册，停摆两天）。修法是 merge 成功后
   `git rm -r .github/workflows` 再 `git checkout ORIG_HEAD -- .github/workflows`
   把 fork 的版本整体恢复：push 永不带 workflow 变更，fork 的 workflow 本就
   自维护。**必须先 rm 再 checkout**——只 checkout 钉不住上游**新增**的
   workflow 文件（它会留在合并提交里，push 照样被拒、当夜停摆；10-19
   临时仓库实验证实）。
2. **冲突必须报红**：早先冲突时靠"开 issue"通知，但仓库 issues 是禁用的，
   `|| true` 把失败吞了，CI 显示绿色 success。已改为冲突直接 `exit 1`。
3. **test.yml 红则夜间合并不推**：merge-upstream 只跑 gateway/agent/gui 三个
   包的精简套件，推上去以后 test.yml 跑全量。任何一侧红都要当天修，
   否则夜间合并停摆、镜像停更。

## 解冲突 SOP

1. `git fetch upstream && git merge upstream/main --no-ff --no-commit`
2. `git diff --name-only --diff-filter=U` 列冲突文件，逐个看 `<<<<<<<` 块。
3. 判读每一处：上游在改什么（结构/逻辑，通常要）？我们在改什么（文案/接线，
   换成 twin 或保留单行）？
4. 按三层原则解：结构取上游，中文渲染进 `fork_errors.go`，调用点留单行。
5. `go build ./...` → `go test ./internal/gateway/ ./internal/agent/ -count=1`。
6. 测试红了先分清：上游新断言 vs 我们的中文输出（改断言为中文）；
   真逻辑坏了回去查解法。
7. 提交合并（`--no-edit` 默认信息即可），单独提交再放 CI/文档类改动。
8. 推送（origin 与 magpie-pr 是同一仓库的改名重定向，推一处即可）。
9. 盯 docker.yml 出镜像，再按服务器手册拉镜像上线。

## 已知遗留

- `emptyReply` 常量仍是英文（057da9c2 扫描时漏网，客户可见），下次顺手中文化。
- 上游 `saysPassing` 会在中文文案后追加英文 "(429: you can retry your request)"，
  该英文短语是给 Pi 等客户端的重试识别模式，不能翻译，属有意保留。

## compaction 标记的 twin（2026-10-22 upstream 抽包）

上游把 compaction 标记抽成了 `internal/codexsummary` 包（`Mark = "magpie1:"`），
gateway 的读写、`sessions/codex_portable.go` 的 rollout 可移植化都用它。fork 的
脱敏标记 `gw1:` 与 legacy 兼容（`magpie1:`，升级前的会话仍要读回）不能丢，于是
在**该包内**加了 fork 专属文件 `codexsummary/fork_marks.go`（第一层：上游永不碰）：
`ForkMark` / `ForkEncrypted`（写 gw1:）/ `ForkMessage`（读双前缀）/ `ForkWritten`
（扫描判断）。共享文件里的引用点（gateway 写点、读回、快扫、native_compaction
排除、portable 逐行快扫）各留一行调 twin。教训：**合并时 `codexsummary.Encrypted`
写点会被 upstream 自动合并进来（写读劈叉）**——解完显式冲突块后必须全局搜
`magpie1:`、`Encrypted(` 复核写读口径一致。

（10-27 增补：上游又加了 `claudeseal` 包——Claude 自己的 compaction 标记，与 fork
无关、不需要 twin；但 `portLine` 的早退检查从此是**三标记**（ForkMark/magpieMark/
claudeMark）。同批上游新增 `sessions/carry_convert.go` 直接调 `codexsummary.Message(`
漏了我们的 gw1:——已在调用点一行换 `ForkMessage(`。读点扫描范围扩大为：
`codexsummary.Message(` / `Encrypted(` / `Written(` 全局 grep。）

## Windows 本地测试红名单（以 Linux CI 为准）

`go test` 在 Windows 本机有一批环境性失败/卡死，全部与代码无关、Linux CI 全绿：

- gateway：`TestClaudeMemRunsFromTheCgroup`/`UnknownBoundsNothing`/`RunsMostTheUsersOwn`
  （上游 #2956：`filepath.Dir` 到不了 `/`，死循环拖满 10 分钟超时——本地跑须
  `-skip "MemRuns|RunsMost"`）；`TestGrokCallsGiveIntegersOnEveryProtocol`（流 mock
  卡死）；`TestSweepBridge…`（建符号链接要特权）；`TestClaudeAsRootEnv`（env 排序）；
  `TestHandedOverGatewayFinishesItsStream`（socket handover 报 Windows 设备错）；
  `TestMaskedRecordKeepsSentAndBack`（`Took` 记 0：Windows 时钟粒度，Linux CI 绿）；
  `TestSlowStartsRestTheModelOnItsAccount`（上游 398803e3 新增，流 mock 卡死）。
- agent：keychain（Windows 凭据管理器真读）、`TestCopilotFolder…`/`TestQoderSettings…`
  （PATH 上的 CLI fixture 不可执行，后者合并前即红）、`TestClaudeDesktop…`（AppData
  结构）、`TestIsMagpieKept…`（fs 分配数 256 > 阈值 200）、`TestAsideOffline…`（CLI
  fixture）、`TestCodexSignInInKeychain`、`TestWorkBuddyRestartOnMac`（在 Windows 进程
  表里找 Mac 的 .app 路径，10-25 上游 d08b6118 新增）、`TestCodexSyncMendsGoneCatalog`
  （Windows 盘符路径写回 config.toml 的格式差异，10-25 上游 ef6f3749 新增）、
  `TestWorkBuddyOwnSaveAfterStartIsNoChange`（Mac 重启模拟，同 d08b6118 族，10-27 新增）、
  `TestOpenLodyRunsItsDesktopEntry`/`TestDesktopExec`（Linux desktop entry 域：文件名带
  引号在 Windows 非法、/bin/sh 解析到 Git 的 sh.exe，10-27 新增）、
  `TestHermesProfilesFollowMagpieModel`（全量跑偶发，单跑绿）。
- sessions（10-27 新增整包红名单）：`TestAgySessions`/`TestAgyRowOnly`（Antigravity CLI
  列表，路径分隔符 `\` vs `/`，上游 b30734f9 新增）；`TestOneListing`/`TestShareListing`
  （上游 51a2068b 新增：`one_listing.go` 的 `l.at.Before(held.since)` 在 Windows 时钟
  粒度下同 tick 判不出"新 hold 该重列"，TestShareListing 的卡死是它失败后泄漏 hold 的
  级联——两测试须一起 skip）；`TestCursorEditorCopiedOncePerChange`（10-27 合并前即红）。
- usage：`TestDesktopSessionAccountMetadata`（10-27 合并前即红）。
- **全量套件非确定性卡死**（10-27 定案）：gateway/agent 全量跑会在后段随机卡死在某个
  测试上（每次不同、0s 起卡），**ORIG_HEAD 合并前同样复现**，与任何合并无关。本地验证
  只跑 `-run` 定向测试，全量绿不绿交给 Linux CI 判。

## 上游自身的红（fork 侧当天修）

上游 3 个 Claude Code loop 提交不经全量门禁，会推红的测试过来（test.yml 红则夜间合并
停摆）。已遇到：`TestFolderVarsAreListed`（e4552cca 给 `library/rtk_upgrade.go` 加了
`os.Getenv("CARGO_HOME")` 却没登记，纯 upstream/main 实测红）——fork 已在
`agentenv_test.go` 的 `notAnAgent` 补 `CARGO_HOME` 一条。遇到"合并树上单跑红、纯
upstream/main 也红"的测试，照此办理：先在 upstream/main worktree 上复现实锤，再在
fork 侧最小修（登记/断言），勿当自己的回归排查。

上游还有一类**跑起来才红的 flaky**（synctest 崩溃带崩整包、`ps failed`、environ 读空），
本地 Windows 全 skip 或全绿、无法复现，只能在 CI 上观察到互换红。这类已进 test.yml
全量步骤的 `-skip` 名单（TestUnreadLoginsWriteErrorsKeepCredentials＝上游自报 #3632、
TestCodexAppServersOnHome、TestEnvOfChild），上游修好后从名单里拿掉。tui 的
TestTUIFetchesSubscriptionModels/agent 也是上游自身红（add927e6 改了 agent picker 的
列表、裸名不在选项里），fork 侧把该子测试期望对齐为 `codex/` 前缀并注释了缘由。
