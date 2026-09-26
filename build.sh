#!/bin/bash
# 9router-go-magisk · 模块构建脚本
# 产物：dist/<模块id>-<版本>-arm64.zip（可直接在 KernelSU / Magisk 刷入）
#
# 流程（每步有校验，失败即停）：
#   1) web 前端构建（Svelte/Vite → web/dist）
#   2) clipboard polyfill 注入（构建时补丁，CLIPBOARD_PATCH=0 关闭）
#   3) 解析层离线回归（node --test，SKIP_TESTS=1 跳过）
#   4) schema 漂移断言（对照上游 DATABASE.md，SKIP_SCHEMA_CHECK=1 跳过——不建议）
#   5) 引擎交叉编译 linux/arm64（CGO_ENABLED=0，嵌入 web/dist）
#   6) MODID 注入（webroot 的 __MOD_ID__ 占位符按 module.prop id 替换，staging 中进行）
#   7) 打包 + verify_zip（完整性 / module.prop / 版本一致性 / 占位符残留断言）
#
# dnsfwd 不在此构建（arm64 C 交叉编译需 sysroot），使用 module/bin/dnsfwd 预编译产物；
# 需要重编时见 tools/build-dnsfwd.sh。
set -euo pipefail
cd "$(dirname "$0")"

VERSION="$(cat VERSION 2>/dev/null || echo 0.0.0)"
OUT_DIR="dist"
MOD_ID="$(grep '^id=' module/module.prop | cut -d= -f2)"
MOD_VERSION="$(grep '^version=' module/module.prop | cut -d= -f2)"
# 发布包命名：9router-go-<上游版本>-r<修订>-magisk.zip
# 版本号与上游同步（1.9.1），r 序号 = 维护者修改版本（大版本更新时归 r1）
REL_VER="$(echo "$MOD_VERSION" | sed 's/^v//')"
ZIP_NAME="9router-go-${REL_VER}-magisk.zip"
STAGING="$(mktemp -d)"

cleanup() { rm -rf "$STAGING"; }
trap cleanup EXIT

step() { echo "== $* =="; }
die() { echo "❌ $*" >&2; exit 1; }

step "1/7 web 前端"
if [ ! -f web/dist/index.html ] || [ "${FORCE:-0}" = "1" ]; then
  if command -v bun >/dev/null 2>&1; then
    (cd web && bun install --frozen-lockfile && bun run build)
  else
    (cd web && [ -d node_modules ] || npm install --no-audit --no-fund
               npm run build)
  fi
else
  echo "web/dist 已存在，跳过（FORCE=1 强制重建）"
fi
[ -f web/dist/index.html ] || die "web/dist/index.html 不存在（前端构建失败？）"

step "2/7 clipboard polyfill 注入"
if [ "${CLIPBOARD_PATCH:-1}" = "1" ]; then
  python3 tools/patch-clipboard.py web/dist/index.html
  grep -q "9router-go-magisk clipboard polyfill" web/dist/index.html \
    || die "polyfill 注入后未找到标记（patch-clipboard.py 行为异常）"
else
  echo "CLIPBOARD_PATCH=0，跳过注入"
fi

step "3/7 仪表盘备份契约 + 模块层离线回归（解析层 / 命令构造器 / 键契约）"
# 仪表盘的 Download Backup 必须带 x-9r-password 头（上游 spec：settings/database/route.js:16）；
# 缺了就是 401 Invalid password（2026-09-26 用户报障）。这里在**构建产物**上再验一次，
# 挡住"改了 web/src 忘了重建 dist / 回退到裸 <a> 下载"这类静默回归。
# 注意：本断言在修复前是红的（旧 dist 里根本没有这个字符串）。
if grep -rq -- 'x-9r-password' web/dist/assets/ 2>/dev/null; then
  echo "仪表盘备份契约 ✅（x-9r-password 在构建产物里）"
else
  die "web/dist 里没有 x-9r-password：仪表盘下载备份会 401 Invalid password（见 docs/FIXPLAN Phase 20）"
fi
if [ "${SKIP_TESTS:-0}" = "1" ]; then
  echo "SKIP_TESTS=1，跳过"
