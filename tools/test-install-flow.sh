#!/bin/sh
# tools/test-install-flow.sh — **install 全路径**离线回归（2026-09-30 架构扫描 C2）
#
# 为什么有它：`install-engine` / `install-module` 是全仓最安全敏感的动作
#（门禁 → 回滚点 → 替换 → 重启 → 起来后才写版本与 .bak），但过去**成功与回滚两条路都没有测试**：
# ops.sh 的 MODDIR 写死成"脚本所在目录的上级"，install 只能真写真实模块目录，于是这两个分支
# 只在真机上赌；唯一的顺序保障是 test-install-gate.sh 的 grep 行号比较（文本形状，不是结构）。
#
# 本测试把 MODDIR 指到临时目录，用 `OPS_LIB_ONLY=1` 只加载 ops.sh 的定义（不跑 dispatch），
# 覆盖掉"进程那一层"的 lifecycle 函数，然后在**真实文件系统**上重放：
#   · 成功路径：引擎起来了 → 版本才写、.bak 才刷新、.prev 清理
#   · 回滚路径：引擎起不来 → 二进制回滚、版本**绝不谎报**
#   · 门禁：不合格源 / 不存在的源 → 绝不碰现有二进制；也不把不合格的当前文件当回滚点
#   · 装包：module.prop 换新、包内引擎版本落盘
# 运行：sh tools/test-install-flow.sh（由 tools/check.sh 编排）
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); echo "  ✅ $*"; }
no() { FAIL=$((FAIL + 1)); echo "  ❌ $*"; }

TMP="$(mktemp -d)" || exit 1
trap 'rm -rf "$TMP"' EXIT
MOD="$TMP/mod"
DATA="$TMP/data"
mkdir -p "$MOD/bin" "$MOD/etc" "$DATA"
cp -R "$ROOT/module/lib" "$MOD/lib" || exit 1
cp "$ROOT/module/module.prop" "$MOD/module.prop"

# 造一个"像引擎"的文件：≥5MB + ELF 魔数（engine_src_ok 的两个判据）。
# 必须带上**唯一标记**：否则每份假引擎内容完全相同，cksum 分不出"新装的那份"与"回滚回去的那份"，
# 断言会假绿（本轮就踩了一次：S2 的"失败的新引擎还在"因为三份文件字节相同而误报）。
mk_engine() {
  printf '\177ELF%s' "${1##*/}" > "$1" || return 1
  dd if=/dev/zero bs=1 count=0 seek=6291456 >> "$1" 2>/dev/null
  chmod 0755 "$1"
}
sum_of() { cksum < "$1" 2>/dev/null | tr -d '\n'; }

mk_engine "$MOD/bin/9router-go" || exit 1
ORIG_SUM="$(sum_of "$MOD/bin/9router-go")"
printf '%s\n' "0.0.1" > "$MOD/etc/engine-version"

echo "== 前置：以 OPS_LIB_ONLY=1 加载 ops.sh（只取定义，不跑 dispatch）=="
if ! ( OPS_LIB_ONLY=1 MODDIR="$MOD" DATA_DIR="$DATA" . "$MOD/lib/ops.sh" ) >/dev/null 2>&1; then
  echo "  ❌ 无法在只加载模式下 source ops.sh —— 需要 MODDIR 可覆盖 + OPS_LIB_ONLY 支持"
  echo "== 结果：通过 $PASS / 失败 $((FAIL + 1)) =="
  exit 1
fi
ok "ops.sh 可在只加载模式下 source"

# shellcheck disable=SC1090
OPS_LIB_ONLY=1 MODDIR="$MOD" DATA_DIR="$DATA" . "$MOD/lib/ops.sh" 2>/dev/null

# **安全护栏**：MODDIR 必须真的指向临时目录。否则后面的 mv 会写进仓库里的真实 module/ ——
# 宁可直接失败，绝不冒险（这也正是本测试在修复前应当"红"的地方）。
if [ "$MODDIR" != "$MOD" ]; then
  echo "  ❌ MODDIR 不可覆盖（期望 $MOD，实际 $MODDIR）—— 中止，绝不让测试写真实模块目录"
  echo "== 结果：通过 $PASS / 失败 $((FAIL + 1)) =="
  exit 1
fi
ok "MODDIR 可覆盖（install 不会碰真实模块目录）"

# 覆盖"进程那一层"：本测试只验编排（门禁/回滚点/版本落盘/回滚），不启真进程
life_stop_all() { :; }
life_wd_hold() { :; }
life_wd_hold_release() { :; }
life_restart_engine() { printf '%s\n' "${FAKE_RESTART:-engine=up}"; }

echo "== S1 成功路径：引擎起来了 → 才写版本、才刷新 .bak、清掉 .prev =="
NEW="$TMP/engine-new"
mk_engine "$NEW"
NEW_SUM="$(sum_of "$NEW")"
FAKE_RESTART=engine=up
OUT="$(cmd_install_engine "$NEW" 1.2.3)"
[ "$OUT" = "engine=up" ] && ok "输出 engine=up" || no "输出应为 engine=up，实际「$OUT」"
[ "$(sum_of "$MOD/bin/9router-go")" = "$NEW_SUM" ] && ok "新引擎已就位" || no "引擎未替换"
[ "$(cat "$DATA/engine-version" 2>/dev/null)" = "1.2.3" ] && ok "版本已写 1.2.3" || no "版本未写"
[ -s "$DATA/engine-version-code" ] && ok "记录了安装时的模块 versionCode" || no "engine-version-code 未写"
[ "$(sum_of "$MOD/bin/9router-go.bak" 2>/dev/null)" = "$NEW_SUM" ] \
  && ok ".bak 刷成"已验证可用"的这一份" || no ".bak 未刷新"
