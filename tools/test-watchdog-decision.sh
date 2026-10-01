#!/bin/sh
# tools/test-watchdog-decision.sh — watchdog.sh 判定核的**离线**自证
# （2026-10-01 架构走查候选 3；与 tools/test-lifecycle-lib.sh 同风格）
#
# 起因：守护主循环 336 行此前离线零覆盖 —— A2/A3/S3/S4 四条架构审查规则的"测试"是
# 真机档 + 注释，改轮询间隔/去抖窗口都可能悄悄破坏一条，离线看不见。
#
# 做法：`_WD_LIB_ONLY=1` source（库模式：只暴露判定核，不进主循环），
#   · 纯规则（去抖 / S3 记账 / S4 位保持）直接断言返回码；
#   · wd_bring_up / wd_death_evidence 用桩造场景（started vs running、pid 消失 vs 号被复用）；
#   · A2 的"校正必须在确认-退出之后"是顺序不变量，用源码行号断言守。
# 循环体的 I/O 编排（trap / fork / CHLD 打断 sleep 是不是真的会醒）仍由真机档回归。
#
# 运行：sh tools/test-watchdog-decision.sh（已接进 tools/check.sh --offline）
set -u
cd "$(dirname "$0")/.." || exit 1

_TMPD="$(mktemp -d 2>/dev/null)" || { echo "❌ 无法创建临时目录"; exit 2; }
trap 'rm -rf "$_TMPD"' EXIT

MODDIR="$PWD/module"
DATA_DIR="$_TMPD"
export MODDIR DATA_DIR
_WD_LIB_ONLY=1
export _WD_LIB_ONLY
. module/lib/watchdog.sh || { echo "❌ 无法 source module/lib/watchdog.sh"; exit 2; }

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); echo "  ✅ $*"; }
no() { FAIL=$((FAIL + 1)); echo "  ❌ $*"; }

echo "== W1 去抖判定（wd_should_act）：事件确认立刻动手，无事件须连续两次 =="
wd_should_act 1 0  && ok "W1a CHLD 已确认（miss=0）→ 立刻动手" || no "W1a 事件确认却不去抖放行"
wd_should_act 0 2  && ok "W1b 连续两次判死 → 动手" || no "W1b 去抖窗口后仍不动手"
wd_should_act 0 1  && no "W1c 单次未确认就动手（去抖失效）" || ok "W1c 单次未确认 → 不动手（去抖）"
wd_should_act 1 5  && ok "W1d 确认与计数并存 → 仍动手" || no "W1d 确认位被计数压掉"

echo "== W2 S3 记账判定（wd_own_if_started）：只有亲手拉起才记归属 =="
wd_own_if_started started && ok "W2a started → 记归属（S3 允许）" || no "W2a started 却不记归属"
wd_own_if_started running && no "W2b running（别人起的）也记归属（伪造退出码 127 并绕过去抖）" \
                           || ok "W2b running → 不记归属（S3）"
wd_own_if_started ""       && no "W2c 空回词也记归属" || ok "W2c 空回词 → 不记归属"

echo "== W3 S4 位保持（wd_keep_chld_pending）：有在管子进程不清 CHLD 位 =="
wd_keep_chld_pending 123 ""   && ok "W3a 有 eng_ours → 保持置位（死因不丢）" || no "W3a eng_ours 在场却清位"
wd_keep_chld_pending ""  456  && ok "W3b 只有 dns_ours → 同样保持" || no "W3b dns_ours 在场却清位"
wd_keep_chld_pending ""  ""   && no "W3c 无子进程仍保持位（下一轮空转）" || ok "W3c 无子进程 → 清位（防空转）"

echo "== W4 wd_bring_up：拉起→等就绪→S3 记账→锚点→日志，三场景 =="
# 公共桩：cgroup/oom 链路（mark_engine_alive 与 oom_delta 的取证锚点），写进 _TMPD
life_cgroup_of() { echo "/testing"; }
life_cgroup_memory_events() { echo ""; return 1; }   # 模拟"本机无 memory.events"→ 如实空
life_engine_pid() { echo 4242; }

# 场景 a：亲手拉起（started）且就绪 → 记 eng_ours + 成功日志
eng_ours=""; oom_base=""
life_ensure_engine() { echo started; }
life_engine_healthy() { return 0; }
if wd_bring_up "引擎已拉起" "拉起失败" && [ "$eng_ours" = "4242" ]; then
  ok "W4a started + 就绪 → eng_ours=4242（S3 记账）"
else
  no "W4a started 却没记归属（eng_ours=[$eng_ours]）"
fi

# 场景 b：引擎是别人起的（running）→ 不记归属，但仍算成功
eng_ours=""; oom_base=""
life_ensure_engine() { echo running; }
if wd_bring_up "引擎已拉起" "拉起失败" && [ -z "$eng_ours" ]; then
  ok "W4b running → 成功但不记归属（S3：不是子进程）"
else
  no "W4b running 被记了归属（eng_ours=[$eng_ours]）—— 伪造退出码的死因并绕过去抖"
fi

# 场景 c：拉起后仍不就绪 → 如实失败（桩 wait_engine 避免真等 10s）
eng_ours=""; oom_base=""
life_ensure_engine() { echo started; }
life_engine_healthy() { return 1; }
wait_engine() { return 1; }
if ! wd_bring_up "引擎已拉起" "拉起失败" && [ -z "$eng_ours" ]; then
  ok "W4c 不就绪 → 返回失败且不记归属（不谎报拉起成功）"
else
  no "W4c 不就绪却报成功或记了归属"
fi

echo "== W5 死因取证（wd_death_evidence）：pid 消失 vs 号被复用，两种死法可区分 =="
eng_cg=""; oom_base=""
life_engine_pid() { echo 999999; }   # 不存在的 pid
_ev="$(wd_death_evidence)"
case "$_ev" in
  *"已消失"*) ok "W5a pid 不存在 → 证据说「已消失（含僵尸态）」" ;;
  *)          no "W5a 消失场景取证错：「$_ev」" ;;
esac
life_engine_pid() { echo $$; }       # 当前 shell：活着、非僵尸 = "号被复用"的形状
_ev="$(wd_death_evidence)"
case "$_ev" in
  *"仍活着但已不是引擎"*) ok "W5b 活着的无关 pid → 证据说「号被复用」（面板谎报 up 的形状）" ;;
  *)                      no "W5b 复用场景取证错：「$_ev」" ;;
esac

echo "== W6 A2 顺序不变量：确认-退出分支必须先于归属校正（倒过来 wait 不到退出原因）=="
_w="$(grep -n 'wait "\$eng_ours"' module/lib/watchdog.sh | head -1 | cut -d: -f1)"
_c="$(grep -n '!= "\$eng_ours" ]; then eng_ours=""' module/lib/watchdog.sh | head -1 | cut -d: -f1)"
if [ -n "$_w" ] && [ -n "$_c" ] && [ "$_w" -lt "$_c" ]; then
  ok "W6 确认-退出(行 $_w) 在校正(行 $_c) 之前 —— wait 先消费，校正后清陈旧值"
else
  no "W6 顺序反了（wait=[$_w] 校正=[$_c]）—— CHLD 取证会拿到别的子进程的状态"
fi

echo "== 结果：通过 $PASS / 失败 $FAIL =="
[ "$FAIL" = 0 ]
