# ADR-0002: 上游零魔改，模块层功能"只增不改"

日期：2026-09-25 ｜ 状态：已接受

## 背景

本仓库是上游 fork，需要长期跟进上游版本。若直接修改上游源码（如把 DNS 管理
API 塞进引擎的 dashboard handler），每次同步上游都会产生冲突，随时间累积成
无法合并的私有分支。

## 决策

**不改上游任何一行代码。** 模块层全部功能以新增文件实现：

- `module/`（脚本 + WebUI + 预编译二进制）—— 引擎完全无感知
- `tools/`（dnsfwd 源码与构建脚本）
- `build.sh`、`CONTEXT.md`、`docs/`、`MAGISK.md` —— 工程与文档

WebUI 通过 root shell（KernelSU `ksu.exec`）操作 `$DATA_DIR` 下的配置文件与
进程，不经过引擎 API，因此与上游引擎解耦。

## 后果

- 正面：`git pull` 上游永远干净 fast-forward；模块功能独立演进。
- 负面：DNS 管理只能在模块 WebUI 做（不能进官方 Dashboard 页面内）；
  WebUI 依赖 root shell 桥，非 root 环境不可用（可接受：模块本身就是 root 场景）。
