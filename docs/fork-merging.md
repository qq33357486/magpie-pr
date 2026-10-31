# Fork 与上游的合并（codex-images 维护手册）

本 fork（qq33357486/magpie-pr，分支 codex-images）跟随上游 yetone/magpie。
每晚 03:00（北京时间）`merge-upstream.yml` 自动把 upstream/main 合进来，
**只要能编译就推**（不再跑上游测试套件）；推送后 docker.yml 自动出镜像
`ghcr.io/qq33357486/magpie:codex-images`（带容器级冒烟）。冲突则 run 报红等人。

## 2026-10-28 大收缩定案（现行架构）

fork 把"客户文案中文化 + 脱敏 + 断流 504"整层**退役归还上游**（merge 90f91b85）：

- **错误文案/脱敏**：客户看到的错误由前置代理 **TransformVetter 统一脱敏**
  （错误响应/错误流里替换邮箱、网址、裸地址、`magpie` 字样；见 TV 仓库
  `src/error_mask.rs`，开关 `ERROR_MASK_ENABLED`）。magpie 本体说上游的英文
  原话。**不要再在 fork 里加中文文案或脱敏网。**
- **断流 504 层**（writeCut/cutBody/cutTold）：整体删除。new-api 侧 retry=0
  早已使 502/504 之别失去意义；真需要可在 TV 层做。
- **上游测试不再当门禁**：test.yml 已删除；merge-upstream 只做 `go build
  -tags nogui`；docker.yml 的容器冒烟（能起、healthcheck、能答请求）是唯一
  上线闸门。上游自己的 flaky/红测试与我们无关，skip 名单文化退役。
- **渠道拓扑**：new-api 的 88/89/90（文本）与 68（生图）都走
  `transformvetter:8000/!<PROFILE>$magpie:3425`，错误出口全被 TV 盖住。

**fork 现存改动（合并时唯一要操心的）**：

| 改动 | 落点 | 性质 |
|---|---|---|
| 生图档位/独立冷却/白名单/计价豁免/流式 | draw.go（块状）+ draw_lane.go + draw_stream.go + fork_draw_whitelist.go（fork 专属）+ usage/window_holds.go（11 行守卫） | 功能 |
| gw1: 压缩标记（防 magpie1: 落进客户 rollout） | codexsummary/fork_marks.go（专属）+ codex_backend.go 四个点（ForkMessage 读/ForkMark 双查/ForkEncrypted 写）+ native_compaction.go（ForkWritten×2）+ sessions/codex_portable.go（portLine 三标记）+ sessions/carry_convert.go（ForkMessage） | 功能 |
| 订阅桥 | claude_subscription.go（rewriteImageNotes + textCallSee 接线）+ claude_image_notes.go（专属） | 功能 |
| textcalls（模型把工具调用打成文本） | textcalls.go（"[tool call " 标记）+ textcalls_magpie.go（专属） | 功能，上游 issue #2485/2486 未修 |
| 响应 model 归一（Anthropic 出口回写为客户请求名，vendor 马甲不露真身；透传路径不经过此处——别名 vendor 须走翻译路径，即 BaseAPI≠客户端协议） | anthropic.go 两处：anthropicEncoder.start（model 优先 e.model，2026-10-31）+ renderAnthropic（删 res.Model 覆盖） | 功能 |
| 少量修上游红 | agentenv_test.go（CARGO_HOME）、tui/models_test.go（add927e6 picker） | 测试 |

共享文件里的钩子合计约 10 处、多为单行——这就是合并冲突的全部期望来源。
冲突解法照旧：**结构取上游、功能钩子按上表逐一植回、测试断言跟实现走**。

## 挂载原则（仍然有效）

- fork 专属文件（上游不存在）零冲突，新逻辑一律放这类文件。
- 共享文件只留单行钩子调用，函数体放专属文件。
- 不重排、不重格式化上游代码；每行无谓 diff 都是未来的冲突面。
- 上游重写客户可见报错原文 → **不用跟**（TV 层兜底脱敏，英文原话可出）。

## 合并 SOP（简化版）

1. `git fetch upstream && git merge upstream/main --no-ff --no-commit`
2. `git diff --name-only --diff-filter=U` 逐个看 `<<<<<<<` 块，按上表分类：
   涉及保留功能的 → 结构取上游、钩子植回；其余 → 整块取上游。
3. `go build ./...`（能编即可；定向跑保留功能的测试：
   `go test ./internal/gateway/ -run "Draw|NativeCompaction|Compact|Textcall|Protection"`）
4. **写读口径复核**：全局 grep `codexsummary.Message(` / `Encrypted(` /
   `Written(`，上游新读点漏 gw1: 的一行换 Fork 系 twin（10-22、10-27 两例教训）。
5. workflows 恢复：`git rm -r .github/workflows` 后
   `git checkout ORIG_HEAD -- .github/workflows`（先 rm 再 checkout，防上游
   新增 workflow 文件留在提交里被拒推）。
6. 提交、推送。docker.yml 出镜像、容器冒烟是闸门。上线按服务器手册
   （skopeo 7890 代理拉取，须人工确认重启）。

## 上游自身的红（历史经验，现已与我们无关）

上游 3 个 Claude Code loop 提交不经全量门禁、CI runs 互相取消从不跑完，
会推红测试过来。过去我们靠 test.yml skip 名单当天修；**test.yml 退役后
此类红不再拦截任何东西**，仅当它撞上保留功能的测试时才需要处理。遇到
"合并树上单跑红"，先在 upstream/main worktree 复现定性，勿当自己的回归。

## Windows 本地验证（历史红名单，仍然适用）

本地只跑定向 `-run` 测试；全量套件在 Windows 有大量环境性失败/卡死
（路径分隔符、时钟粒度、cgroup、keychain 等，历史清单见 git 历史本文件
10-27 版），Linux 也不跑了——编译 + 容器冒烟是唯一闸门。

## 历史章节

三层挂载/Told 双胞胎/compaction twin/CI 三坑的完整历史与解法存于 git
历史（本文件 10-27 及更早版本），现行架构下大部分机制已退役，仅在下
次想恢复中文文案或脱敏网时才需要翻。
