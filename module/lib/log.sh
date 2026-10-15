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
  #
  # 必须 copytruncate，不能 mv 换目录项（2026-10-15 架构评审 C2）：引擎与 dnsfwd 的
  # stdout 是 shell 以 >>（O_APPEND）持有的 fd（lifecycle.sh 的启动重定向）。mv 之后
  # 进程继续写**已被 unlink 的旧 inode**——可见日志永冻在 400 行不再增长、磁盘无界
  # 增长（正是本 module 要防的"24/7 吃掉 /data"）、诊断导出（ops.sh export-logs 只读
  # 可见文件）漏掉轮转后的全部引擎日志。原地截断保留 inode，O_APPEND 的 fd 继续落
  # 可见文件。代价与 logrotate 的 copytruncate 同款：截断到回填之间的小窗口里并发
  # 追加可能落在文件中部——可接受，轮转本就丢弃历史。
  _f="$(log_path_of "$1")" || return 1
  _cap="$(log_cap_of "$1")"
  [ -f "$_f" ] || return 0
  _sz="$(wc -c < "$_f" 2>/dev/null | tr -d ' ')"
  case "$_sz" in ''|*[!0-9]*) return 0 ;; esac
  [ "$_sz" -lt "$_cap" ] && return 0
  # 先落 tmp：tail 失败就原样返回，绝不把日志截成空
  tail -n 400 "$_f" > "$_f.tmp" 2>/dev/null || { rm -f "$_f.tmp" 2>/dev/null; return 1; }
  # 再原地截断回填（> 截断同一个 inode，O_APPEND 的 fd 不受影响），最后清掉 tmp
  cat "$_f.tmp" > "$_f" && rm -f "$_f.tmp"
}
log_rotate_all() { log_rotate_one engine; log_rotate_one dns; log_rotate_one watchdog; }
