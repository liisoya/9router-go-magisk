# ADR-0003: 引擎 parity 缺陷允许定点修复（附上游 PR 义务）

日期：2026-09-25 ｜ 状态：已接受（修订 ADR-0002）

## 背景

ADR-0002 规定"上游零魔改"。但实践中发现上游 Go 移植存在**功能缺失级 parity
bug**——例如 codebuddy-cn 执行器缺少 Node 版的 agent 系统提示词清洗逻辑，
导致真实客户端（CodeBuddy CLI）请求被腾讯渠道校验拦截（HTTP 400 code 11128
"Illegal API invocation from an unapproved channel"），核心供应商完全不可用。
此类问题模块层无法绕过（请求体转换发生在引擎内部）。

## 决策

对**阻断核心功能**的上游 parity 缺陷，允许直接修改引擎源码，但必须：

1. **单点、最小化**：修复收敛在最小函数范围，不做顺手重构
2. **补丁存档**：`git diff` 存入 `tools/patches/<name>.patch`，上游同步后
   可一键重放（`git apply`）
3. **上游 PR 义务**：向 `luqman-v1/9router-go` 提交 PR；上游合并后删除
   本地补丁回归零魔改
4. **ADR 登记**：每个引擎修复在本文件追加记录

## 已应用的引擎修复

| 日期 | 文件 | 问题 | 补丁存档 | 上游 PR |
|---|---|---|---|---|
| 2026-09-25 | `internal/proxy/executor/codebuddy.go` | 缺少 agent 系统提示词清洗 → codebuddy-cn 全部请求被腾讯 11128 拦截 | `tools/patches/codebuddy-cn-agent-prompt-sanitizer.patch` | 待提交 |
| 2026-09-25 | `internal/handlers/dashboard/settings.go` | 内嵌 Svelte 前端以 multipart/form-data 上传备份（`file` 字段、无密码、凭 admin 会话），后端却按 Node 时代的 JSON+password 解析 → Dashboard 导入数据库必然 400 "Invalid database payload"（新设备首装导入被阻断）。修复：multipart 分支解析 `file` 字段；admin 会话/CLI token/密码三选一授权（该路径中间件本就强制 admin 会话）。Node JSON 流保持不变 | **已撤回**（Phase 20 把前端改回上游 JSON+password 形状后该分支已无调用方；Phase 23 随 marge 一并撤掉） | 无需（与上游一致） |
| 2026-09-26 | `internal/handlers/router.go` | `/api/models/test`（仪表盘模型测试）被挂在 RequireApiKey 组内——Node 原版它是 dashboard 内部端点（admin 会话语义，`pingModelByKind`）。备份导入清空 apiKeys 表后，仪表盘所有模型测试在引擎门口 401 "Invalid API key."（请求未达上游）。修复：移入 RequireDashboardAuth 组（admin 会话 / CLI token / API key 三选一），仪表盘从此不依赖 apiKeys 表 | **已由上游吸收**（v1.9.2 `1865f78`，本地补丁已撤） | 已完成 |
| 2026-09-26 | `internal/handlers/router.go` | `media.HandleWebFetch`（网页抓取）**已实现但从未挂载** → 内嵌 Dashboard 的媒体面板按 `/v1/web/fetch` 调用时必 404（UI 调用 parity 巡检抓到；真机 `GET /v1/web/fetch` 由 404 → 405 验证已挂载）。修复：显式双注册 `/web/fetch` + `/v1/web/fetch`（与 `/v1/models` 同形，1 行 + 1 行） | `tools/patches/media-web-fetch-route.patch` | 待提交 |
