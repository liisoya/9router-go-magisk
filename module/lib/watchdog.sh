#!/system/bin/sh
# watchdog.sh — 生命周期守护：只判"进程在不在"，不在就拉起。
#
# 判据只有一条（故意如此）：**不做错误率/健康度判据** —— 上游正常返回 502 时错误率会飙升，
# 那种判据会把健康的引擎反复重启。这里只回答"pid 还在不在"。
#
# 为什么需要它（2026-09-26 真机事故，两条独立死因都会走到同一个结局）：
#   1) 系统清理连坐：WebUI 经 ksu.exec 启动的服务落在**管理器应用的 cgroup** 里
#      （实测 `0::/uid_10235/pid_2661`；setsid 只换会话不换 cgroup）。MIUI 锁屏清理/
#      上滑清理该应用时整组被 SIGKILL —— 引擎与 dnsfwd 同时静默死亡。
#      lifecycle.sh 的 life_cgroup_escape 让新进程脱组；本守护兜住已经在组里的老进程。
#   2) 其它死亡：OOM、崩溃、端口被抢、二进制替换后交接失败……任何原因，只要进程没了
#      就拉起来 —— 这正是用户侧"必须手动重启才恢复"的根治点。
#
# 自身为什么安全：只由 service.sh 在开机路径启动（init/ksud 上下文，cgroup 为 `/`），
# armed 是闸（life_wd_start 会检查）；非开机路径起的守护会随启动者被清掉，等于没守护。
#
# 本文件**不读写任何状态文件**：状态与意图全部问 lib/lifecycle.sh（唯一所有者，ADR-0005）。
# 它只做三件事：等事件、去抖、记日志。
#
# ── 事件驱动（FIXPLAN Phase 33.12，2026-09-29 真机验证）──
# 原来是"每 5s 醒一次问一句『还在吗』" = 17280 次唤醒/天，其中绝大多数答案是"在"。
# 现在：
#   * **子进程退出 → CHLD 信号立刻打断 sleep**（真机实测 3s 内醒）。引擎是我们亲手起的
#     时候，还能 `wait` 到**退出原因**（退出码 / 被哪个信号杀）并写进日志 —— 这是回答
#     "为什么又死了"的唯一客观证据；
#   * **运维请求 → USR1 叫醒**（life_wd_request 写完文件顺手通知），不再等一个周期；
#   * `sleep` 只做**兜底**：引擎不是我们子进程时、僵尸、日志轮转、内存采样，缺省 60s。
#
# ⚠️ **信号 handler 内禁止 fork**（尤其禁止 log()，它带 date）：
#   handler 自己 fork 会再产生一个 SIGCHLD → 再进 handler → **自我触发的风暴**
#   （真机实测：同一秒刷了几百行，且那个循环会一直转下去）。
#   所以两个 handler 都只做算术赋值，真正的活儿全部留给主循环。
#
MODDIR="$(cd "$(dirname "$0")/.." && pwd)"
DATA_DIR="${DATA_DIR:-/data/adb/9router-go}"

# ── 引导证据（写在**任何 source 之前**）──
# 2026-09-29 真机事故：开机那次守护没起来，日志里连"守护启动"都没有 —— 于是无法区分
# "它根本没被执行"与"它在 source 库里就死了"。这行先落盘，两种情形立刻可分：
#   没有本行        → 脚本压根没被执行（setsid/exec/权限/更早的失败）
#   有本行、无"守护启动" → 死在 source lifecycle.sh / log.sh 或 log_rotate_all
# 直接 printf 而不能用 log_write：本行必须早于 lib/log.sh 被 source（日志路径此刻仍在硬编码）。
printf '[%s] watchdog: 引导中 pid=%s\n' "$(date '+%F %T')" "$$" >> "$DATA_DIR/watchdog.log" 2>/dev/null

. "$MODDIR/lib/lifecycle.sh" || exit 1

