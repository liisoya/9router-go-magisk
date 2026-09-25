#!/system/bin/sh
# 9router-go · 安装期检查
# 仅用 POSIX shell，不依赖 Magisk 专有函数（KernelSU / Magisk 通用）

MODPATH="${MODPATH:-${0%/*}}"

abi="$(getprop ro.product.cpu.abi 2>/dev/null)"
case "$abi" in
  arm64*|aarch64*) ;;
  *)
    echo "9router-go: 仅支持 arm64，当前为 $abi"
    if command -v abort >/dev/null 2>&1; then abort "不支持的架构：$abi"; fi
    exit 1
    ;;
esac

# 可执行位兜底：zip 里脚本/二进制若为 644，装完会跑不起来
for f in "$MODPATH"/*.sh "$MODPATH"/bin/*; do
  [ -f "$f" ] && chmod 0755 "$f" 2>/dev/null
done

# 数据目录（全新安装；不迁移任何旧模块数据）
mkdir -p "${DATA_DIR:-/data/adb/9router-go}/db" 2>/dev/null

echo "9router-go: 架构检查通过（$abi）"
exit 0
