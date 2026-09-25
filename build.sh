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

step "3/7 解析层离线回归"
if [ "${SKIP_TESTS:-0}" = "1" ]; then
  echo "SKIP_TESTS=1，跳过"
elif command -v node >/dev/null 2>&1; then
  node --test module/webroot/test/parsers.test.js
else
  echo "node 不可用，跳过"
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
for f in "$STAGING"/module/webroot/index.html "$STAGING"/module/webroot/app.js; do
  [ -f "$f" ] && sed -i "s/__MOD_ID__/${MOD_ID}/g" "$f"
done
if grep -rq "__MOD_ID__" "$STAGING/module/webroot"; then
  die "webroot 仍有未注入的 __MOD_ID__ 占位符"
fi
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