LOG="$LOG_WATCHDOG_PATH"

# 内存证据的阈值与节奏由 lib/lifecycle.sh 拥有（LIFE_RSS_WARN_KB / life_rss_log_due）——
# 本文件只是调用方，不再自带一份会漂的常量。

# ── 日志 ──
# 路径与上限的唯一声明在 lib/log.sh（C4）；本守护是唯一的长驻进程，因此由它按那份声明
# 定期轮转（log_rotate_all）。这里只留一行写日志的包装。
log() { log_write watchdog "[$(date '+%F %T')] $*"; }
wait_engine() {
  # 引擎起来后要 bind + 载入目录才可用；固定 3s 在高负载时会误报"拉起失败"
  # （真机见过：日志说失败，但 2s 后 /health 200）。轮询到 10s，如实判定。
  # 轮询语义的唯一实现在 lib/wait.sh —— ADR-0004 那次误报正是"四处各写一份"的代价
  wait_for 10 1 life_engine_healthy
}
# ── 轮询间隔（兜底周期）──
# 用户可用 $DATA_DIR/watchdog-interval 覆盖（2..300 秒，缺省 60）。
# 60 之所以够用：真正的"死了马上救"靠 CHLD 事件（<1s），这个周期只负责兜底维护。
# 何时退回短周期：引擎不是本守护的子进程时（刚开机、或守护自己重启过），CHLD 收不到，
# 只能靠轮询 —— 那时用 iv_fallback，等我们第一次亲手拉起引擎后自动回到长周期。
# 每轮读一次配置（1 次 fork/分钟）；之前那套"缓存 12 轮"是为 5s 热路径省的，已不需要。
iv=60
iv_fallback=10
interval_refresh() {
  _i="$(cat "$DATA_DIR/watchdog-interval" 2>/dev/null | tr -d ' \n')"
  case "$_i" in
    ''|*[!0-9]*) iv=60 ;;
    *) if [ "$_i" -ge 2 ] && [ "$_i" -le 300 ]; then iv="$_i"; else iv=60; fi ;;
  esac
}

life_wd_announce
log_rotate_all
interval_refresh
log "守护启动 pid=$$ cgroup=$(life_cgroup_of $$) interval=${iv}s（事件驱动：CHLD 死亡事件 + USR1 请求）"

