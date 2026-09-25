#!/bin/bash
# tools/deploy-device.sh — 模块文件直推真机（开发迭代用，发布仍走 build.sh 完整打包）
#
# 为什么必须用本脚本而不是手动 adb push：仓库源文件含构建期占位符 __MOD_ID__
# （build.sh 第 6 步注入）。手动 push 会把占位符原样带到设备 → CFG.MODDIR 错误
# → 面板"获取不到信息"（2026-09-26 真机事故）。本脚本把注入、推送、权限、
# 自检固化为一条命令，杜绝手工步骤遗漏。
#
# 用法：tools/deploy-device.sh [设备序列号]
#   设备序列号缺省取 `adb devices` 中第一台 usb 设备。
# 推送内容：lib/ops.sh、webroot/{index.html,app.js,bridge.js,parsers.js}、etc/engine-version
# 自检：注入后占位符计数必须为 0；远端执行 ops.sh panel 显示 engine=up。
set -euo pipefail
cd "$(dirname "$0")/.."

MOD_ID="$(grep '^id=' module/module.prop | cut -d= -f2)"
SERIAL="${1:-}"
ADB="adb"
[ -n "$SERIAL" ] && ADB="adb -s $SERIAL"

step() { echo "== $* =="; }
die() { echo "❌ $*" >&2; exit 1; }

step "1/4 目标设备"
[ -n "$SERIAL" ] || SERIAL="$($ADB devices | awk '$2=="device" && $1 !~ /emulator/ {print $1; exit}')"
[ -n "$SERIAL" ] || die "未找到已连接设备"
echo "设备: $SERIAL"

step "2/4 注入占位符 → 暂存"
STAGING="$(mktemp -d)"
trap 'rm -rf "$STAGING"' EXIT
mkdir -p "$STAGING/webroot" "$STAGING/lib" "$STAGING/etc"
for f in webroot/index.html webroot/app.js webroot/bridge.js webroot/parsers.js; do
  sed "s/__MOD_ID__/${MOD_ID}/g" "module/$f" > "$STAGING/$f"
done
cp module/lib/ops.sh "$STAGING/lib/"
cp module/etc/engine-version "$STAGING/etc/" 2>/dev/null || true
if grep -rq "__MOD_ID__" "$STAGING"; then
  die "注入后仍有 __MOD_ID__ 残留"
fi
echo "占位符注入完成（0 残留）"

step "3/4 推送到设备"
$ADB push "$STAGING/lib/ops.sh" /data/local/tmp/d-opssh >/dev/null
$ADB push "$STAGING/webroot" /data/local/tmp/d-webroot >/dev/null
$ADB push "$STAGING/etc" /data/local/tmp/d-etc >/dev/null

step "4/4 设备侧落位 + 自检"
$ADB shell "su -c '
M=/data/adb/modules/$MOD_ID
cp /data/local/tmp/d-opssh \$M/lib/ops.sh
cp /data/local/tmp/d-webroot/* \$M/webroot/
[ -f /data/local/tmp/d-etc/engine-version ] && cp /data/local/tmp/d-etc/engine-version \$M/etc/engine-version
chmod 0755 \$M/lib/ops.sh
rm -rf /data/local/tmp/d-opssh /data/local/tmp/d-webroot /data/local/tmp/d-etc
if grep -q __MOD_ID__ \$M/webroot/index.html; then echo \"FAIL: 占位符残留\"; exit 1; fi
\$M/lib/ops.sh panel | head -c 120; echo
'"
echo "✅ 部署完成：$SERIAL"
