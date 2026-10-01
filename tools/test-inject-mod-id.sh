#!/bin/sh
# tools/test-inject-mod-id.sh — 注入器的离线自证（不需要设备、不需要网络）
#
# 为什么值得一条独立测试：注入器是**两条发布/迭代路径的共同前置**（打包与直推），
# 它出错的表现是"上线后才在设备上看到占位符"（真机事故：CFG.MODDIR 错误、面板获取不到信息）。
# 本测试把 5 条判据固化：全树注入 / MOD_ID 覆盖 / 幂等 / 缺 id 拒绝 / 不可读拒绝。
#
# 运行：sh tools/test-inject-mod-id.sh（已接进 tools/check.sh --offline）
set -u
cd "$(dirname "$0")/.." || exit 1
INJ="tools/inject-mod-id.sh"
PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); echo "  ✅ $*"; }
no() { FAIL=$((FAIL + 1)); echo "  ❌ $*"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

fresh() {
  # 造一个假源树 + 目标树；目标树里有一个**清单之外**的新文件也带占位符 ——
  # 旧实现（硬编码 2 个文件）会漏掉它，这正是本测试要钉住的行为
  SRC="$TMP/src"
  DST="$TMP/dst"
  rm -rf "$SRC" "$DST"
  mkdir -p "$SRC/webroot" "$SRC/lib" "$DST/webroot" "$DST/lib"
  printf 'id=fake-mod\nversion=v9.9.9-r9\n' > "$SRC/module.prop"
  printf '<meta name="x" content="__MOD_ID__">\n' > "$DST/webroot/index.html"
  printf 'const P = "/data/adb/modules/__MOD_ID__";\n' > "$DST/webroot/app.js"
  printf '// new file __MOD_ID__\n' > "$DST/webroot/parsers.js"     # ← 清单之外的新文件
  printf '#!/bin/sh\necho __MOD_ID__\n' > "$DST/lib/ops.sh"
}

echo "== T1 全树注入（含清单之外的新文件）=="
fresh
if sh "$INJ" "$SRC" "$DST" > "$TMP/out1" 2>&1; then
  if grep -rq -- '__MOD_ID__' "$DST"; then
    no "T1 仍有残留"
  else
    ok "T1 0 残留"
  fi
  grep -q 'parsers.js' "$TMP/out1" && ok "T1b 清单外的新文件也被注入并列出" || no "T1b 新文件没被注入"
  grep -q 'fake-mod' "$DST/webroot/parsers.js" && ok "T1c MOD_ID 来自 module.prop" || no "T1c 注入内容不对"
else
  no "T1 注入器非零退出：$(cat "$TMP/out1")"
fi

echo "== T2 MOD_ID 环境变量覆盖 =="
fresh
if MOD_ID=override-mod sh "$INJ" "$SRC" "$DST" >/dev/null 2>&1 && grep -q 'override-mod' "$DST/webroot/index.html"; then
  ok "T2 覆盖生效"
else
  no "T2 覆盖没生效"
fi

echo "== T3 幂等（无占位符时再跑一次仍成功）=="
fresh
sh "$INJ" "$SRC" "$DST" >/dev/null 2>&1
if sh "$INJ" "$SRC" "$DST" > "$TMP/out3" 2>&1; then
  grep -q '0 残留' "$TMP/out3" && ok "T3 第二次运行：0 个文件被注入、0 残留" || no "T3 输出异常"
else
  no "T3 第二次运行失败"
fi

echo "== T4 module.prop 缺 id → 拒绝（不是把它注入成空串）=="
rm -rf "$TMP/src4" "$TMP/dst4"
mkdir -p "$TMP/src4" "$TMP/dst4"
printf 'version=v1\n' > "$TMP/src4/module.prop"
printf '__MOD_ID__\n' > "$TMP/dst4/x.js"
if sh "$INJ" "$TMP/src4" "$TMP/dst4" > "$TMP/out4" 2>&1; then
  no "T4 缺 id 却成功了"
else
  grep -q '没有 id=' "$TMP/out4" && ok "T4 拒绝并给出原因" || no "T4 报错信息不清：$(cat "$TMP/out4")"
  grep -q '__MOD_ID__' "$TMP/dst4/x.js" && ok "T4b 目标树未被改动（未注入空串）" || no "T4b 目标被污染"
fi

echo "== T5 不可读文件 → 拒绝（读不到就无法证明零残留）=="
fresh
printf '__MOD_ID__\n' > "$DST/webroot/hidden.js"
chmod 000 "$DST/webroot/hidden.js"
if sh "$INJ" "$SRC" "$DST" > "$TMP/out5" 2>&1; then
  no "T5 有不可读文件却成功了（零残留无法保证）"
else
  grep -q '不可读' "$TMP/out5" && ok "T5 拒绝并列出文件" || no "T5 报错信息不清：$(cat "$TMP/out5")"
fi
chmod 644 "$DST/webroot/hidden.js" 2>/dev/null || true

echo "== 结果：通过 $PASS / 失败 $FAIL =="
[ "$FAIL" = 0 ] || exit 1
exit 0
