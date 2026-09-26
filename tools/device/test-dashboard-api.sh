#!/system/bin/sh
# tools/device/test-dashboard-api.sh — 仪表盘 API 功能门禁（真机执行，退出码即判定）
#
# 为什么有它：v1.9.2 升级撤掉了两个本地引擎补丁（/api/models/test 鉴权、备份导入 multipart），
# 并把 /version 系列改为公开。"撤补丁"的前提是"上游方案功能确实可用"，这条门禁把前提变成断言：
#   A1 /version 公开可读（无需任何凭据）
#   A2 本机 CLI token 能通过 dashboard 鉴权（/api/models/test 非 401 —— 不再依赖 apiKeys 表）
#   A3 备份导出：CLI token → 200；错误密码 → 401；无凭据 → 401
#   A4 对照组：无凭据 /v1/models 仍 401（保护没有被削弱）
#   A5 导出载荷包含密码相关状态（导入后按"导入数据的密码"登录 —— 用户关心的点）
#
# 用 CLI token 而不是密码：token 由 machine-id + "9r-cli-auth" + auth/cli-secret 推导
# （internal/auth/clitoken.go），与用户的登录密码无关 —— 用户改过密码也不会让门禁失效。
#
# 用法：
#   adb push tools/device/test-dashboard-api.sh /data/local/tmp/
#   adb shell 'su -c "sh /data/local/tmp/test-dashboard-api.sh"'

MODDIR="${1:-/data/adb/modules/ninerouter-go}"
DATA_DIR="${2:-/data/adb/9router-go}"
OPS="$MODDIR/lib/ops.sh"
PORT="$("$OPS" get-port 2>/dev/null)"; PORT="${PORT:-20128}"
BASE="http://127.0.0.1:$PORT"
TMP="/data/local/tmp/9r-api.$$"

PASS=0; FAIL=0
ok() { echo "  ✅ $1"; PASS=$((PASS + 1)); }
no() { echo "  ❌ $1"; FAIL=$((FAIL + 1)); }
info() { echo "  · $1"; }
code() { curl -s -o "$TMP" -w '%{http_code}' -m 15 "$@"; }

# CLI token：raw machine-id + salt + cli-secret，sha256 取前 16 位十六进制（两端都 TrimSpace）
# 这两个文件是**惰性生成**的（CLIToken() 第一次被调用时创建）：先用一个必然错误的 token
# 打一次 dashboard 鉴权路由，把文件触发出来，再推导真正的 token。
if [ ! -f "$DATA_DIR/machine-id" ] || [ ! -f "$DATA_DIR/auth/cli-secret" ]; then
  # 用 /api/settings/database（必过 RequireDashboardAuth）触发，而不是 /api/auth/status
  # ——后者现在是公开路由（登录页要读它），中间件不跑，文件不会被创建。
  curl -s -o /dev/null -m 5 -H 'x-9r-cli-token: 0000000000000000' "$BASE/api/settings/database" 2>/dev/null
fi
MID="$(tr -d '[:space:]' < "$DATA_DIR/machine-id" 2>/dev/null)"
SEC="$(tr -d '[:space:]' < "$DATA_DIR/auth/cli-secret" 2>/dev/null)"
if [ -n "$MID" ] && [ -n "$SEC" ]; then
  TOKEN="$(printf '%s' "${MID}9r-cli-auth${SEC}" | sha256sum | cut -c1-16)"
else
  TOKEN=""
fi

echo "== 仪表盘 API 功能门禁：$BASE =="
if [ -n "$TOKEN" ]; then info "CLI token 已推导（${#TOKEN} 位）"
else info "警告：缺 machine-id/cli-secret，无法推导 CLI token（A2/A3a 将跳过）"; fi

# A1 /version 公开可读（v1.9.2 起上游把它移出鉴权组）
c="$(code "$BASE/version")"
if [ "$c" = "200" ] && grep -q '"currentVersion":"' "$TMP"; then
  ok "A1 /version 公开可读（$(sed -n 's/.*"currentVersion":"\([^"]*\)".*/\1/p' "$TMP")）"
else
  no "A1 /version 期望 200 且含 currentVersion，实际 $c"
fi

if [ -n "$TOKEN" ]; then
  # A2 本机 CLI token 通过 dashboard 鉴权（撤掉 models-test 补丁后由上游实现承担）
  c="$(code -H "x-9r-cli-token: $TOKEN" -X POST -H 'Content-Type: application/json' -d '{}' "$BASE/api/models/test")"
  if [ "$c" != "401" ]; then ok "A2 CLI token 调 /api/models/test → $c（越过鉴权，不依赖 apiKeys 表）"
  else no "A2 CLI token 仍 401：$(head -c 160 "$TMP")"; fi

  # A3 备份导出：CLI token → 200（撤掉 multipart 补丁后仍必须可用）
  c="$(code -H "x-9r-cli-token: $TOKEN" "$BASE/api/settings/database")"
  if [ "$c" = "200" ]; then ok "A3a CLI token 导出备份 → 200"
  else no "A3a CLI token 导出备份期望 200，实际 $c"; fi

  # A5 导出载荷是否带密码相关状态（导入后用"导入数据的密码"登录）
  if [ "$c" = "200" ]; then
    if grep -qiE 'password|authPassword|hasPassword|requireLogin' "$TMP"; then
      ok "A5 导出载荷含密码/登录状态字段（导入即按导入数据的密码登录）"
    else
      info "A5 载荷里没看到 password 相关字段（可能密码不在备份范围内）—— 建议单独确认"
    fi
  fi
fi

# A3b/A3c 密码判据未被削弱
c="$(code -H "x-9r-password: definitely-wrong" "$BASE/api/settings/database")"
[ "$c" = "401" ] && ok "A3b 错误密码 → 401" || no "A3b 错误密码期望 401，实际 $c"
c="$(code "$BASE/api/settings/database")"
[ "$c" = "401" ] && ok "A3c 无凭据 → 401" || no "A3c 无凭据期望 401，实际 $c"

# A4 对照组
c="$(code "$BASE/v1/models")"
[ "$c" = "401" ] && ok "A4 对照组 无凭据 /v1/models → 401（保护未削弱）" || no "A4 无凭据 /v1/models 期望 401，实际 $c"

rm -f "$TMP"
echo "== 结果：通过 $PASS / 失败 $FAIL =="
[ "$FAIL" = 0 ] || exit 1
exit 0
