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
# （lib/ops.sh 是 WebUI/service.sh/action.sh 的统一 seam，漏掉会导致
#   mksh "can't execute: Permission denied"、引擎无法拉起）
for f in "$MODPATH"/*.sh "$MODPATH"/lib/*.sh "$MODPATH"/bin/*; do
  [ -f "$f" ] && chmod 0755 "$f" 2>/dev/null
done

# 数据目录（全新安装；不迁移任何旧模块数据）
DATA_DIR="${DATA_DIR:-/data/adb/9router-go}"
mkdir -p "$DATA_DIR/db" 2>/dev/null

# ── 引擎保留：管理器刷包不得把面板/引擎更新的成果降级（2026-10-02 用户决策）──────────
# 管理器刷包不跑 ops.sh，包内 bin/ 会整体覆盖在跑的引擎。模块稳定、引擎高频更新是常态，
# "包内引擎基线旧于在跑引擎"是普遍形状 —— 保留在跑的那份，两条更新通道才不打架。
# 本脚本运行时：MODPATH = 新包暂存目录，旧模块仍在 /data/adb/modules/<id>（管理器稍后才换）。
# 记账必须写齐（engine-version=保留值 / engine-version-code=新包 versionCode）：否则装完
# 首次读状态时，engine_version_sync 的自愈判据看到记录与 versionCode 不符，会把刚保留的
# 版本"自愈"回包内旧值 → 面板谎报降级。降级只在"包内基线更新或相同"时发生（整包更新的本职）。
NEWID="$(sed -n 's/^id=//p' "$MODPATH/module.prop" 2>/dev/null | tr -d ' \r')"
OLDDIR="/data/adb/modules/$NEWID"
PKG_VER="$(cat "$MODPATH/etc/engine-version" 2>/dev/null | tr -d ' \r')"
RUN_VER="$(cat "$DATA_DIR/engine-version" 2>/dev/null | tr -d ' \r')"
if [ -n "$NEWID" ] && [ -d "$OLDDIR" ] && [ -f "$OLDDIR/bin/9router-go" ] \
   && [ -n "$RUN_VER" ] && [ -n "$PKG_VER" ]; then
  # 版本比较（仅 x.y.z 数字段，每段 3 位拼成整数；toybox sort 无 -V 保证，不依赖）
  RUNK="$(printf '%s' "$RUN_VER" | awk -F. '{printf "%03d%03d%03d",$1+0,$2+0,$3+0}')"
  PKGK="$(printf '%s' "$PKG_VER" | awk -F. '{printf "%03d%03d%03d",$1+0,$2+0,$3+0}')"
  if [ -n "$RUNK" ] && [ -n "$PKGK" ] && [ "$RUNK" -gt "$PKGK" ] 2>/dev/null; then
    cp "$OLDDIR/bin/9router-go" "$MODPATH/bin/9router-go" 2>/dev/null
    if cmp -s "$OLDDIR/bin/9router-go" "$MODPATH/bin/9router-go"; then
      # 复制成功才保留；连 .bak 回滚点一起搬（它是"最后一次已验证可用"）
      cp "$OLDDIR/bin/9router-go.bak" "$MODPATH/bin/9router-go.bak" 2>/dev/null
      chmod 0755 "$MODPATH/bin/9router-go" 2>/dev/null
      NEWCODE="$(sed -n 's/^versionCode=//p' "$MODPATH/module.prop" 2>/dev/null | tr -d ' \r')"
      printf '%s\n' "$RUN_VER" > "$DATA_DIR/engine-version" 2>/dev/null
      printf '%s\n' "$NEWCODE" > "$DATA_DIR/engine-version-code" 2>/dev/null
      echo "9router-go: 包内引擎基线 $PKG_VER 旧于在跑的 $RUN_VER —— 已保留你更新的引擎（不降级）"
    fi
  fi
fi

echo "9router-go: 架构检查通过（$abi）"
exit 0
