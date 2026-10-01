#!/bin/sh
# tools/test-install-gate.sh — 断言"装包的门禁先于动作"（2026-09-29 诊断 I1）。
#
# 为什么有它：`cmd_install_module` 是模块**唯一安装入口**，而它过去是「先 stop_all 再 unzip」——
# zip 坏 / 非 zip / unzip 缺失时只能 echo install-failed 走人，引擎与 dnsfwd **已经被停掉**；
# 守护若未武装，服务不会自己回来。同文件的 `cmd_install_engine` 一直是"不合格源绝不碰现有二进制"。
#
# 分工（2026-09-30 架构扫描 C5 重整）：
#   · **本文件**只跑否定路径，但断言是**行为**的 —— 用真实 lifecycle + 临时 DATA_DIR，
#     坏包若真的走到 stop_all，那个"活着的引擎" pidfile 就会被删掉（I1b/I2b 就是在看它）。
#   · **install 的成功与回滚全路径**在 `tools/test-install-flow.sh`（INSTALLFLOW）里跑：
#     那边把 MODDIR 指到临时目录，所以能真装、真回滚。
#   · 这里曾有一条 I4「用 grep 行号比较门禁行与 stop_all 行」—— **已删**：顺序判断用文本形状
#     （行号）是脆的（格式化/重构即可打穿），而它的前提"成功路径不能跑"也已被 INSTALLFLOW 取代。
set -u
OPS="./module/lib/ops.sh"
if [ ! -f "$OPS" ]; then
  echo "找不到 $OPS（请在仓库根执行）" >&2
  exit 1
fi

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); printf '  ✅ %s\n' "$*"; }
no() { FAIL=$((FAIL + 1)); printf '  ❌ %s\n' "$*"; }

TMPD="$(mktemp -d 2>/dev/null || echo /tmp/ig.$$)"
trap 'rm -rf "$TMPD"' EXIT
# 假数据目录 + 一个"活着的引擎" pidfile：若门禁失效、真的走到了 stop_all，pidfile 会被删掉
sleep 60 &
FAKE=$!
printf '%s\n' "$FAKE" > "$TMPD/9router.pid"
printf 'i am not a zip\n' > "$TMPD/bad.zip"
printf 'PK\x03\x04 garbage but named zip\n' > "$TMPD/truncated.zip"

echo "== I 坏包必须在「动服务」之前被挡下 =="
OUT="$(DATA_DIR="$TMPD" sh "$OPS" install-module "$TMPD/bad.zip" 2>/dev/null)"
[ "$OUT" = "install-failed" ] && ok "I1 非 zip → 如实回 install-failed" || no "I1 输出不对：「$OUT」"
[ -f "$TMPD/9router.pid" ] && ok "I1b 服务**没有被停**（pidfile 还在）" \
  || no "I1b 门禁失效：坏包也停了服务（pidfile 已被删）"
[ "$(cat "$TMPD/9router.pid" 2>/dev/null)" = "$FAKE" ] && ok "I1c 引擎 pid 未被改动" \
  || no "I1c pidfile 内容被改（引擎被动过）"
OUT2="$(DATA_DIR="$TMPD" sh "$OPS" install-module "$TMPD/truncated.zip" 2>/dev/null)"
[ "$OUT2" = "install-failed" ] && ok "I2 截断的 zip → 同样挡住" || no "I2 输出不对：「$OUT2」"
[ -f "$TMPD/9router.pid" ] && ok "I2b 服务仍然没有被停" || no "I2b 截断包也停了服务"
OUT3="$(DATA_DIR="$TMPD" sh "$OPS" install-module "$TMPD/nope.zip" 2>/dev/null)"
[ "$OUT3" = "no-src" ] && ok "I3 文件不存在 → no-src（与坏包区分）" || no "I3 输出不对：「$OUT3」"

# I4「grep 行号比顺序」已删除（2026-09-30 C5）：顺序判断改用行为 —— 坏包是否真的停掉服务
# 由上面的 I1b/I2b 直接看 pidfile，成功/回滚全路径由 tools/test-install-flow.sh 覆盖。

kill -9 "$FAKE" 2>/dev/null
wait "$FAKE" 2>/dev/null
echo "== 结果：通过 $PASS / 失败 $FAIL =="
[ "$FAIL" = 0 ]