[ ! -f "$DATA/engine.prev" ] && ok "回滚点已清理" || no ".prev 残留"

echo "== S2 回滚路径：新引擎起不来 → 二进制回滚、版本绝不谎报 =="
BAD="$TMP/engine-bad"
mk_engine "$BAD"
BAD_SUM="$(sum_of "$BAD")"
BEFORE_SUM="$(sum_of "$MOD/bin/9router-go")"
BEFORE_VER="$(cat "$DATA/engine-version" 2>/dev/null)"
FAKE_RESTART=engine=down
OUT="$(cmd_install_engine "$BAD" 9.9.9)"
[ "$OUT" = "install-failed-rolled-back" ] && ok "输出 install-failed-rolled-back" || no "输出应为回滚，实际「$OUT」"
[ "$(sum_of "$MOD/bin/9router-go")" = "$BEFORE_SUM" ] && ok "二进制已回滚到启动前那一份" || no "二进制未回滚"
[ "$(sum_of "$MOD/bin/9router-go")" != "$BAD_SUM" ] && ok "失败的那份没有被留下" || no "失败的新引擎还在"
[ "$(cat "$DATA/engine-version" 2>/dev/null)" = "$BEFORE_VER" ] \
  && ok "版本保持旧值（绝不谎报 9.9.9）" || no "版本被谎报成 9.9.9"
[ ! -f "$DATA/engine.prev" ] && ok "回滚点已清理" || no ".prev 残留"

echo "== S3 门禁：不合格/不存在的源绝不碰现有二进制 =="
GOOD_SUM="$(sum_of "$MOD/bin/9router-go")"
SMALL="$TMP/small"
printf '\177ELF' > "$SMALL"; chmod 0755 "$SMALL"
OUT="$(cmd_install_engine "$SMALL" 1.0.0)"
[ "$OUT" = "install-rejected-src" ] && ok "太小 → install-rejected-src" || no "太小未被拦，实际「$OUT」"
NOTELF="$TMP/notelf"
dd if=/dev/zero of="$NOTELF" bs=1 count=0 seek=6291456 2>/dev/null; chmod 0755 "$NOTELF"
OUT="$(cmd_install_engine "$NOTELF" 1.0.0)"
[ "$OUT" = "install-rejected-src" ] && ok "非 ELF → install-rejected-src" || no "非 ELF 未被拦，实际「$OUT」"
OUT="$(cmd_install_engine "$TMP/nope" 1.0.0)"
[ "$OUT" = "no-src" ] && ok "源不存在 → no-src" || no "缺源未被拦，实际「$OUT」"
[ "$(sum_of "$MOD/bin/9router-go")" = "$GOOD_SUM" ] && ok "三次拒绝后二进制一字未动" || no "被拒绝的源却改了二进制"

echo "== S4 当前二进制本身不合格时，不得把它当回滚点 =="
printf 'garbage' > "$MOD/bin/9router-go"      # 故意破坏"当前"
NEW2="$TMP/engine-new2"
mk_engine "$NEW2"
NEW2_SUM="$(sum_of "$NEW2")"
FAKE_RESTART=engine=down                       # 起不来 → 应走 install-failed（没有 .prev 可用）
OUT="$(cmd_install_engine "$NEW2" 2.0.0)"
[ "$OUT" = "install-failed" ] && ok "无合格回滚点 → install-failed" || no "输出异常：「$OUT」"
[ "$(sum_of "$MOD/bin/9router-go")" = "$NEW2_SUM" ] && ok "没有把垃圾 cp 成回滚点（也就没回滚成垃圾）" || no "回滚点用了不合格文件"
[ ! -f "$DATA/engine.prev" ] && ok "未留下 .prev" || no ".prev 残留"

echo "== S5 装包成功路径：换 module.prop、包内引擎版本落盘 =="
STAGE="$TMP/pkg"
mkdir -p "$STAGE/lib" "$STAGE/bin" "$STAGE/etc"
printf 'id=ninerouter-go\nname=x\nversion=v9.9.9-test\nversionCode=999999\n' > "$STAGE/module.prop"
printf '# lib\n' > "$STAGE/lib/ops.sh"
mk_engine "$STAGE/bin/9router-go"
printf '%s\n' "7.7.7" > "$STAGE/etc/engine-version"
(cd "$STAGE" && zip -q -r "$TMP/pkg.zip" module.prop lib bin etc) || no "造 zip 失败"
FAKE_RESTART=engine=up
OUT="$(cmd_install_module "$TMP/pkg.zip")"
[ "$OUT" = "engine=up" ] && ok "输出 engine=up" || no "输出应为 engine=up，实际「$OUT」"
grep -q '^version=v9.9.9-test$' "$MOD/module.prop" && ok "module.prop 已换成包内版本" || no "module.prop 未更新"
[ "$(cat "$DATA/engine-version" 2>/dev/null)" = "7.7.7" ] \
  && ok "包内引擎版本已落盘（engine_version_sync --from-package）" || no "包内引擎版本未落盘"
[ -s "$DATA/last-module.zip" ] && ok "留档 last-module.zip" || no "未留档"

echo "== 结果：通过 $PASS / 失败 $FAIL =="
[ "$FAIL" -eq 0 ] || exit 1
exit 0
