#!/bin/sh
# tools/check-dns-candidates.sh — DNS 优选候选池的单一事实源闸门（2026-10-22，ADR-0010 完整优选落地）。
#
# 背景：候选池（面板"测速优选"芯片 + 手动优选 + 守护自动优选共享的候选清单）曾硬编码在
# page-dns.js 的 DNS_CANDIDATES 数组里 —— shell 侧要做完整优选时没有可共享的第二份，
# ADR-0010 因此挂账"待候选池收敛为单一来源"。现在唯一来源是 module/etc/dns-candidates；
# 本闸门缝住两处仍需保留的形状：
#   P1 源文件本身：≥1 条配置行、全部带 cat=/label= 元数据、无环回、行首动词合法
#   P2 page-dns.js 的 JS 硬编码全量清单不复活（fallback 只许有静态最小集两条明文）
#   P3 dnsfwd.c 的 probe_score 注释与 TSV 表头含 score 列（评分公式唯一实现仍随 C 侧）
#
# 用法：sh tools/check-dns-candidates.sh（已接进 tools/check.sh --offline）
set -u
cd "$(dirname "$0")/.." || exit 1

DEF=module/etc/dns-candidates
PAGE=module/webroot/page-dns.js
DNSFWD=tools/dnsfwd.c
for _f in "$DEF" "$PAGE" "$DNSFWD"; do
  [ -f "$_f" ] || { echo "❌ 找不到 $_f" >&2; exit 1; }
done

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); printf '  ✅ %s\n' "$*"; }
no() { FAIL=$((FAIL + 1)); printf '  ❌ %s\n' "$*"; }

echo "== P1 候选池源文件本身 =="
_n="$(grep -cE '^[[:space:]]*cat=' "$DEF" 2>/dev/null | tr -d ' ')"
[ "${_n:-0}" -ge 8 ] && ok "P1a 候选条目充足（$_n 条）" || no "P1a 候选条目过少（$_n 条 < 8）"
_badmeta="$(grep -E '^[[:space:]]*cat=' "$DEF" | grep -vcE '^[[:space:]]*cat=(明文|doh|dot)[[:space:]]+label=\S+[[:space:]]+(nameserver|doh|dot)[[:space:]]' || true)"
[ "${_badmeta:-0}" = "0" ] && ok "P1b 全部行带合法元数据（cat/label + 动词）" \
  || no "P1b 有 $_badmeta 行缺元数据或动词非法"
_badloop="$(grep -E '^[[:space:]]*cat=' "$DEF" | grep -cE '127\.0\.0\.1|::1' || true)"
[ "${_badloop:-0}" = "0" ] && ok "P1c 无环回地址" || no "P1c 混入环回地址（自我循环）"
_nocat="$(grep -Ev '^[[:space:]]*(#|$)' "$DEF" | grep -cvE '^[[:space:]]*cat=' || true)"
[ "${_nocat:-0}" = "0" ] && ok "P1d 无元数据外的游离行" || no "P1d 有 $_nocat 行不带 cat= 前缀"

echo "== P2 JS 侧全量清单不复活 =="
_hardcode="$(grep -cE "v: '(nameserver|doh|dot) " "$PAGE" 2>/dev/null | tr -d ' ')"
[ "${_hardcode:-0}" -le 2 ] && ok "P2a JS 硬编码上游 ≤2（只许 fallback 最小集，$_hardcode 条）" \
  || no "P2a JS 里复活了全量清单（$_hardcode 条 > 2）——改候选请只改 module/etc/dns-candidates"
grep -q "loadDnsCandidates" "$PAGE" \
  && ok "P2b 面板运行时读单一来源文件" || no "P2b 面板没有消费单一来源（loadDnsCandidates 消失）"

echo "== P3 dnsfwd 评分公式（唯一实现）="
grep -q "probe_score" "$DNSFWD" \
  && ok "P3a probe_score 公式在 C 侧" || no "P3a probe_score 消失（评分公式被搬去 shell/JS？）"
grep -q 'tcp_ok/total\\ttcp_ip\\tscore' "$DNSFWD" \
  && ok "P3b TSV 表头带 score 列" || no "P3b TSV 表头丢了 score 列（shell/JS 消费方会全部失效）"

echo
echo "结果：通过 $PASS / 失败 $FAIL"
[ "$FAIL" = 0 ]
