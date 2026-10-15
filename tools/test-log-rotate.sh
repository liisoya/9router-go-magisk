#!/bin/sh
# tools/test-log-rotate.sh — 断言日志轮转是 copytruncate，不是 mv 换目录项（架构评审 C2）。
#
# 为什么有它（2026-10-15）：引擎与 dnsfwd 的 stdout 是 shell 以 >>（O_APPEND）持有的
# fd（lifecycle.sh 的启动重定向，ADR-0006 明确允许这种"进程流重定向"用法）。轮转若用
# mv 换目录项，进程继续写**已被 unlink 的旧 inode**——可见日志永冻在 400 行不再增长、
# 磁盘无界增长（ADR-0006:14 要防的"24/7 吃掉 /data"）、诊断导出（ops.sh export-logs
# 只读可见文件）漏掉轮转后的全部引擎日志，而死因取证恰恰依赖它（ADR-0004）。
# watchdog.log 不受影响（log_write 每次开闭），所以这个坑只在 engine/dns 两份上爆。
#
# 本测试把"轮转不能换 inode、O_APPEND 的 fd 必须继续落可见文件"这个隐形契约钉住：
# 开一个真 O_APPEND fd 写超上限 → 轮转 → 经**同一个 fd** 再写 → 断言新内容在可见
# 文件里、inode 不变、无 tmp 残留。旧（mv）实现在 R1/R4 两条上必红。
#
# 用法：在仓库根执行 `sh tools/test-log-rotate.sh`
set -u
LOG=./module/lib/log.sh
if [ ! -f "$LOG" ]; then
  echo "找不到 $LOG（请在仓库根执行）" >&2
  exit 1
fi

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); printf '  ✅ %s\n' "$*"; }
no() { FAIL=$((FAIL + 1)); printf '  ❌ %s\n' "$*"; }

TMPD="$(mktemp -d 2>/dev/null || echo /tmp/logrot.$$)"
mkdir -p "$TMPD"
cleanup() { rm -rf "$TMPD"; }
trap cleanup EXIT

ino_of() {
  stat -c %i "$1" 2>/dev/null || ls -i "$1" 2>/dev/null | awk '{print $1}'
}

# 子 shell 里 source：log.sh 需要 DATA_DIR，路径全部落临时目录，不碰真数据目录
(
  DATA_DIR="$TMPD"
  . "$LOG" || exit 1
  # 测试用小 cap（字节）：不必真写 1MB。LOG_*_CAP 是普通变量，sourcing 后可覆盖。
  LOG_ENGINE_CAP=200

  echo "== R 轮转契约（copytruncate）=="
  # R0：开一个 O_APPEND fd —— 模拟 shell 给引擎/dnsfwd stdout 的重定向
  exec 9>>"$LOG_ENGINE_PATH"
  i=0
  while [ "$i" -lt 600 ]; do
    printf 'line-%03d\n' "$i" >&9
    i=$((i + 1))
  done
  _n0="$(wc -l < "$LOG_ENGINE_PATH" | tr -d ' ')"
  [ "$_n0" = 600 ] && ok "R0 经 fd 写入 600 行（超 200 字节 cap）" || no "R0 经 fd 写入 600 行（得到 $_n0）"

  _ino_before="$(ino_of "$LOG_ENGINE_PATH")"
  log_rotate_one engine
  _ino_after="$(ino_of "$LOG_ENGINE_PATH")"
  [ -n "$_ino_before" ] && [ "$_ino_before" = "$_ino_after" ] \
    && ok "R1 轮转后 inode 不变（mv 实现会换 → 此处红）" \
    || no "R1 轮转后 inode 不变（$_ino_before → $_ino_after）"

  _n1="$(wc -l < "$LOG_ENGINE_PATH" | tr -d ' ')"
  [ "$_n1" = 400 ] && ok "R2 只留最后 400 行" || no "R2 只留最后 400 行（得到 $_n1）"
  _first="$(head -n 1 "$LOG_ENGINE_PATH")"
  [ "$_first" = "line-200" ] && ok "R3 保留的是尾部（首行 = line-200）" || no "R3 保留尾部（首行 $_first）"

  # R4：核心回归——经**同一个 fd** 再写，必须落在可见文件里。
  # mv 实现下这些行进已 unlink 的旧 inode：可见文件永冻、磁盘无界增长。
  printf 'after-rotate-1\nafter-rotate-2\n' >&9
  exec 9>&-
  _n2="$(wc -l < "$LOG_ENGINE_PATH" | tr -d ' ')"
  [ "$_n2" = 402 ] && ok "R4 轮转后经同一 fd 的写入落在可见文件（402 行）" || no "R4 同一 fd 写入可见（得到 $_n2 行）"
  _last="$(tail -n 1 "$LOG_ENGINE_PATH")"
  [ "$_last" = "after-rotate-2" ] && ok "R5 新写入内容在文件尾部" || no "R5 新写入内容在文件尾部（$_last）"

  [ ! -e "$LOG_ENGINE_PATH.tmp" ] && ok "R6 无 .tmp 残留" || no "R6 无 .tmp 残留"

  # R7：低于 cap 不轮转（watchdog 用默认 256KB cap）
  printf 'small\n' >>"$LOG_WATCHDOG_PATH"
  _w0="$(wc -c < "$LOG_WATCHDOG_PATH" | tr -d ' ')"
  log_rotate_all
  _w1="$(wc -c < "$LOG_WATCHDOG_PATH" | tr -d ' ')"
  [ "$_w0" = "$_w1" ] && ok "R7 低于 cap 不轮转" || no "R7 低于 cap 不轮转（$_w0 → $_w1）"

  # R8：dns 日志超 cap 同样截断（50 行全留：tail -n 400 取 min）
  LOG_DNS_CAP=10
  i=0
  while [ "$i" -lt 50 ]; do printf 'dns-%d\n' "$i" >>"$LOG_DNS_PATH"; i=$((i + 1)); done
  log_rotate_all
  _d="$(wc -l < "$LOG_DNS_PATH" | tr -d ' ')"
  [ "$_d" = 50 ] && ok "R8 dns 日志轮转后保留 50 行" || no "R8 dns 日志轮转（得到 $_d）"

  # R9：engine 日志（402 行 ≈ 4.8KB > 200 cap）被 log_rotate_all 收到 400 行
  _n3="$(wc -l < "$LOG_ENGINE_PATH" | tr -d ' ')"
  [ "$_n3" = 400 ] && ok "R9 log_rotate_all 覆盖 engine（402 → 400）" || no "R9 log_rotate_all 覆盖 engine（得到 $_n3）"

  # R10：再跑一轮，内容不变（幂等）
  log_rotate_all
  _n4="$(wc -l < "$LOG_ENGINE_PATH" | tr -d ' ')"
  _d2="$(wc -l < "$LOG_DNS_PATH" | tr -d ' ')"
  [ "$_n4" = 400 ] && [ "$_d2" = 50 ] && ok "R10 二次轮转幂等（内容不变）" || no "R10 二次轮转幂等（engine $_n4 / dns $_d2）"

  echo
  echo "结果：通过 $PASS / 失败 $FAIL"
  [ "$FAIL" = 0 ]
)
rc=$?
exit "$rc"
