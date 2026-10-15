#!/bin/sh
# tools/test-ops-upstreams.sh — ops.sh 上游清单写入口（write-upstreams / restore-upstreams）
# 的**行为**自证（2026-10-15 架构评审 C4）。
#
# 为什么有它：WebUI 过去经 ksu.exec 直写 dns-upstreams.conf，"备份→原子写→热重载"的纪律
# 整个在 JS 里（page-dns.js 的 DNS_STORE），绕过 ops.sh（AGENT-CONVENTIONS §3 运维动作
# 唯一入口）——于是同一纪律在 JS 与 life_dns_recover 各有一份，adb/action 路径想改上游也
# 无门可入。现在改写只经 seam，本测试把 seam 的行为钉住：
#   · 备份优先：任一回滚点建不起来 → 拒绝落盘（用户配置不被置于无回滚点状态）
#   · .prev 每次刷新（先删再备——否则"回滚上一版"永远回到首版）、.initial 首次留档不覆盖
#   · 原子写盘（tmp + mv）、写后必热重载，成败**分开回报**（不得把写成功当成已生效）
#   · 空内容 / 环回地址（127.0.0.1、::1）拒绝
#   · 恢复（initial|prev）+ 热重载；没有该回滚点 → restore-none
#
# 做法同 tools/test-install-flow.sh：OPS_LIB_ONLY=1 只加载定义，MODDIR/DATA_DIR 指临时
# 目录，在真实文件系统上重放；进程层（life_reload_dns）按用例覆盖。
#
# 运行：sh tools/test-ops-upstreams.sh（已接进 tools/check.sh --offline）
set -u
cd "$(dirname "$0")/.." || exit 1

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); printf '  ✅ %s\n' "$*"; }
no() { FAIL=$((FAIL + 1)); printf '  ❌ %s\n' "$*"; }

TMPD="$(mktemp -d 2>/dev/null || echo /tmp/opsup.$$)"
trap 'rm -rf "$TMPD"' EXIT
mkdir -p "$TMPD/mod/lib" "$TMPD/mod/etc" "$TMPD/data"
for _f in lifecycle.sh ops.sh log.sh wait.sh; do
  cp "module/lib/$_f" "$TMPD/mod/lib/$_f" || { echo "❌ 复制 $_f 失败"; exit 2; }
done
cp module/etc/dns-defaults "$TMPD/mod/etc/" 2>/dev/null || true

MODDIR="$TMPD/mod"
DATA_DIR="$TMPD/data"
export MODDIR DATA_DIR
# shellcheck source=/dev/null
OPS_LIB_ONLY=1 . "$MODDIR/lib/ops.sh" || { echo "❌ 无法 source ops.sh"; exit 2; }

# 进程层按用例覆盖（reload 成败可注入）；真实实现只发 SIGHUP，测试里不碰真进程
life_reload_dns() { echo "${_STUB_RELOAD:-reloaded}"; }

# base64 编解码（stdin 通道，与 KB.writeUpstreams 的命令形状一致）
b64e() { printf '%s' "$1" | base64 | tr -d '\n'; }
UP="$LIFE_UPSTREAMS"

echo "== W 写入（备份→原子写→热重载，单一 seam）=="
printf 'nameserver 223.5.5.5\n' > "$UP"          # 预置一份"用户当前配置"
b64e 'nameserver 119.29.29.29' | cmd_write_upstreams > "$TMPD/v1" 2>/dev/null
_v="$(cat "$TMPD/v1")"
[ "$_v" = "write-ok-reloaded" ] && ok "W1 首写判据 write-ok-reloaded" || no "W1 判据不对：$_v"
grep -q '119.29.29.29' "$UP" 2>/dev/null && ok "W2 新内容落盘" || no "W2 新内容没落盘"
grep -q '223.5.5.5' "$UP.initial" 2>/dev/null && ok "W3 .initial 留档首写前的配置" || no "W3 没有 .initial 留档"
grep -q '223.5.5.5' "$UP.prev" 2>/dev/null && ok "W4 .prev 刷新为改写前的版本" || no "W4 .prev 没刷新"
[ ! -e "$UP.tmp" ] && ok "W5 无 .tmp 残留" || no "W5 留下 .tmp"

b64e 'nameserver 114.114.114.114' | cmd_write_upstreams > "$TMPD/v2" 2>/dev/null
[ "$(cat "$TMPD/v2")" = "write-ok-reloaded" ] && ok "W6 二写判据" || no "W6 二写判据：$(cat "$TMPD/v2")"
grep -q '119.29.29.29' "$UP.prev" 2>/dev/null && ok "W7 .prev 每次刷新（二写前那份）" || no "W7 .prev 没跟着刷新"
grep -q '223.5.5.5' "$UP.initial" 2>/dev/null && ok "W8 .initial 不被覆盖（仍是首写前）" || no "W8 .initial 被覆盖了"

