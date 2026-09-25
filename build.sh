#!/bin/bash
# 9router-go-magisk · 模块构建脚本
# 产物：dist/9router-go-<version>-arm64.zip（可直接在 KernelSU / Magisk 刷入）
#
# 步骤：
#   1) 构建 web 前端（Svelte/Vite）→ web/dist
#   2) 交叉编译引擎 linux/arm64（CGO_ENABLED=0 静态，嵌入 web/dist）
#   3) 打包 module/ 为 Magisk 模块 zip
#
# dnsfwd 不在此构建（arm64 C 交叉编译需 sysroot），使用 module/bin/dnsfwd 预编译产物；
# 需要重编时见 tools/build-dnsfwd.sh。
set -euo pipefail
cd "$(dirname "$0")"

VERSION="$(cat VERSION 2>/dev/null || echo 0.0.0)"
OUT_DIR="dist"
MOD_ID="$(grep '^id=' module/module.prop | cut -d= -f2)"
ZIP_NAME="${MOD_ID}-${VERSION}-arm64.zip"

echo "== 1/3 web 前端 =="
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

echo "== 2/3 引擎交叉编译 (linux/arm64) =="
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
  -ldflags "-s -w -X '9router/proxy/internal/updater.CurrentVersion=${VERSION}'" \
  -o module/bin/9router-go ./cmd/9router-go/
chmod 0755 module/bin/9router-go

echo "== 3/3 打包模块 zip =="
mkdir -p "$OUT_DIR"
rm -f "${OUT_DIR}/${ZIP_NAME}"
if command -v zip >/dev/null 2>&1; then
  (cd module && zip -r9 "../${OUT_DIR}/${ZIP_NAME}" \
    module.prop customize.sh service.sh action.sh uninstall.sh webroot etc bin \
    -x 'bin/*.o' > /dev/null)
else
  echo "未安装 zip，改用 python 打包"
  (cd module && python3 - "$OUT_DIR/$ZIP_NAME" <<'PY'
import sys, zipfile, os
out = sys.argv[1]
with zipfile.ZipFile(out, 'w', zipfile.ZIP_DEFLATED, compresslevel=9) as z:
    for root, dirs, files in os.walk('.'):
        for f in files:
            p = os.path.join(root, f)
            if f.endswith('.o'): continue
            z.write(p, os.path.relpath(p, '.'))
print('packed', out)
PY
)
fi

ls -lh "${OUT_DIR}/${ZIP_NAME}"
echo "完成：${OUT_DIR}/${ZIP_NAME}"
