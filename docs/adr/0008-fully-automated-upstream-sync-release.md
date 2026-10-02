# ADR-0008: 上游同步发布采用全自动流水线，冲突/失败即熔断

日期：2026-10-02 ｜ 状态：已接受

## 背景

此前每次上游（`luqman-v1/9router-go`）发版都要手动完成：合并基线 → `bump-version.sh` →
`build.sh` → 打 tag → 发布模块 zip → 更新 `update.json`/`version.json`。上游活跃，手工跟进
成为日常负担。同时模块有 `updateJson` 在线更新，设备端会自动升级——**坏包会直接推到所有用户**，
这是本决策最大的风险面。

## 决策

1. **全自动发布**：GitHub Actions 定时（每日 UTC 22:00，`workflow_dispatch` 手动兜底，
   `concurrency` 防重叠）检测上游最新 Release tag 与本地 `VERSION` 比对。有新基线则：
   `git merge` 上游 tag → `bump-version.sh` → 完整 `build.sh`（七步含全部门禁）→
   全绿则自动 commit main + push tag `vX.Y.Z-r1` → `release.yml` 发布模块 zip，并由 bot
   commit `update.json`/`version.json`。24 小时内上游发多个版本时只构建检测时的最新，
   中间版本跳过。
2. **硬熔断**：merge 冲突或任一门禁失败 → 放弃本次发布，main 保持上一基线原状，
   自动开 issue（label `sync-failed`，同步成功后自动关闭旧的）转人工。不做机械补丁重放
   （`git apply` 三个 patch）——"能打上但语义错"的包在全自动、无人复核的模式下最危险。
3. **fork 上禁用 `release.yml` 的 docker job**：缺 `DOCKERHUB_*` secret 与 `prod` environment
   必然失败，且模块不需要 Docker 镜像。

## Considered Options

- **半自动**（机器人开 PR，人工确认后合并发布）：拒绝——引擎更新多为修 bug、基线相对稳定，
  每天一次的人工确认无增量价值；熔断开 issue 已兜住冲突/失败风险。
- **自动重放 `tools/patches/*.patch`**：拒绝——见决策 2。真痛了再议。

## Consequences

- 发布不再需要人工扳机；信任从"人工复核"转移到"门禁"（`build.sh` 七步断言 + CI 测试，
  见 ADR-0007/`docs/TESTING.md` 的门禁语义）。
- ADR-0003 的定点补丁与上游改动重叠时，同步会频繁熔断——转人工裁决是特性，不是缺陷。