miss_eng=0
miss_dns=0
elapsed=0          # 已运行秒数：内存策略的记账单位（用秒，免得随周期变化而静默漂移）
rss_at=0           # 上次记内存时的 elapsed
started_at=""      # B4：墙钟起点（首次采样时填 `date +%s`）
eng_ours=""        # 本守护亲手拉起的引擎 pid —— 只有它才能 wait 到退出原因
dns_ours=""
confirmed_eng=0    # CHLD 已确认该子进程真的退出 → 跳过"连续两次判死"去抖
confirmed_dns=0
ev_chld=0
ev_usr1=0
# handler 内**只做算术赋值**（零 fork）：见文件头的风暴警告。
trap 'ev_chld=1' CHLD
trap 'ev_usr1=1' USR1
while :; do
  # ── 关闭意图（卸载 / 用户显式关守护）──
  if life_wd_disabled; then
    life_wd_retire
    log "收到关闭意图，守护退出"
    exit 0
  fi

  # ── 事件确认的退出（CHLD）：**先取退出原因，再交给下面的监督分支动手** ──
  # 为什么要先确认"是哪一个"：CHLD 只说"某个子进程走了"，而 `wait` 只能取一次
  # （取完状态就没了）—— 所以必须在拉起之前，趁子进程还在僵尸态把原因取走。
  # 为什么要比对 pidfile：`eng_ours` 可能已经过期（被 stop_all 换掉、或被别人替换），
  # 那时 wait 取到的是别的子进程的状态，会把退出原因记错、甚至记成"被信号 7 杀"。
  if [ "$ev_chld" = "1" ]; then
    if [ -n "$eng_ours" ] && [ "$(life_engine_pid)" = "$eng_ours" ] \
       && ! life_engine_healthy; then
      wait "$eng_ours" 2>/dev/null
      _ec=$?
      log "引擎退出（$(life_exit_reason "$_ec") pid=$eng_ours）"
      eng_ours=""
      confirmed_eng=1
    fi
    if [ -n "$dns_ours" ] && [ "$(life_dns_pid)" = "$dns_ours" ] \
       && ! life_dns_healthy; then
      wait "$dns_ours" 2>/dev/null
      _ec=$?
      log "dnsfwd 退出（$(life_exit_reason "$_ec") pid=$dns_ours）"
      dns_ours=""
      confirmed_dns=1
    fi
  fi

  # ── 快路径的前提校正（2026-09-29 架构走查 A2）──
  # `eng_ours`/`dns_ours` 只表示"我们亲手起过它"。一旦 pidfile 里已经不是它
  # （用户从 WebUI 起过新的、stop_all 换过、或守护自己重启过），这个值就**陈旧**：
  # 新进程不是我们的子进程 → 收不到 CHLD；而变量非空又让下面停在 60s 长周期 →
  # 自愈从秒级退化成最多 60s。所以判据从"变量非空"改成"pidfile 里就是它"，当轮就归零。
  # （注意顺序：上面的确认-退出分支要用 pidfile == eng_ours 才 wait 得到退出原因，
  #   所以这一校正必须放在它**之后**。）
  if [ -n "$eng_ours" ] && [ "$(life_engine_pid)" != "$eng_ours" ]; then eng_ours=""; fi
  if [ -n "$dns_ours" ] && [ "$(life_dns_pid)" != "$dns_ours" ]; then dns_ours=""; fi

  # ── 运维请求（优先于维护窗口：重启必须能立刻生效；USR1 会在写完请求时叫醒我们）──
  _req="$(life_wd_take_request)"
  case "$_req" in
    restart)
      log "收到 restart 请求：停 → 重拉（免疫上下文）"
      life_stop_all >/dev/null
      eng_ours=""; dns_ours=""       # 旧的都停了：别拿它们的 pid 去 wait（取不到状态）
      life_ensure_engine >/dev/null 2>&1
      if wait_engine; then
        eng_ours="$(life_engine_pid)"
        log "重启完成 pid=$eng_ours cgroup=$(life_cgroup_of "$eng_ours")"
      else
        log "重启后引擎仍不在（详见 9router.log）"
      fi
      miss_eng=0
      confirmed_eng=0
      # stop_all 把 DNS 也停了：请求既然停了它，就必须由同一处把它拉回来
      # （否则只能靠下面的监督分支补救，白等一个周期）
      life_ensure_dns >/dev/null 2>&1
      dns_ours="$(life_dns_pid)"
      miss_dns=0
      confirmed_dns=0
      ;;
    start)
      life_ensure_engine >/dev/null 2>&1
      life_ensure_dns >/dev/null 2>&1
      eng_ours="$(life_engine_pid)"
      dns_ours="$(life_dns_pid)"
      miss_eng=0
      miss_dns=0
      confirmed_eng=0
      confirmed_dns=0
      ;;
    '') ;;
    *) log "忽略未知请求：$_req" ;;
  esac

  if life_wd_should_supervise; then
    # ── 引擎：**事件确认的退出立刻动手**；没有事件时才退回"连续两次判死"去抖 ──
    # 去抖防的是"正在优雅退出/更新交接"的瞬间抢跑；而 CHLD 是既成事实，不必再等一轮。
    if life_engine_healthy; then
      miss_eng=0
      confirmed_eng=0
    else
      miss_eng=$((miss_eng + 1))
      if [ "$confirmed_eng" = "1" ] || [ "$miss_eng" -ge 2 ]; then
        _why="连续 $miss_eng 次判死"
        [ "$confirmed_eng" = "1" ] && _why="事件确认已退出"
        log "引擎不在（$_why），拉起"
        life_ensure_engine >/dev/null 2>&1
        if wait_engine; then
          eng_ours="$(life_engine_pid)"
          log "引擎已拉起 pid=$eng_ours cgroup=$(life_cgroup_of "$eng_ours")"
        else
          log "拉起失败（详见 9router.log）"
        fi
        miss_eng=0
        confirmed_eng=0
      fi
    fi

    # ── dnsfwd：谓词里已含"用户关闭 / 已让路（:53 被第三方占用 = 正常稳态）"──
    if life_dns_healthy; then
      miss_dns=0
      confirmed_dns=0
    else
      miss_dns=$((miss_dns + 1))
      if [ "$confirmed_dns" = "1" ] || [ "$miss_dns" -ge 2 ]; then
        log "dnsfwd 不在，拉起"
        life_ensure_dns >/dev/null 2>&1
        dns_ours="$(life_dns_pid)"
        miss_dns=0
        confirmed_dns=0
      fi
    fi
  else
    # 不在管辖范围（用户停服 / 维护窗口 / 未武装）：计数清零，回来时不会立刻动手
    miss_eng=0
    miss_dns=0
    confirmed_eng=0
    confirmed_dns=0
  fi

  # ── 维护（缺省每轮 ≈60s）：日志轮转 + 内存证据 ──
  # 为什么记在这里：守护是**唯一的长驻进程** —— 出事一小时后，只有它还能告诉你当时的 RSS。
  # 该不该记由 lifecycle 的策略决定（超阈值 + 12 分钟限流；另有一条每小时基线）；
  # 记账单位是**秒**而不是轮次 —— 否则改周期会把"每小时"静默改成"每 12 小时"。
  log_rotate_all
  _r="$(life_rss_kb "$(life_engine_pid)")" || _r=""
  if [ "$(life_rss_log_due "$_r" "$elapsed" "$rss_at")" = "1" ]; then
    log "引擎内存 ${_r}kB（阈值 ${LIFE_RSS_WARN_KB}kB，已运行 ${elapsed}s）"
    rss_at=$elapsed
  fi

  # ── 睡到下一轮：被 CHLD / USR1 打断就立刻回到循环顶 —— 这就是"事件驱动" ──
  # 引擎不是本守护的子进程时退回短周期：那种情况收不到 CHLD，只能靠轮询兜住
  # （而且第一次亲手拉起它之后就会自动回到长周期）。
  interval_refresh
  poll_iv="$iv"
  [ -z "$eng_ours" ] && poll_iv="$iv_fallback"
  # 先清事件位再睡：否则本轮自己 fork 出来的 CHLD 会让下一轮立即空转
  # （真机验证过"fork 之后的下一次 wait 不会被迟到的 CHLD 打断"，清位是免费的保险）。
  ev_chld=0
  ev_usr1=0
  sleep "$poll_iv" & _sp=$!
  wait "$_sp" 2>/dev/null
  # 被打断时那个 sleep 还活着：收掉它（否则会累积，而且它的退出还会白唤醒一次），
  # 并 wait 收尸避免留下僵尸（本轮刚把"僵尸不算活着"这条判定补上）。
  kill "$_sp" 2>/dev/null
  wait "$_sp" 2>/dev/null
  # B4（2026-09-29 走查）：用**墙钟**推进记账，而不是"名义间隔之和" —— sleep 会被 CHLD/USR1
  # 打断（可能只睡了几十毫秒），无条件累加 poll_iv 会让 elapsed 高估 → "每 1 小时"的内存基线
  # 比真实一小时更早触发。代价是每轮多一次 `date` fork（缺省 60s 一次），换账本与注释口径一致。
  _now="$(date +%s)"
  [ -n "$started_at" ] || started_at="$_now"
  elapsed=$((_now - started_at))
done
