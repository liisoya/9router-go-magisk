#!/system/bin/sh
# tools/device/test-dashboard-api.sh — 仪表盘 API 功能门禁（真机执行，退出码即判定）
#
# 为什么有它：v1.9.2 升级撤掉了两个本地引擎补丁（/api/models/test 鉴权、备份导入 multipart），
# 并把 /version 系列改为公开。"撤补丁"的前提是"上游方案功能确实可用"，这条门禁把前提变成断言：
#   A1 /api/models/test 用 dashboard 会话可进入 handler（非 401）
#   A2 对照组：无凭据 /v1/models 仍 401（保护没有被削弱）
#   A3 备份导出鉴权：正确 x-9r-password → 200；错误 → 401；无凭据 → 401
#   A4 /version 已公开（无需 key）且 currentVersion 非空
#
# 用法：
#   adb push tools/device/test-dashboard-api.sh /data/local/tmp/
#   adb shell 'su -c "sh /data/local/tmp/test-dashboard-api.sh"'
#   # 用户改过密码时，第三个参数传当前密码（initial-password 只是首装默认值）：
#   adb shell 'su -c "sh /data/local/tmp/test-dashboard-api.sh /data/adb/modules/ninerouter-go /data/adb/9router-go <当前密码>"'
#
# 已知限制：A1/A3a 需要**当前** dashboard 密码。只读 initial-password（首装默认值）会在
# 用户改过密码时误判 —— 所以第三个参数优先，且登录失败时明确打印"跳过"而不是报失败。

MODDIR="${1:-/data/adb/modules/ninerouter-go}"
DATA_DIR="${2:-/data/adb/9router-go}"
OPS="$MODDIR/lib/ops.sh"
PORT="$("$OPS" get-port 2>/dev/null)"; PORT="${PORT:-20128}"
BASE="http://127.0.0.1:$PORT"
PW="${3:-$(cat "$DATA_DIR/initial-password" 2>/dev/null)}"
CJ="/data/local/tmp/9r-cj.$$"
TMP="/data/local/tmp/9r-api.$$"

PASS=0; FAIL=0
ok() { echo "  ✅ $1"; PASS=$((PASS + 1)); }
no() { echo "  ❌ $1"; FAIL=$((FAIL + 1)); }
info() { echo "  · $1"; }
code() { curl -s -o "$TMP" -w '%{http_code}' -m 10 "$@"; }

echo "== 仪表盘 API 功能门禁：$BASE =="

# A4 /version 公开可读（v1.9.2 起上游把它移出鉴权组）
c="$(code "$BASE/version")"
if [ "$c" = "200" ] && grep -q '"currentVersion":"' "$TMP"; then
  ok "A4 /version 公开可读（$(sed -n 's/.*"currentVersion":"\([^"]*\)".*/\1/p' "$TMP")）"
else
  no "A4 /version 期望 200 且含 currentVersion，实际 $c"
fi

# A3 备份导出鉴权（密码头是上游 shape；撤掉 multipart 补丁后仍必须可用）
c="$(code -H "x-9r-password: $PW" "$BASE/api/settings/database")"
[ "$c" = "200" ] && ok "A3a 正确密码头导出 → 200" || no "A3a 正确密码头导出期望 200，实际 $c"
c="$(code -H "x-9r-password: definitely-wrong" "$BASE/api/settings/database")"
[ "$c" = "401" ] && ok "A3b 错误密码 → 401" || no "A3b 错误密码期望 401，实际 $c"
c="$(code "$BASE/api/settings/database")"
[ "$c" = "401" ] && ok "A3c 无凭据 → 401" || no "A3c 无凭据期望 401，实际 $c"

# A1/A2 模型测试的鉴权归属（撤掉 models-test 补丁后由上游实现承担）
if [ -z "$PW" ]; then
  info "A1/A2 跳过：读不到 $DATA_DIR/initial-password（用户已改密码？）"
else
  c="$(code -c "$CJ" -X POST -H 'Content-Type: application/json' -d "{\"password\":\"$PW\"}" "$BASE/api/auth/login")"
  if [ "$c" = "200" ]; then
    c="$(code -b "$CJ" -X POST -H 'Content-Type: application/json' -d '{}' "$BASE/api/models/test")"
    if [ "$c" != "401" ]; then ok "A1 会话调 /api/models/test → $c（到达 handler，不再 401）"
    else no "A1 会话调 /api/models/test 仍是 401：$(head -c 160 "$TMP")"; fi
    c="$(code "$BASE/v1/models")"
    [ "$c" = "401" ] && ok "A2 对照组 无凭据 /v1/models → 401（保护未削弱）" || no "A2 无凭据 /v1/models 期望 401，实际 $c"
  else
    info "A1/A2 跳过：登录失败（$c）—— initial-password 与用户当前密码不一致？"
  fi
fi

rm -f "$CJ" "$TMP"
echo "== 结果：通过 $PASS / 失败 $FAIL =="
[ "$FAIL" = 0 ] || exit 1
exit 0
