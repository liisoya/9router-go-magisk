#!/bin/sh
# tools/test-update-json.sh — 断言 update.json 生成器与一致性闸门（2026-10-15 架构评审 C1）。
#
# 为什么有它：update.json 是设备端应用内更新的清单（page-update.js 按 versionCode
# 比较），曾与 module.prop 漂移（清单 v1.9.7-r2/109071 vs 模块 v1.9.13-r1/109130）——
# 低于 109130 的设备永远看不到更新，而没有任何门禁拦这类漂移。本测试钉住两件事：
#   ① 生成器从 module.prop 派生出的四个字段逐字正确（含 fork 的 --repo 覆盖）；
#   ② --check 对每一种漂移形状（versionCode / version / 坏 JSON / 缺文件）都红。
# 最后一条 L1 是**活体断言**：仓库自身的 update.json 必须与 module.prop 一致——
# 这与 tools/check.sh 的 UPDATEJSON 闸门同源，发布准备忘跑 --write 时这里先红。
#
# 用法：在仓库根执行 `sh tools/test-update-json.sh`
set -u
GEN="./tools/gen-update-json.py"
if [ ! -f "$GEN" ]; then
  echo "找不到 $GEN（请在仓库根执行）" >&2
  exit 1
fi

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); printf '  ✅ %s\n' "$*"; }
no() { FAIL=$((FAIL + 1)); printf '  ❌ %s\n' "$*"; }

TMPD="$(mktemp -d 2>/dev/null || echo /tmp/updjson.$$)"
mkdir -p "$TMPD"
cleanup() { rm -rf "$TMPD"; }
trap cleanup EXIT

# 假 module.prop：版本/版本号与真实发布形状同构（v 前缀 + r 修订）
cat > "$TMPD/module.prop" <<'EOF'
id=ninerouter-go
name=9router-go
version=v9.9.9-r3
versionCode=999903
updateJson=https://example.invalid/update.json
EOF

echo "== W 生成器（从 module.prop 派生四个字段）=="
python3 "$GEN" --write --prop "$TMPD/module.prop" --out "$TMPD/update.json" >/dev/null 2>&1
if [ $? = 0 ] && [ -f "$TMPD/update.json" ]; then
  ok "W1 --write 生成文件"
else
  no "W1 --write 生成文件"
fi

_ver="$(sed -n 's/.*"version": "\(.*\)".*/\1/p' "$TMPD/update.json" | head -1)"
_code="$(sed -n 's/.*"versionCode": \([0-9]*\).*/\1/p' "$TMPD/update.json" | head -1)"
_zip="$(sed -n 's/.*"zipUrl": "\(.*\)".*/\1/p' "$TMPD/update.json" | head -1)"
_chg="$(sed -n 's/.*"changelog": "\(.*\)".*/\1/p' "$TMPD/update.json" | head -1)"
[ "$_ver" = "v9.9.9-r3" ] && ok "W2 version = v9.9.9-r3" || no "W2 version（得到 $_ver）"
[ "$_code" = "999903" ] && ok "W3 versionCode = 999903" || no "W3 versionCode（得到 $_code）"
[ "$_zip" = "https://github.com/liisoya/9router-go-magisk/releases/download/v9.9.9-r3/9router-go-9.9.9-r3-magisk.zip" ] \
  && ok "W4 zipUrl 形状（tag 直链 + 去 v 文件名）" || no "W4 zipUrl（得到 $_zip）"
[ "$_chg" = "https://github.com/liisoya/9router-go-magisk/releases/download/v9.9.9-r3/changelog.md" ] \
  && ok "W5 changelog 是 tag 直链（非 latest/download）" || no "W5 changelog（得到 $_chg）"

# fork 覆盖：release.yml 传 github.repository，URL 必须跟着变
python3 "$GEN" --write --prop "$TMPD/module.prop" --out "$TMPD/update-fork.json" --repo "someone/9router-go-magisk" >/dev/null 2>&1
if grep -q "https://github.com/someone/9router-go-magisk/releases/download/" "$TMPD/update-fork.json" 2>/dev/null; then
  ok "W6 --repo 覆盖产生 fork URL"
else
  no "W6 --repo 覆盖产生 fork URL"
fi

# 幂等：CI 调用后必须零 diff（release.yml 的提交步骤依赖这一点）
cp "$TMPD/update.json" "$TMPD/update.first"
python3 "$GEN" --write --prop "$TMPD/module.prop" --out "$TMPD/update.json" >/dev/null 2>&1
if cmp -s "$TMPD/update.first" "$TMPD/update.json"; then
  ok "W7 --write 幂等（两次输出逐字节一致）"
else
  no "W7 --write 幂等"
fi

echo "== C 一致性闸门（--check 对每种漂移形状红）=="
python3 "$GEN" --check --prop "$TMPD/module.prop" --out "$TMPD/update.json" >/dev/null 2>&1
[ $? = 0 ] && ok "C1 一致时 --check 退出 0" || no "C1 一致时 --check 退出 0"

sed 's/"versionCode": 999903/"versionCode": 999902/' "$TMPD/update.json" > "$TMPD/drift-code.json"
_err="$(python3 "$GEN" --check --prop "$TMPD/module.prop" --out "$TMPD/drift-code.json" 2>&1)"
[ $? != 0 ] && ok "C2 versionCode 漂移 → 红" || no "C2 versionCode 漂移 → 红"
printf '%s' "$_err" | grep -q "versionCode" && ok "C3 漂移信息点名 versionCode" || no "C3 漂移信息点名 versionCode（得到 $_err）"

sed 's/"version": "v9.9.9-r3"/"version": "v9.9.9-r2"/' "$TMPD/update.json" > "$TMPD/drift-ver.json"
python3 "$GEN" --check --prop "$TMPD/module.prop" --out "$TMPD/drift-ver.json" >/dev/null 2>&1
[ $? != 0 ] && ok "C4 version 漂移 → 红" || no "C4 version 漂移 → 红"

printf '{ not json' > "$TMPD/bad.json"
python3 "$GEN" --check --prop "$TMPD/module.prop" --out "$TMPD/bad.json" >/dev/null 2>&1
[ $? != 0 ] && ok "C5 坏 JSON → 红" || no "C5 坏 JSON → 红"

python3 "$GEN" --check --prop "$TMPD/module.prop" --out "$TMPD/nonexistent.json" >/dev/null 2>&1
[ $? != 0 ] && ok "C6 缺文件 → 红" || no "C6 缺文件 → 红"

echo "== L 活体（仓库自身必须一致）=="
python3 "$GEN" --check >/dev/null 2>&1
if [ $? = 0 ]; then
  ok "L1 仓库 update.json 与 module.prop 一致（发布准备状态）"
else
  no "L1 仓库 update.json 与 module.prop 不一致——运行 python3 tools/gen-update-json.py --write"
fi

echo
echo "结果：通过 $PASS / 失败 $FAIL"
[ "$FAIL" = 0 ]
