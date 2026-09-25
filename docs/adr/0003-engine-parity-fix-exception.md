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
