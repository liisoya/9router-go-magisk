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
# 推送内容：lib/{ops.sh,watchdog.sh}、service.sh、
#           webroot/{index.html,app.js,bridge.js,parsers.js}、etc/engine-version
# 自检：注入后占位符计数必须为 0；远端执行 ops.sh panel 显示 engine=up 且 watchdog=up。
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
cp module/webroot/index.html module/webroot/app.js module/webroot/bridge.js module/webroot/parsers.js "$STAGING/webroot/"
cp module/lib/*.sh "$STAGING/lib/"
cp module/service.sh "$STAGING/"
cp module/etc/engine-version "$STAGING/etc/" 2>/dev/null || true
# 注入唯一实现（与 build.sh 同一份；全树注入 + 自己断言零残留）。
# 此前这里手写 sed + 列了 4 个文件，与 build.sh 的 2 个文件清单不同 —— 加新占位符文件时会漏。
sh tools/inject-mod-id.sh module "$STAGING"

step "3/4 推送到设备"
$ADB push "$STAGING/lib" /data/local/tmp/d-lib >/dev/null
$ADB push "$STAGING/service.sh" /data/local/tmp/d-service.sh >/dev/null
$ADB push "$STAGING/webroot" /data/local/tmp/d-webroot >/dev/null
$ADB push "$STAGING/etc" /data/local/tmp/d-etc >/dev/null

step "4/4 设备侧落位 + 自检"
$ADB shell "su -c '
M=/data/adb/modules/$MOD_ID
cp /data/local/tmp/d-lib/* \$M/lib/
cp /data/local/tmp/d-service.sh \$M/service.sh
cp /data/local/tmp/d-webroot/* \$M/webroot/
[ -f /data/local/tmp/d-etc/engine-version ] && cp /data/local/tmp/d-etc/engine-version \$M/etc/engine-version
chmod 0755 \$M/*.sh \$M/lib/*.sh
rm -rf /data/local/tmp/d-lib /data/local/tmp/d-service.sh /data/local/tmp/d-webroot /data/local/tmp/d-etc
if grep -q __MOD_ID__ \$M/webroot/index.html; then echo \"FAIL: 占位符残留\"; exit 1; fi
[ -x \$M/lib/watchdog.sh ] || { echo \"FAIL: lib/watchdog.sh 不可执行\"; exit 1; }
[ -x \$M/lib/lifecycle.sh ] || { echo \"FAIL: lib/lifecycle.sh 不可执行\"; exit 1; }
\$M/lib/ops.sh panel | head -c 160; echo
'"
echo "✅ 部署完成：$SERIAL"
