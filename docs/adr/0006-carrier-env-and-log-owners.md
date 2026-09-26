# ADR-0006: 承载性 env 与日志策略各自收成单一来源

日期：2026-09-26 ｜ 状态：已接受（C3 / C4；与 ADR-0005 同批）

## 背景

架构体检（2026-09-26）指出两处"知识在散文里、没有所有者"：

1. **引擎运行环境**：哪些 env 是**承载性**的（缺了引擎就不可用或不受管）只活在
   `ops.sh` 的 `export` 语句与注释里。历史上 `service.sh` 与 `ops.sh` 各有一份，漏一处
   即静默失效（例：没有 `SSL_CERT_DIR` 则所有 HTTPS 与更新检查 `x509: unknown authority`；
   没有 `AUTO_UPDATE=false` 则引擎会按 DB 开启自更新、绕过模块管理）。
2. **日志策略**：四份日志四个写者，`9router.log`（每请求一行）与 `dnsfwd.log`
   （每次让路/失败一行）在本轮之前**无任何轮转**；轮转一度只写在守护里、只覆盖其中一份。

## 决策

1. **承载性 env 的单一来源 = `$DATA_DIR/runtime.env`**（0600，含初始密码，构建器
   `life_write_runtime_env`，清单 `life_carrier_env_keys`）。启动引擎前 `set -a; . runtime.env`
   加载；生成失败才退回内联并**写日志**（不静默降级）。门禁 T7 逐条断言"键在文件里
   **且真的进了引擎进程**"（读 `/proc/<pid>/environ`）。
2. **日志策略的单一来源 = `lib/log.sh`**：路径、上限（engine 1MB / dns 256KB / watchdog 256KB）、
   轮转实现只此一份。写者只有两种用法：`log_write <name> <line>`（整行）与
   `LOG_ENGINE_PATH`/`LOG_DNS_PATH`（引擎/dnsfwd 自身 stdout 的流重定向）。
   轮转由唯一长驻进程（守护）每 ~60s 调 `log_rotate_all`；门禁 T6 用隔离数据目录实跑
   （440000B → 4400B）。

## 被否决的替代

- **把 env 写成文档清单由人核对**：文档不是闸门（AGENTS §5.0 的原话：文档式修复 ≠ 修复）。
- **让每个调用方各自 export**：C1 已把"启动引擎"收敛到一处，env 必须跟它同处一地。
- **日志改为所有写者走管道/函数（强制 `log_write`）**：引擎与 dnsfwd 的 stdout 是**进程流**，
  强制经 shell 包装只会引入缓冲与丢失风险；故保留"路径 + 流重定向"这一条合法用法并写进接口。

## 后果

- 新增承载性 env / 改日志上限都只改一处；门禁 T6/T7 立刻能挡住回归。
- `runtime.env` 会被 `install-module` 的 zip 覆盖流程保留在 `$DATA_DIR`（不在模块内），
  模块更新不会把它清掉。