# 空内容 / 环回：拒绝且不改写
_before="$(cat "$UP")"
b64e '   ' | cmd_write_upstreams > "$TMPD/v3" 2>/dev/null
[ "$(cat "$TMPD/v3")" = "write-failed-empty" ] && ok "W9 空内容 → write-failed-empty" || no "W9 空内容判据：$(cat "$TMPD/v3")"
b64e 'nameserver 127.0.0.1' | cmd_write_upstreams > "$TMPD/v4" 2>/dev/null
[ "$(cat "$TMPD/v4")" = "write-failed-loopback" ] && ok "W10 环回 → write-failed-loopback" || no "W10 环回判据：$(cat "$TMPD/v4")"
[ "$_before" = "$(cat "$UP")" ] && ok "W11 拒绝时原配置一字未动" || no "W11 拒绝时配置被改了"

# 备份失败 → 拒绝落盘（用户配置不被置于无回滚点状态）
life_upstreams_backup_prev() { return 1; }
b64e 'nameserver 8.8.8.8' | cmd_write_upstreams > "$TMPD/v5" 2>/dev/null
_v5="$(cat "$TMPD/v5")"
[ "$_v5" = "write-failed-no-backup" ] && ok "W12 备份失败 → write-failed-no-backup" || no "W12 备份失败判据：$_v5"
grep -q '8.8.8.8' "$UP" 2>/dev/null && no "W13 备份失败却落盘了（用户将失去回滚点）" || ok "W13 备份失败未落盘"
life_upstreams_backup_prev() {          # 还原真实实现（下一段 reload 失败用例要用）
  [ -f "$LIFE_UPSTREAMS" ] || return 0
  rm -f "$LIFE_UPSTREAMS.prev" 2>/dev/null
  cp "$LIFE_UPSTREAMS" "$LIFE_UPSTREAMS.prev" 2>/dev/null
}

# 热重载失败：配置**已落盘**，判据分开回报（调用方不得把写成功当成已生效）
_STUB_RELOAD=fail
b64e 'nameserver 223.6.6.6' | cmd_write_upstreams > "$TMPD/v6" 2>/dev/null
[ "$(cat "$TMPD/v6")" = "write-ok-reload-failed" ] && ok "W14 热重载失败 → write-ok-reload-failed（分开回报）" || no "W14 判据：$(cat "$TMPD/v6")"
grep -q '223.6.6.6' "$UP" 2>/dev/null && ok "W15 热重载失败但配置已落盘" || no "W15 配置没落盘"
_STUB_RELOAD=reloaded

echo "== R 恢复（回滚点 + 热重载）=="
cmd_restore_upstreams prev > "$TMPD/r1" 2>/dev/null
[ "$(cat "$TMPD/r1")" = "restore-ok-reloaded" ] && ok "R1 restore prev 判据" || no "R1 判据：$(cat "$TMPD/r1")"
# .prev 此刻是 W14 那笔写入前的版本（114.114.114.114）——"回滚上一版"回到的正是它
grep -q '114.114.114.114' "$UP" 2>/dev/null && ok "R2 恢复后 = .prev 内容" || no "R2 内容不对：$(cat "$UP")"
cmd_restore_upstreams initial > "$TMPD/r2" 2>/dev/null
[ "$(cat "$TMPD/r2")" = "restore-ok-reloaded" ] && ok "R3 restore initial 判据" || no "R3 判据：$(cat "$TMPD/r2")"
grep -q '223.5.5.5' "$UP" 2>/dev/null && ok "R4 恢复后 = .initial 内容" || no "R4 内容不对"
cmd_restore_upstreams bogus > "$TMPD/r3" 2>/dev/null
[ "$(cat "$TMPD/r3")" = "restore-none" ] && ok "R5 非法回滚点名 → restore-none" || no "R5 判据：$(cat "$TMPD/r3")"
rm -f "$UP.prev"
cmd_restore_upstreams prev > "$TMPD/r4" 2>/dev/null
[ "$(cat "$TMPD/r4")" = "restore-none" ] && ok "R6 没有 .prev → restore-none" || no "R6 判据：$(cat "$TMPD/r4")"
_STUB_RELOAD=fail
cmd_restore_upstreams initial > "$TMPD/r5" 2>/dev/null
[ "$(cat "$TMPD/r5")" = "restore-ok-reload-failed" ] && ok "R7 恢复的热重载失败也分开回报" || no "R7 判据：$(cat "$TMPD/r5")"

echo
echo "结果：通过 $PASS / 失败 $FAIL"
[ "$FAIL" = 0 ]
