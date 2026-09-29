#!/system/bin/sh
# wait.sh — 「等就绪 / 等消失」的唯一实现（可 source 的库；source 时零副作用、无外部依赖）
#
# 为什么有它（架构候选 6）：这个语义原先在**四处**各写一遍轮询 ——
#   watchdog.sh 的 wait_engine（拉起后等 10×1s）、lifecycle.sh 的 life_stop_all（等退出 50×0.2s）、
#   life_restart_engine（等健康 20×2s）、tools/device/test-lifecycle.sh（3 处等新 pid）。
# 代价已经付过一次：ADR-0004 的"固定 3s 就断言拉起失败"误报，当时只修了 watchdog 那一份 ——
# 因为另外三份在别处，谁也没想起它们也是同一条语义。
#
# 接口（就三件）
#   wait_for  <次数> <步长秒> <谓词命令...>   谓词为真 → 0；跑满次数仍假 → 1
#   wait_gone <次数> <步长秒> <pid...>        所有 pid 都"真的退出了"（含僵尸态）→ 0
#   wait_pid_gone <pid>                       单个 pid 是否真的退出了
#
# 语义要点（调用方依赖这些）
#   - **先判定、再睡觉**：谓词一开始就为真时零等待（调用方常拿它当"已经就绪吗"用）
#   - 次数与步长都是整数（Android 的 sh 没有 bc）；`sleep 0.2` 这类小数值只作步长，不参与算术
#   - 谓词在**当前 shell** 执行（不 fork）：调用方可以让谓词顺手落一个变量
#     （真机门禁就这么用：读到新 pid 后把它带出来）
#   - 僵尸仍能被 `kill -0` 命中，所以"退出"判定必须看 /proc/<pid>/stat 的状态位，
#     否则等待循环会白跑满超时

wait_pid_gone() {
  _wp="${1:-}"
  [ -n "$_wp" ] || return 0                       # 空 pid = 从来没有这个进程
  kill -0 "$_wp" 2>/dev/null || return 0          # 已经不在
  _ws="$(sed -n 's/^[^)]*) \([A-Z]\).*/\1/p' "/proc/$_wp/stat" 2>/dev/null)"
  [ "$_ws" = "Z" ] && return 0                    # 僵尸：已死，但 kill -0 还会成功
  return 1
}

# 谓词：**所有**给定 pid 都已消失
wait_pids_gone() {
  for _wp in "$@"; do
    wait_pid_gone "$_wp" || return 1
  done
  return 0
}

wait_for() {
  _wn="${1:-0}"
  _wd="${2:-1}"
  shift 2
  case "$_wn" in ''|*[!0-9]*) return 1 ;; esac    # 次数非法：拒绝（不默认放行）
  [ "$_wn" -gt 0 ] || return 1
  _wi=0
  while [ "$_wi" -lt "$_wn" ]; do
    "$@" && return 0
    _wi=$((_wi + 1))
    [ "$_wi" -lt "$_wn" ] && sleep "$_wd"          # 最后一轮不再白等
  done
  return 1
}

wait_gone() {
  _wn="${1:-0}"
  _wd="${2:-1}"
  shift 2
  wait_for "$_wn" "$_wd" wait_pids_gone "$@"
}
