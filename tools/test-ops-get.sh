#!/bin/sh
# tools/test-ops-get.sh — 断言 ops.sh 的**键访问器**与**单行契约**（action.sh 那类消费者的网）。
#
# 为什么有它（2026-09-29 架构走查 A4）：`ops.sh status/panel` 是**一行**空格分隔的 k=v，
# 而 `module/action.sh` 曾自己拿 `grep "^key="` 去解析它 —— 行锚匹配**行中间的键永远不中**
# （engine= 显示成空），而**行首的键**（port=）会命中整行、把残余当值吐出来
# （`curl http://127.0.0.1:$(kv port)/health` 必然失败）。
# 解析收敛到 `ops.sh get` 之后，用这组断言把契约钉住：中间键、行首键、多键、缺键、非法键名。
#
# 用法：在仓库根执行 `sh tools/test-ops-get.sh`
set -u
OPS="./module/lib/ops.sh"
ACT="./module/action.sh"
if [ ! -f "$OPS" ]; then
  echo "找不到 $OPS（请在仓库根执行）" >&2
  exit 1
fi

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); printf '  ✅ %s\n' "$*"; }
no() { FAIL=$((FAIL + 1)); printf '  ❌ %s\n' "$*"; }

# 假数据目录：只放一个端口文件与一个**已死**的 pidfile —— 值断言全部相对真实 status 输出，
# 因此不依赖引擎真实状态（本测试要钉的是"解析契约"，不是"引擎在不在"）。
TMPD="$(mktemp -d 2>/dev/null || echo /tmp/opsget.$$)"
mkdir -p "$TMPD"
printf '20133\n' > "$TMPD/port"
printf '99999\n' > "$TMPD/9router.pid"
cleanup() { rm -rf "$TMPD"; }
trap cleanup EXIT
run_ops() { DATA_DIR="$TMPD" sh "$OPS" "$@" 2>/dev/null; }
# 参照实现：先按空白切行再匹配（**与本仓库真机门禁里的读法一致**）
st_kv() { printf '%s' "$ST" | tr ' ' '\n' | sed -n "s/^$1=//p" | head -1; }

ST="$(run_ops status)"

echo "== S 单行契约（消费者依赖的结构）=="
if [ -n "$ST" ] && [ "$(printf '%s' "$ST" | wc -l | tr -d ' ')" = 0 ]; then
  ok "S1 status 输出恰好一行（无换行）"
else
  no "S1 status 不是单行（或为空）"
fi
BAD=""
for tok in $ST; do
  case "$tok" in *=*) ;; *) BAD="$tok" ;; esac
done
[ -z "$BAD" ] && ok "S2 status 每个 token 都是 k=v（没有裸词）" || no "S2 出现非 k=v 的 token：「$BAD」"
if [ "$(run_ops panel | wc -l | tr -d ' ')" = 1 ] && [ -n "$(run_ops panel)" ]; then
  ok "S3 panel 也是单行（promise 降级形态只保留末行）"
else
  no "S3 panel 不是单行（行数 $(run_ops panel | wc -l | tr -d ' ')）"
fi

echo "== G 访问器：行首键 / 中间键 / 多键 / 缺键 / 非法键名 =="
[ "$(run_ops get port)" = "port=$(st_kv port)" ] && ok "G1 行首键 port 取值正确" \
  || no "G1 port 得到「$(run_ops get port)」，期望「port=$(st_kv port)」"
case "$(run_ops get port)" in
  *" "*) no "G1b port 的值含空格（旧 bug 的整行残余）" ;;
  *) ok "G1b port 值不含空格" ;;
esac
[ "$(run_ops get port)" = "port=20133" ] && ok "G1c port 取自数据目录（20133）" || no "G1c port 没取到 20133"
[ "$(run_ops get engine)" = "engine=$(st_kv engine)" ] && ok "G2 中间键 engine 取值正确" \
  || no "G2 engine 得到「$(run_ops get engine)」，期望「engine=$(st_kv engine)」"
[ "$(run_ops get engine_pid)" = "engine_pid=$(st_kv engine_pid)" ] && ok "G2b 中间键 engine_pid 取值正确" \
  || no "G2b engine_pid 得到「$(run_ops get engine_pid)」"
MULTI="$(run_ops get port engine dns)"
if printf '%s\n' "$MULTI" | grep -q '^port=' && printf '%s\n' "$MULTI" | grep -q '^engine=' && printf '%s\n' "$MULTI" | grep -q '^dns='; then
  ok "G3 多键一次调用 → 每键一行且齐全"
else
  no "G3 多键输出不全：$(printf '%s' "$MULTI" | tr '\n' '|')"
fi
OUT4="$(run_ops get no_such_key_zzz)"
RC4=$?
if [ -z "$OUT4" ] && [ "$RC4" != 0 ]; then
  ok "G4 不存在的键 → 无输出且退出非 0（读不到 ≠ 空结果）"
else
  no "G4 缺键处理错（输出「$OUT4」退出 $RC4）"
fi
run_ops get 'engine=.*' >/dev/null 2>&1 && no "G5 非法键名被接受（会被当正则用）" || ok "G5 非法键名被拒"

echo "== A action.sh 端到端（真实消费者，值与 status 对齐）=="
A_OUT="$(DATA_DIR="$TMPD" sh "$ACT" 2>/dev/null)"
printf '%s\n' "$A_OUT" | grep -q "端口     : 20133$" && ok "A1 action.sh 端口行干净（就是 20133，无残余）" \
  || no "A1 action.sh 端口行不对：$(printf '%s\n' "$A_OUT" | grep 端口)"
printf '%s\n' "$A_OUT" | grep -q "引擎     : $(st_kv engine) (PID $(st_kv engine_pid))" && ok "A2 action.sh 引擎行与 status 一致" \
  || no "A2 action.sh 引擎行不对：$(printf '%s\n' "$A_OUT" | grep 引擎)"
printf '%s\n' "$A_OUT" | grep -q "DNS      : $(st_kv dns)" && ok "A3 action.sh DNS 行与 status 一致" \
  || no "A3 action.sh DNS 行不对：$(printf '%s\n' "$A_OUT" | grep DNS)"

echo "== 结果：通过 $PASS / 失败 $FAIL =="
[ "$FAIL" = 0 ]
