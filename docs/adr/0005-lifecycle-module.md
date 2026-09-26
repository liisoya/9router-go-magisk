# ADR-0005: 生命周期收成一个 module（lib/lifecycle.sh），ops.sh 退为配置与编排

日期：2026-09-26 ｜ 状态：已接受（实施 ADR-0004 的后续；不修订 0001/0002/0003）

## 背景

ADR-0004 引入了守护，但把它的状态（`watchdog-armed` / `-hold` / `-req` / `-off` + 三个
pidfile）留给 4 个调用方各自读写：`service.sh`（武装/清 off）、`ops.sh`（hold/req/off）、
`watchdog.sh`（消费全部）、`uninstall.sh`（写 off/撤武装）。优先级（req &gt; hold &gt;
自动拉起 &gt; 用户意图）只由 `if` 的书写顺序表达。

后果已经在真机上出现两次：
1. `cmd_restart_engine` 里的 `rm -f "$WD_OFF"` 会**撤销用户的显式关闭意图**（用户关不掉守护）；
2. `cmd_watchdog_start` 与 pidfile 落盘存在竞态，`restart-engine` 于是退回"在调用者
   cgroup 里本地启动" —— 正好违背 ADR-0004 的前提（重启必须发生在免疫上下文）。

这两条都不是"写错一行"，而是**状态无主**的必然结果：没有 module 能回答"用户到底要不要它跑"。

## 决策

1. **新增 `module/lib/lifecycle.sh`**：可 `source`、source 时零副作用，是本项目**唯一**
   读写生命周期状态文件的地方。状态文件名（`watchdog-armed/-hold/-req/-off`、`service-off`、
   三个 pidfile）成为它的 implementation detail，其它文件不得出现这些名字。
   唯一豁免：`uninstall.sh` 的兜底分支（卸载是最后一道保险，library 不可读时也要能停进程，
   故刻意保留一小段独立实现，并在注释里标注"这是唯一被允许的重复"）。
2. **接口按"意图"设计**（depth 在接口）：
   - 意图：`life_boot`（开机：清用户关闭意图 + 武装 + 启动守护）、`life_stop_user` /
     `life_start_user`（用户显式启停，守护必须尊重）、`life_restart_engine`、`life_stop_all`、
     `life_shutdown`（卸载）
   - 幂等：`life_ensure_engine` / `life_ensure_dns`
   - 谓词：`life_engine_healthy` / `life_dns_healthy` / `life_wd_should_supervise` /
     `life_wd_take_request`（守护每轮只问这几个问题）
   - 只读：`life_state`（机器可读一行，ops.sh 的 status 组合它）
3. **`ops.sh` 退为配置与编排**：数据（schema/PORT/密码/出厂 key）、状态聚合、
   引擎与模块 zip 安装编排；对外子命令集合**保持不变**（action.sh / WebUI / 门禁零改动），
   新增 `stop-user` / `start-user` 两个意图动词。
4. **用户意图可见可操作**：WebUI 概览新增「启动服务 / 停止服务」；`life_state` 用
   `engine=stopped` 与"未运行"区分（前者是用户意图，后者是故障）。

## 被否决的替代

- **把生命周期做成 ops.sh 的子命令集**（不新增文件）：`watchdog.sh` 仍要反调
  `"$OPS" stop-engine` 之类（子进程 + 解析文本），状态知识仍有两份入口；且 ops.sh 会继续膨胀。
- **守护自己持有状态、ops.sh 只写请求**：则"用户意图"仍散落两处，`service-off` 这类新意图
  又要改两个 module —— 正是本次要消除的模式。
- **`uninstall.sh` 完全依赖 lifecycle.sh**：卸载必须比其它路径更耐坏（模块可能已半损坏）。

## 后果

- 加一个意图状态（如"暂停"、"仅 DNS"）只需改 lifecycle.sh 一处。
- 守护的循环从 3 个内联条件判断缩成 `life_wd_should_supervise` + 两个健康谓词；
  `dnsfwd` 的"让路"语义（:53 被第三方占用 = 正常稳态）也只在一个谓词里。
- 代价：多一个文件（约 300 行 shell），且"source 一份库"意味着改库要重启守护才生效
  （与 watchdog.sh 自身的替换同性质，已在 ADR-0004 记录）。
- 门禁扩到 8 条断言（T1~T5），其中 T4（用户停服不被复活）与 T5（维护窗口生效且到期自愈）
  是"意图有主"的直接回归。
