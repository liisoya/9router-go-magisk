# ADR-0009: 上游同步从「全自动合并」改为「detect+notify，人工合并」（修订 ADR-0008）

日期：2026-10-15 ｜ 状态：已接受（修订 ADR-0008 的决策 1/2；其余仍有效）

## 背景

ADR-0008 选了全自动：每日检测上游 Release → `git merge` → `bump-version.sh` → `build.sh`
七步门禁 → 自动 commit + push tag → `release.yml` 发布；merge 冲突或任一门禁失败即熔断、
开 `sync-failed` issue 转人工。

2026-10-15 调研（`docs/INVESTIGATION-20261015-dns-and-upstream-sync.md`）实证该流水线
**从未成功过一次**：`sync-upstream.yml` run #6–#13（10-08→10-15）全 `failure`，`main` 卡在
`fe43d77`。逐步结论：

- 第 7 步 `Merge upstream baseline` 失败——本地复现 `git merge --no-ff` 上游 tag 撞 **13 个
  冲突文件**（merge-base = v1.9.7，上游已到 v1.9.13；冲突面正是 §10.2 那份重度定制共享文件清单）。
- 第 12 步熔断 `Open sync-failed issue` **也失败**——仓库只有 11 个默认标签、**没有 `sync-failed`**，
  `gh issue create --label sync-failed` 报错 → **issue 从未开出 → 用户零通知**，只在 Actions 页看到一片红。

根因：模块对 `internal/**`、`web/**` 等共享文件重度定制，与上游 6 个小版本的分叉使自动
`git merge` 结构性不可行——分叉只增不减，熔断必每天触发，且越拖越难合。

## 决策

1. **上游同步改为 detect + notify**：定时/手动只检测上游最新 Release 与本地 `VERSION` 比对；
   有新基线则**开/更新一个 issue**（label `upstream-sync`，流程内幂建）提醒人工按
   AGENT-CONVENTIONS §7 合并。**不再**自动 merge / build / tag / push。
2. **自动运行暂停**：`sync-upstream.yml` 的 `schedule` cron 注掉，只留 `workflow_dispatch`。
   恢复每日提醒是安全的（纯通知、不再合并），取消注释即可。
3. **修熔断静默失败**：任何要用的标签先幂等创建再用（`gh label create … 2>/dev/null || true`），
   杜绝"标签不存在 → issue 开不出 → 零通知"。
4. 合并/构建/发版回到人工可控流程（§7 八步 + `build.sh` + tag + `release.yml`），首版目标 `v1.9.13-r1`。
5. **权限收紧**：`contents: read` + `issues: write`（不再写仓库内容）。

## 被否决的替代

- **保留全自动、只把冲突交人工**：拒绝——分叉持续增大，冲突只会更多更烂；"机器人合一半留一半"
  在全自动、无人复核下最危险（同 ADR-0008 决策 2 的理由）。
- **自动重放 `tools/patches/*.patch`**：拒绝——"能打上但语义错"的包在无人复核下最危险。
- **半自动（bot 开 PR，人工合并）**：部分采纳为 notify issue 这一轻量形态；不上升到 PR，是因为
  合并需在本地解 13 处冲突 + 跑 `tools/check.sh --all` + 真机验证，在 PR 里做反而更难 review。

## 后果

- 不再有每日红色噪音；上游更新以 issue 形式可见、可追、可手动关闭。
- 发布节奏交回人工：每次同步 = 一次 §7 全流程 + 真机验证。信任回到"人工 + 门禁"。
- 与 ADR-0002（additive-only，上游 pristine）方向一致：先把模块代码迁出共享文件以降耦
  （后续独立阶段），未来才可能重新评估自动合并。
- 待办（不擅动）：`origin/main`(`fe43d77`) 落后本地 `main`(`8df9e8f`) 约 108 个提交，推送需维护者
  明确确认——不可逆远端变更，本 ADR 不擅自推。
