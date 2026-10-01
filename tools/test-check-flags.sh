#!/bin/sh
# tools/test-check-flags.sh — 断言门禁入口 tools/check.sh 的**档位选择与严格模式**真的生效。
#
# 为什么单独有它（2026-09-29 架构走查 A1）：`--require-device` / `--require-parity` 曾经只设
# REQ_* 而**不选档位**，于是 PICKED 为 0 → 退回离线档 → 真机/对照档整块不执行，而 REQ_* 只在
# 被跳过的块里被读 → 「严格模式」空转：报成功却一条 T*/A*/parity 都没跑。
# 这是**最高级别的假绿**（门禁自己不可信，其他断言全白搭），所以「门禁入口」本身也要有门禁盯着。
#
# 用法：在仓库根执行 `sh tools/test-check-flags.sh`
set -u
CHK="./tools/check.sh"
if [ ! -f "$CHK" ]; then
  echo "找不到 $CHK（请在仓库根执行）" >&2
  exit 1
fi
# 用 bash 调用而不是 ./check.sh：本脚本可能被 checkout 成无可执行位，
# 而"跑不起来"会让断言以非 0 退出，**恰好被误读成"门禁真的拦住了"**（本轮真的踩了一次假绿）。
chk() { bash "$CHK" "$@"; }

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); printf '  ✅ %s\n' "$*"; }
no() { FAIL=$((FAIL + 1)); printf '  ❌ %s\n' "$*"; }

# 前置：入口必须真的能跑起来并给出可解析输出，否则后面全是"空 vs 期望值"的误判
PROBE="$(chk --print-tiers 2>&1 | sed -n 's/^tiers=//p')"
if [ -z "$PROBE" ]; then
  echo "❌ 前置失败：check.sh --print-tiers 没有输出，无法判定（先看 bash tools/check.sh --help）" >&2
  chk --print-tiers 2>&1 | head -3 >&2
  exit 1
fi

# 只读 --print-tiers 的输出：不执行任何档位，因此本测试自身不会递归调用 check.sh
tiers() { chk "$@" --print-tiers 2>/dev/null | sed -n 's/^tiers=//p'; }
flags() { chk "$@" --print-tiers 2>/dev/null | sed -n 's/^require-device=\([01]\) require-parity=\([01]\)$/\1\2/p'; }

echo "== C1 档位选择：无参数 / 单档 / 全档 =="
[ "$(tiers)" = "offline" ] && ok "C1 无参数 → offline（默认档）" || no "C1 无参数得到「$(tiers)」，期望 offline"
[ "$(tiers --device)" = "device" ] && ok "C1b --device → 只跑真机档" || no "C1b --device 得到「$(tiers --device)」"
[ "$(tiers --parity)" = "parity" ] && ok "C1c --parity → 只跑对照档" || no "C1c --parity 得到「$(tiers --parity)」"
[ "$(tiers --all)" = "offline,device,parity" ] && ok "C1d --all → 三档全跑" || no "C1d --all 得到「$(tiers --all)」"

echo "== C2 严格模式必须**同时选档**（A1 的红灯项）=="
[ "$(tiers --require-device)" = "device" ] && ok "C2 --require-device → 真的选中真机档" \
  || no "C2 --require-device 得到「$(tiers --require-device)」—— 严格模式空转（假绿）"
[ "$(flags --require-device)" = "10" ] && ok "C2b --require-device 同时置严格位" || no "C2b 严格位没置：$(flags --require-device)"
[ "$(tiers --require-parity)" = "parity" ] && ok "C2c --require-parity → 真的选中对照档" \
  || no "C2c --require-parity 得到「$(tiers --require-parity)」—— 严格模式空转（假绿）"
[ "$(flags --require-parity)" = "01" ] && ok "C2d --require-parity 同时置严格位" || no "C2d 严格位没置：$(flags --require-parity)"

echo "== C3 严格位真的会拦：用「没有设备」的对照（PATH 前置一个什么都不做的 adb）=="
# 对照的两半必须同时成立，否则等于没测：非严格 → 按约定 SKIP 并退出 0；严格 → 必须退出非 0。
# 同时要求：**退出码之外还要看输出**（只比退出码会重蹈"跑不起来=拦住了"的假绿）。
TMPD="$(mktemp -d 2>/dev/null || echo /tmp/cflag.$$)"
mkdir -p "$TMPD/bin"
printf '#!/bin/sh\nexit 0\n' > "$TMPD/bin/adb"
chmod 755 "$TMPD/bin/adb"
PATH="$TMPD/bin:$PATH" chk --device >"$TMPD/a.log" 2>&1
A=$?
PATH="$TMPD/bin:$PATH" chk --require-device >"$TMPD/b.log" 2>&1
B=$?
if [ "$A" = 0 ] && grep -q "⏭\|SKIP" "$TMPD/a.log"; then
  ok "C3 非严格 + 无设备 → 按约定 SKIP 且退出 0"
else
  no "C3 非严格档退出码 $A（期望 0 且含 SKIP）：$(tail -2 "$TMPD/a.log")"
fi
if [ "$B" != 0 ] && grep -q "没有可用设备" "$TMPD/b.log"; then
  ok "C3b 严格 + 无设备 → 退出非 0 且给出「没有可用设备」（缺前置真的被拦住）"
elif [ "$B" != 0 ]; then
  no "C3b 退出非 0 但不是因为缺前置（可能是脚本本身跑不起来）：$(tail -2 "$TMPD/b.log")"
else
  no "C3b 严格档却退出 0 —— 缺前置没被拦住（严格模式失效）"
fi
rm -rf "$TMPD"

echo "== 结果：通过 $PASS / 失败 $FAIL =="
[ "$FAIL" = 0 ]
