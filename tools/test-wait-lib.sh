#!/bin/sh
# tools/test-wait-lib.sh — lib/wait.sh（等就绪 / 等消失的唯一实现）的**离线**自证
#
# 为什么能在离线跑：wait.sh 零依赖（不需要 MODDIR / DATA_DIR / 设备），所以"轮询语义"这件事
# 第一次可以脱离真机验证 —— 这正是把它抽出来的附带收益（此前四处轮询只能上真机看）。
#
# 运行：sh tools/test-wait-lib.sh（已接进 tools/check.sh --offline）
set -u
cd "$(dirname "$0")/.." || exit 1
. module/lib/wait.sh || { echo "❌ 无法 source module/lib/wait.sh"; exit 2; }

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); echo "  ✅ $*"; }
no() { FAIL=$((FAIL + 1)); echo "  ❌ $*"; }

echo "== W1 wait_for：谓词立刻为真 → 成功且不白等 =="
_start="$(date +%s)"
if wait_for 10 1 true; then
  _el=$(( $(date +%s) - _start ))
  [ "$_el" -le 1 ] && ok "W1 零等待成功（${_el}s）" || no "W1 成功但白等了 ${_el}s"
else
  no "W1 应当成功"
fi

echo "== W2 wait_for：跑满次数仍假 → 如实失败 =="
_start="$(date +%s)"
if wait_for 2 1 false; then
  no "W2 假谓词却报了成功（会谎报『拉起成功』）"
else
  _el=$(( $(date +%s) - _start ))
  [ "$_el" -ge 1 ] && ok "W2 如实失败（等了 ~${_el}s）" || no "W2 失败得太快（没真的轮询）"
fi

echo "== W3 wait_for：次数非法（0 / 非数字）→ 拒绝 =="
if wait_for 0 1 true; then no "W3 次数 0 却成功"; else ok "W3 次数 0 → 失败"; fi
if wait_for abc 1 true; then no "W3b 非数字次数却成功"; else ok "W3b 非数字次数 → 失败"; fi

echo "== W4 谓词在当前 shell 执行（可带参数、可落变量）=="
_seen=""
_pick() { _seen="$1"; return 0; }
if wait_for 3 0.1 _pick hello && [ "$_seen" = "hello" ]; then
  ok "W4 谓词收到参数且变量可见"
else
  no "W4 谓词参数/变量没生效（_seen=${_seen}）"
fi

echo "== W5 wait_pid_gone：空 pid / 已退出 / 活着 =="
wait_pid_gone "" && ok "W5 空 pid → 视为已消失" || no "W5 空 pid 判错"
sleep 0.1 &
_p=$!
wait "$_p" 2>/dev/null
wait_pid_gone "$_p" && ok "W5b 已退出的 pid → 消失" || no "W5b 已退出却判还在"
sleep 5 &
_live=$!
wait_pid_gone "$_live" && no "W5c 活着的 pid 被判消失" || ok "W5c 活着的 pid → 未消失"
kill -9 "$_live" 2>/dev/null

echo "== W6 wait_gone：全部消失才算数 =="
sleep 0.1 &
_a=$!
wait_gone 5 0.1 "$_a" && ok "W6 已退出 → 成功" || no "W6 应当成功"
sleep 5 &
_b=$!
wait_gone 2 0.1 "$_b" && no "W6b 还活着却成功" || ok "W6b 仍活着 → 失败"
kill -9 "$_b" 2>/dev/null

echo "== 结果：通过 $PASS / 失败 $FAIL =="
[ "$FAIL" = 0 ] || exit 1
exit 0
