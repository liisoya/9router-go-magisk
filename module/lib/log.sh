#!/system/bin/sh
# log.sh — 日志策略的唯一所有者（C4）
#
# 为什么有这个 module：四份日志有四个写者（ops.sh / service.sh / watchdog.sh / uninstall.sh），
# 而 `9router.log` 每个请求一行、`dnsfwd.log` 每次让路或失败一行 —— 在本轮之前**无任何轮转**
# （24/7 运行会无界增长，最终吃掉 /data）。轮转一度只写在守护里、且只覆盖其中一份。
# 现在：路径、上限、轮转实现都只在这里声明一次；写者只有两种用法：
#   1) 整行写入：log_write <engine|dns|watchdog> "<text>"
#   2) 进程流重定向（引擎/dnsfwd 自己往 stdout 写日志）：用 LOG_ENGINE_PATH / LOG_DNS_PATH
# 轮转由唯一的长驻进程（守护）每 ~60s 调一次 log_rotate_all（幂等、便宜：lstat + wc）。
[ -n "${DATA_DIR:-}" ] || { echo "log.sh: 需要 DATA_DIR" >&2; return 1 2>/dev/null || exit 1; }

# 路径（唯一声明）
LOG_ENGINE_PATH="$DATA_DIR/9router.log"
LOG_DNS_PATH="$DATA_DIR/dnsfwd.log"
LOG_WATCHDOG_PATH="$DATA_DIR/watchdog.log"
# 上限（唯一声明）：引擎日志按"每请求一行"估算，上限放宽到 1MB；其余 256KB
LOG_ENGINE_CAP=1048576
LOG_DNS_CAP=262144
LOG_WATCHDOG_CAP=262144

log_path_of() {
  case "$1" in
    engine)   echo "$LOG_ENGINE_PATH" ;;
    dns)      echo "$LOG_DNS_PATH" ;;
    watchdog) echo "$LOG_WATCHDOG_PATH" ;;
    *)        return 1 ;;
  esac
}
log_cap_of() {
  case "$1" in
    engine)   echo "$LOG_ENGINE_CAP" ;;
    dns)      echo "$LOG_DNS_CAP" ;;
    watchdog) echo "$LOG_WATCHDOG_CAP" ;;
    *)        return 1 ;;
  esac
}
log_write() {
  # 写一行（调用方不必知道文件在哪、有没有轮转）
  _p="$(log_path_of "${1:-engine}")" || return 1
  printf '%s\n' "$2" >>"$_p"
}
log_rotate_one() {
  # 超过上限就只留最后 400 行（保留现场，丢弃历史）
  _f="$(log_path_of "$1")" || return 1
  _cap="$(log_cap_of "$1")"
  [ -f "$_f" ] || return 0
  _sz="$(wc -c < "$_f" 2>/dev/null | tr -d ' ')"
  case "$_sz" in ''|*[!0-9]*) return 0 ;; esac
  [ "$_sz" -lt "$_cap" ] && return 0
  tail -n 400 "$_f" > "$_f.tmp" 2>/dev/null && mv "$_f.tmp" "$_f"
}
log_rotate_all() { log_rotate_one engine; log_rotate_one dns; log_rotate_one watchdog; }
