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
# 它只做三件事：轮询、去抖（连续两次判死）、记日志。
#
MODDIR="$(cd "$(dirname "$0")/.." && pwd)"
DATA_DIR="${DATA_DIR:-/data/adb/9router-go}"
. "$MODDIR/lib/lifecycle.sh" || exit 1

LOG="$LOG_WATCHDOG_PATH"

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
interval() {
  _i="$(cat "$DATA_DIR/watchdog-interval" 2>/dev/null | tr -d ' \n')"
  case "$_i" in ''|*[!0-9]*) echo 5 ;; *) [ "$_i" -ge 2 ] && [ "$_i" -le 300 ] && echo "$_i" || echo 5 ;; esac
}

life_wd_announce
log_rotate_all
log "守护启动 pid=$$ cgroup=$(life_cgroup_of $$) interval=$(interval)s"

miss_eng=0
miss_dns=0
ticks=0
while :; do
  # ── 关闭意图（卸载 / 用户显式关守护）──
  if life_wd_disabled; then
    life_wd_retire
    log "收到关闭意图，守护退出"
    exit 0
  fi

  # ── 运维请求（优先于维护窗口：重启必须能立刻生效）──
  _req="$(life_wd_take_request)"
  case "$_req" in
    restart)
      log "收到 restart 请求：停 → 重拉（免疫上下文）"
      life_stop_all >/dev/null
      life_ensure_engine >/dev/null 2>&1
      if wait_engine; then
        log "重启完成 pid=$(life_pid_of "$LIFE_ST_ENGINE") cgroup=$(life_cgroup_of "$(life_pid_of "$LIFE_ST_ENGINE")")"
      else
        log "重启后引擎仍不在（详见 9router.log）"
      fi
      miss_eng=0
      # stop_all 把 DNS 也停了：请求既然停了它，就必须由同一处把它拉回来
      # （否则只能靠下面的监督分支 ~10s 后补救，白等两个周期）
      life_ensure_dns >/dev/null 2>&1
      miss_dns=0
      ;;
    start)
      life_ensure_engine >/dev/null 2>&1
      life_ensure_dns >/dev/null 2>&1
      miss_eng=0
      miss_dns=0
      ;;
    '') ;;
    *) log "忽略未知请求：$_req" ;;
  esac

  if life_wd_should_supervise; then
    # ── 引擎：连续两次判死才动手（避免与"正在优雅退出/更新交接"的瞬间抢跑）──
    if life_engine_healthy; then
      miss_eng=0
    else
      miss_eng=$((miss_eng + 1))
      if [ "$miss_eng" -ge 2 ]; then
        log "引擎不在（连续 $miss_eng 次判死），拉起"
        life_ensure_engine >/dev/null 2>&1
        if wait_engine; then
          log "引擎已拉起 pid=$(life_pid_of "$LIFE_ST_ENGINE") cgroup=$(life_cgroup_of "$(life_pid_of "$LIFE_ST_ENGINE")")"
        else
          log "拉起失败（详见 9router.log）"
        fi
        miss_eng=0
      fi
    fi

    # ── dnsfwd：谓词里已含"用户关闭 / 已让路（:53 被第三方占用 = 正常稳态）"──
    if life_dns_healthy; then
      miss_dns=0
    else
      miss_dns=$((miss_dns + 1))
      if [ "$miss_dns" -ge 2 ]; then
        log "dnsfwd 不在（连续 $miss_dns 次判死），拉起"
        life_ensure_dns >/dev/null 2>&1
        miss_dns=0
      fi
    fi
  else
    # 不在管辖范围（用户停服 / 维护窗口 / 未武装）：计数清零，回来时不会立刻动手
    miss_eng=0
    miss_dns=0
  fi

  ticks=$((ticks + 1))
  [ $((ticks % 12)) -eq 0 ] && log_rotate_all   # 约每 60s 轮转一次

  sleep "$(interval)"
done
