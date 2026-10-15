#!/bin/sh
# tools/check-dns-defaults.sh — DNS 内置默认集的单一事实源闸门（2026-10-15 架构评审 C3）。
#
# 背景：内置默认上游集（223.5.5.5 + 119.29.29.29）曾有四份互不相认的拷贝——
# lifecycle.sh 的 life_prep 兜底、life_dns_recover 候选腿（同一文件内两份字面量，
# 注释自称"同源"却无缝合）、page-dns.js 候选池的腾讯腿、dnsfwd.c 的自举兜底。
# 现在唯一来源是 module/etc/dns-defaults；本闸门把三处仍需保留字面量的地方缝住：
#   D1 默认源文件本身：≥1 条明文 nameserver、无环回、配置行全部合法
#   D2 lifecycle.sh 的 fallback 字面量集合 == 文件集合（默认源缺失时的保底不漂移）
#   D3 dnsfwd.c 的 boot_resolve 自举字面量 ∈ 文件集合（C 侧最后防线不漂移）
#   D4 dnsfwd.c 的默认配置路径带 -go（B10：曾经写成 /data/adb/9router/，潜伏 bug）
#
# 为什么打在"源"上而不是只靠 L23：L23 验的是 life_prep 的生成物（消费末端）；这里验
# 来源本身与各消费者的字面量——源头写错或消费者偷偷改字面量，在这里就红，而不是等
# 真机首启解析全挂。"无境外公共解析器"由 L23 的行为断言负责（清单只保留一份）。
#
# 用法：在仓库根执行 `sh tools/check-dns-defaults.sh`（已接进 tools/check.sh --offline）
set -u
cd "$(dirname "$0")/.." || exit 1

DEF=module/etc/dns-defaults
LIFE=module/lib/lifecycle.sh
DNSFWD=tools/dnsfwd.c
for _f in "$DEF" "$LIFE" "$DNSFWD"; do
  [ -f "$_f" ] || { echo "❌ 找不到 $_f" >&2; exit 1; }
done

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); printf '  ✅ %s\n' "$*"; }
no() { FAIL=$((FAIL + 1)); printf '  ❌ %s\n' "$*"; }

# 文件里的 nameserver 地址集合（配置行，注释行除外）
ns_file() {
  grep -E '^[[:space:]]*nameserver[[:space:]]+[0-9]' "$DEF" 2>/dev/null \
    | awk '{print $2}' | sort -u
}

echo "== D1 默认源文件本身 =="
_ns_n="$(grep -cE '^[[:space:]]*nameserver[[:space:]]+[0-9]' "$DEF" 2>/dev/null | tr -d ' ')"
[ "${_ns_n:-0}" -ge 1 ] && ok "D1a 至少一条明文 nameserver（$_ns_n 条）" \
  || no "D1a 默认源没有明文上游——首启即解析失败"
_bad=""
for _ip in $(ns_file); do
  case "$_ip" in 127.*|::1|0.0.0.0) _bad="$_bad $_ip" ;; esac
done
[ -z "$_bad" ] && ok "D1b 无环回/未指定地址" || no "D1b 混入环回地址：$_bad"
# 配置行形状：非注释非空行必须 nameserver/doh/dot 开头（dnsfwd 只认这三种前缀）
_shape="$(grep -Ev '^[[:space:]]*(#|$)' "$DEF" 2>/dev/null \
  | grep -Ev '^[[:space:]]*(nameserver|doh|dot)[[:space:]]' | tr '\n' ' ')"
[ -z "$_shape" ] && ok "D1c 配置行全部合法（nameserver/doh/dot）" \
  || no "D1c 非法配置行：$_shape"

echo "== D2 lifecycle.sh 兜底字面量 ↔ 默认源（fallback 不漂移）=="
# life_dns_default_lines 的 printf 兜底块（默认源缺失时的最小集）
_life_ns() {
  awk '/^life_dns_default_lines\(\)/,/^}/' "$LIFE" 2>/dev/null \
    | grep -oE "nameserver [0-9][0-9.]*" | awk '{print $2}' | sort -u
}
_fl="$(ns_file | tr '\n' ' ' | sed 's/ *$//')"
_ll="$(_life_ns | tr '\n' ' ' | sed 's/ *$//')"
[ -n "$_ll" ] && [ "$_fl" = "$_ll" ] && ok "D2 fallback 与默认源一致（$_ll）" \
  || no "D2 fallback（$_ll）与默认源（$_fl）漂移"

echo "== D3 dnsfwd.c 自举字面量 ∈ 默认源 =="
_lits="$(grep -oE 'parse_upstream\("[0-9][0-9.]*"' "$DNSFWD" 2>/dev/null \
  | grep -oE '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | sort -u)"
_miss=""
for _ip in $_lits; do
  ns_file | grep -qx "$_ip" || _miss="$_miss $_ip"
done
[ -n "$_lits" ] && [ -z "$_miss" ] && ok "D3 dnsfwd.c 自举字面量都在默认源里（$_lits）" \
  || no "D3 dnsfwd.c 字面量不在默认源：${_miss:-（未找到任何字面量）}"

echo "== D4 dnsfwd.c 默认配置路径（B10：曾经少 -go）=="
_wrong="$(grep -c '/data/adb/9router/dns-upstreams\.conf' "$DNSFWD" 2>/dev/null | tr -d ' ')"
_ok_path="$(grep -c '/data/adb/9router-go/dns-upstreams\.conf' "$DNSFWD" 2>/dev/null | tr -d ' ')"
[ "${_wrong:-0}" = 0 ] && [ "${_ok_path:-0}" -ge 1 ] \
  && ok "D4 默认路径带 -go（旧路径 0 处，新路径 $_ok_path 处）" \
  || no "D4 dnsfwd.c 仍有少 -go 的旧默认路径（${_wrong} 处）"

echo
echo "结果：通过 $PASS / 失败 $FAIL"
[ "$FAIL" = 0 ]
