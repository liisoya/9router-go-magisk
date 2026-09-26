# ADR-0004: 生命周期归模块（启动即脱 cgroup + 开机上下文守护）

日期：2026-09-26 ｜ 状态：已接受（新增；不修订 ADR-0001/0002/0003）

## 背景

用户侧症状：「跑了一段时间，面板显示引擎未运行，Dashboard 打不开，手动重启才恢复」。
真机复现与取证（MI 6X / Android 14 / KernelSU v0.9.5，2026-09-26）：

1. 引擎与 `dnsfwd` **同时静默消失**：`9router.log` 无退出行、无 panic，`dnsfwd.log` 无退出行，
   `dmesg`/`logcat` 无 OOM/LMK 记录，设备未重启（dropbox 无新 `SYSTEM_BOOT`）。
2. `dumpsys activity exit-info me.weishu.kernelsu`：
   `11:35:23 reason=10 (USER REQUESTED) description=LockScreenClean` —— 同一瞬间另有 2 个
   普通 App 被同样清理（系统级锁屏清理后台）。
3. 模块 WebUI 用 `ksu.exec` 执行命令；实测其派生的 `sh` 是**管理器应用的子进程**，
   cgroup = `0::/uid_10235/pid_2661`（与 App 同组）。
4. 死因链：用户 11:17 在 WebUI 点「重启引擎」→ `service.sh` 里的 `setsid` 只换会话
   **不换 cgroup** → 引擎与 dnsfwd 留在应用 cgroup → 11:35 系统清应用时整组 SIGKILL。
   `setsid` 无用已在真机证明（把 setsid 进程放进应用 cgroup，`am force-stop` 后同死）。
5. 为什么必须手动重启：`ops.sh` 只有 `restart-engine`，**没有任何守护**，一死即永久停机。

同时确认：**引擎没有"内存超限自杀"逻辑**（全仓非测试代码只有启动期 `log.Fatal`/`os.Exit`
与自更新的 `RestartSelf` 交接），113 MB 常驻是 Go 引擎 + 内嵌前端的正常值；面板里的
200/300 MB 只是 UI 上色阈值，不触发任何动作。

## 决策

1. **启动即脱组**：所有启动路径（service.sh / 守护 / WebUI 重启 / 更新后重启）统一走
   `ops.sh start-engine`；进程起来后立刻 `cgroup_escape`（root 写 cgroup 根
   `cgroup.procs`，实测 `move=ok`）。失败不阻塞启动（有守护兜底），但写入 `9router.log`。
   `start-dns` 同样处理。
2. **守护（lib/watchdog.sh）**：
   - 判据只有"pid 还在不在"——**不做错误率/健康度判据**（上游 502 会让健康引擎被反复重启）。
   - 连续两次判死才动手（避免与"优雅退出/更新交接"的瞬间抢跑）。
   - **只由 service.sh 在开机路径武装启动**（init/ksud 上下文，cgroup 为 `/`）；
     `watchdog-armed` 是闸，其他上下文起的守护不武装（它自己也会被连坐，等于没守护）。
   - 让路：`watchdog-hold`（绝对到期时间，维护窗口）、`watchdog.req`（restart/start，
     **优先于 hold**）、`watchdog-off`（自尽）。
   - `ops.sh restart-engine` 有守护时**委托守护**执行停止+启动：这样"重启"发生在免疫上下文里，
     而不是把新进程又放回应用 cgroup。
3. **可观测**：`ops.sh status/panel` 增加 `watchdog=up|down|stale`；WebUI 概览页显示
   「生命周期守护」。不可观测的保命机制等于没有。
4. **门禁**：`tools/device/test-lifecycle.sh`（真机）断言 T1 守护在场 / T2 强杀后 40s 内自愈
   （新 PID + `/health` 200）/ T3 从应用 cgroup 启动仍能脱组。修复前 T2、T3 必红。

## 备选与否决

- **只加 `setsid`/`nohup`**：无用，已真机证明（cgroup 连带杀伤不看会话）。
- **只做守护、不脱组**：可用，但用户点一次 WebUI 重启就会把进程放回应用 cgroup，守护要等
  下一次判死（≥10s）才救回来，且引擎重启期间请求全断。脱组从根上消掉这一类。
- **只脱组、不做守护**：解决已知死因，但 OOM/崩溃/端口被抢仍然"必须手动重启"。
- **让 init 托管（init.rc）**：模块的 `.rc` 在 post-fs-data 之后才挂载，init 早已解析完
  `system/etc/init`，不可靠；KernelSU 无"在 init 上下文跑任意命令"的通用接口。
- **用 `am`/`init` 拉起**：无对应接口，且会引入与 Android 组件生命周期的耦合。

## 后果

- 用户不再需要手动重启；`watchdog.log` 会记录"进程不在 → 已拉起"的完整时间线。
- 代价：多一个常驻 shell 进程（每 5s 一次 `kill -0` + 两个文件判断，CPU 近似 0），
  以及 `watchdog-interval`（秒）这个可选调参文件。
- 守护自身若被杀：`ops.sh status` 显示 `watchdog=stale`，且**任何一次 `restart-engine`
  都会顺手把它重新武装启动**，重启设备恢复。