else
  # 单一清单来源：离线门禁由 tools/check.sh 编排（清单见 docs/TESTING.md）。
  # CHECK_FAST=1 → 跳过 GO-TEST 与 TSC（本流程里的 `go build`、`bun run build` 已覆盖对应风险），
  # 避免构建时间翻倍；发布前请单独跑 `tools/check.sh --offline` 全套。
  CHECK_FAST=1 bash tools/check.sh --offline || die "离线门禁未通过（见 docs/TESTING.md）"
fi

step "4/7 schema 漂移断言"
if [ "${SKIP_SCHEMA_CHECK:-0}" = "1" ]; then
  echo "SKIP_SCHEMA_CHECK=1，跳过（不建议）"
else
  python3 tools/gen-schema.py --check
fi

step "5/7 引擎交叉编译 (linux/arm64)"
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
  -ldflags "-s -w -X '9router/proxy/internal/updater.CurrentVersion=${VERSION}'" \
  -o module/bin/9router-go ./cmd/9router-go/
chmod 0755 module/bin/9router-go
# 引擎真实版本落盘（ops.sh engine_version 的构建期来源；运行期由 install-engine 覆写）
printf '%s\n' "$VERSION" > module/etc/engine-version

step "6/7 staging + MODID 注入"
mkdir -p "$STAGING"
cp -r module "$STAGING/module"
# 注入唯一实现（全树注入 + 自己断言零残留）：此前是这里的 2 文件 sed 循环 + deploy-device.sh
# 里的另一份 4 文件 sed —— 清单不同，加新占位符文件时只改一处就会漏（见 tools/inject-mod-id.sh 头注释）
bash tools/inject-mod-id.sh module "$STAGING/module"
find "$STAGING/module" -name '*.sh' -exec chmod 0755 {} +
find "$STAGING/module/bin" -type f -exec chmod 0755 {} +

step "7/7 打包 + 校验"
mkdir -p "$OUT_DIR"
rm -f "${OUT_DIR}/${ZIP_NAME}"
# 纯净发布：webroot/test（离线测试）不随模块分发，仅保留在仓库供回归
(cd "$STAGING/module" && zip -r9 "${OLDPWD}/${OUT_DIR}/${ZIP_NAME}" \
  module.prop customize.sh service.sh action.sh uninstall.sh lib webroot etc bin \
  -x 'bin/*.o' 'webroot/test/*' > /dev/null)

verify_zip() {
  local zip_path="$1"
  local ex="$STAGING/verify"
  mkdir -p "$ex"
  unzip -t "$zip_path" > /dev/null || die "zip 完整性校验失败"
  unzip -q -o "$zip_path" -d "$ex" || die "zip 解压失败"
  [ -f "$ex/module.prop" ] || die "zip 缺 module.prop"
  [ -f "$ex/lib/ops.sh" ] || die "zip 缺 lib/ops.sh"
  [ -f "$ex/lib/lifecycle.sh" ] || die "zip 缺 lib/lifecycle.sh（生命周期唯一所有者）"
  [ -f "$ex/lib/log.sh" ] || die "zip 缺 lib/log.sh（日志策略唯一所有者）"
  [ -f "$ex/lib/wait.sh" ] || die "zip 缺 lib/wait.sh（等就绪/等消失唯一所有者）"
  [ -f "$ex/lib/watchdog.sh" ] || die "zip 缺 lib/watchdog.sh（生命周期守护）"
  [ -f "$ex/service.sh" ] || die "zip 缺 service.sh"
  [ -f "$ex/webroot/index.html" ] || die "zip 缺 webroot/index.html"
  grep -q "^version=${MOD_VERSION}$" "$ex/module.prop" \
    || die "module.prop 版本与预期不符（期望 ${MOD_VERSION}）"
  grep -rq "__MOD_ID__" "$ex/webroot" && die "webroot 有未注入的 __MOD_ID__ 占位符"
  if [ "${CLIPBOARD_PATCH:-1}" = "1" ]; then
    grep -q "clipboard polyfill" "$ex/bin/9router-go" \
      || die "引擎二进制未包含 polyfill 注入标记（web/dist 可能是旧的，用 FORCE=1 重建）"
  fi
  echo "verify_zip ✅"
}
verify_zip "${OUT_DIR}/${ZIP_NAME}"

ls -lh "${OUT_DIR}/${ZIP_NAME}"
echo "完成：${OUT_DIR}/${ZIP_NAME}"
