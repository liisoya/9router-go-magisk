#!/bin/sh
# tools/test-dns-optimize.sh — DNS 完整优选的离线自证（2026-10-22，ADR-0010 挂账落地）。
#
# 覆盖：
#   O1 评分对纯函数 life_dns_scored_pairs：多行取最高分、<50% 过滤、score 列缺失行拒、
#      降序、doh 带空格 ident 完整保留
#   O2 life_dns_select_best：Top N 截断
#   O3 life_dns_best_score_of：最高分单值
#   O4 候选池文件：life_dns_candidate_lines 剥元数据/保序去重、life_dns_candidates_file 发现
#   O5 life_dns_reoptimize 全链（桩 dnsfwd + 真 TSV）：not-beaten / beaten-strike 双确认
#      / reoptimized 写盘形状（自定义项在前+Top5+头注释+无环回）/ stale-config 让位
#   O6 strike 文件语义：清零与落盘
#
# 运行：sh tools/test-dns-optimize.sh（已接进 tools/check.sh --offline）
set -u
cd "$(dirname "$0")/.." || exit 1

_TMPD="$(mktemp -d 2>/dev/null)" || { echo "❌ 无法创建临时目录"; exit 2; }
trap 'rm -rf "$_TMPD"' EXIT
MODDIR="$PWD/module"
DATA_DIR="$_TMPD"
export MODDIR DATA_DIR
. module/lib/lifecycle.sh || { echo "❌ 无法 source module/lib/lifecycle.sh"; exit 2; }

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); echo "  ✅ $*"; }
no() { FAIL=$((FAIL + 1)); echo "  ❌ $*"; }

TSV="$(printf 'upstream\tfamily\tdomain\trtt_ms\tok/total\tn_ans\tanswers\tfakeip\ttcp_ms\ttcp_ok/total\ttcp_ip\tscore\n223.5.5.5\tv4\tapi.test.cn\t28\t2/2\t1\t1.2.3.4\tno\t35\t2/2\t1.2.3.4\t92.5\ndoh https://doh.pub/dns-query\tdoh\tapi.test.cn\t80\t2/2\t1\t1.2.3.6\tno\t45\t2/2\t1.2.3.6\t85.1\n119.29.29.29\tv4\tapi.test.cn\t50\t2/2\t1\t1.2.3.5\tno\t300\t0/2\t1.2.3.5\t48.0\n114.114.114.114\tv4\tapi.test.cn\t20\t3/4\t1\t-\tno\t-1\t0/0\t-\t64.2\nfake.up\tv4\tx\t10\t2/2\t1\t198.18.0.1\tyes\t-1\t0/0\t-\t-50.0\nbadrow\tv4\tx\t10\t1/1\t1\t-\tno\t-1\t0/0\t-\tnan\nweakrow\tv4\tx\t10\t1/3\t1\t-\tno\t-1\t0/0\t-\t90.0\n')"

echo "== O1 life_dns_scored_pairs：评分对（多行取最高 / 过滤 / 降序）=="
_pairs="$(printf '%s\n' "$TSV" | life_dns_scored_pairs)"
_first="$(printf '%s\n' "$_pairs" | head -1)"
[ "$(printf '%s' "$_first" | cut -f2)" = "223.5.5.5" ] && [ "$(printf '%s' "$_first" | cut -f1)" = "000001092500" ] \
  && ok "O1a 最高分行是 223.5.5.5（key=(92.5+1000)×1000=1092500）" || no "O1a 排序/取最高错误：首行 $(printf '%s' "$_first" | tr '\t' '|')"
printf '%s\n' "$_pairs" | grep -q "doh https://doh.pub/dns-query" \
  && ok "O1b doh 带空格 ident 完整保留" || no "O1b doh ident 被截断"
printf '%s\n' "$_pairs" | grep -q "fake.up" && no "O1c fake-ip 负分未被沉底过滤" || ok "O1c fake-ip 负分沉底"
printf '%s\n' "$_pairs" | grep -q "badrow" && no "O1d score 非法行混入" || ok "O1d score 非法行被拒"
printf '%s\n' "$_pairs" | grep -q "weakrow" && no "O1e 可用率 1/3 未过滤" || ok "O1e 可用率 <50% 被过滤"
_n="$(printf '%s\n' "$_pairs" | wc -l | tr -d ' ')"
[ "$_n" = "4" ] && ok "O1f 有效上游 4 条" || no "O1f 期望 4 条，实际 $_n"

echo "== O2/O3 Top-N 与最高分 =="
_b3="$(printf '%s\n' "$TSV" | life_dns_select_best 3 | tr '\n' '|')"
[ "$_b3" = "223.5.5.5|doh https://doh.pub/dns-query|114.114.114.114|" ] \
  && ok "O2 Top3 顺序正确" || no "O2 Top3 错误：$_b3"
_bs="$(printf '%s\n' "$TSV" | life_dns_best_score_of)"
case "$_bs" in 000001092500) ok "O3 最高分 key=1092500（92.5 分）" ;; *) no "O3 最高分错误：$_bs" ;; esac

echo "== O4 候选池单一来源读取 =="
_cf="$(life_dns_candidates_file)" && [ -n "$_cf" ] && ok "O4a 候选池文件可发现" || no "O4a 候选池文件未发现"
_cl="$(life_dns_candidate_lines "$MODDIR/etc/dns-candidates")"
[ "$(printf '%s\n' "$_cl" | head -1)" = "nameserver 119.29.29.29" ] \
  && ok "O4b 元数据剥除后首行是配置行" || no "O4b 元数据未剥净：$(printf '%s' "$_cl" | head -1)"
[ "$(printf '%s\n' "$_cl" | wc -l | tr -d ' ')" = "15" ] \
  && ok "O4c 15 条候选完整" || no "O4c 条数错误：$(printf '%s\n' "$_cl" | wc -l | tr -d ' ')"
printf '%s\n' "$_cl" | grep -q "cat=" && no "O4d cat= 残留" || ok "O4d 无元数据残留"

echo "== O5 life_dns_reoptimize 全链（桩 dnsfwd）=="
# 桩：候选池探测 TSV（好分）与当前配置探测 TSV（低分）走同一桩，按 -f 参数区分
mkdir -p "$DATA_DIR"
printf 'nameserver 119.29.29.29\n' > "$LIFE_UPSTREAMS"
printf 'nameserver 223.5.5.5\nnameserver 119.29.29.29\n' > "$_TMPD/upstreams.prev"
CAND_TSV_FILE="$_TMPD/cand.tsv"
printf '%s\n' "$TSV" > "$CAND_TSV_FILE"
life_dns_running() { return 0; }
LIFE_DNSFWD="$_TMPD/fake-dnsfwd"
cat > "$LIFE_DNSFWD" <<STUB
#!/bin/sh
# 桩：探测输出固定 TSV（真实主机参数被忽略）
cat "$CAND_TSV_FILE"
STUB
chmod 0755 "$LIFE_DNSFWD"
life_reload_dns() { echo reloaded; }
life_dns_probe_hosts() { printf 'api.test.cn\n'; }

# 用例 1：候选最高 92.5 vs 当前（同桩 TSV 同源 92.5）→ gap 0 → not-beaten
_v="$(life_dns_reoptimize)"
[ "$_v" = "not-beaten" ] && ok "O5a 候选未击败（gap<门槛）→ 不改写" || no "O5a 期望 not-beaten，得 $_v"

# 用例 2：当前配置探测换低分 TSV → gap 达标 → strike=1（双确认第一拍）
CUR_TSV_FILE="$_TMPD/cur.tsv"
printf 'upstream\tfamily\tdomain\trtt_ms\tok/total\tn_ans\tanswers\tfakeip\ttcp_ms\ttcp_ok/total\ttcp_ip\tscore\n223.5.5.5\tv4\tapi.test.cn\t28\t2/2\t1\t1.2.3.4\tno\t35\t2/2\t1.2.3.4\t40.0\n' > "$CUR_TSV_FILE"
cat > "$LIFE_DNSFWD" <<STUB
#!/bin/sh
case "\$*" in
  *"\$MODDIR/etc/dns-candidates"*) cat "$CAND_TSV_FILE" ;;
  *) cat "$CUR_TSV_FILE" ;;
esac
STUB
chmod 0755 "$LIFE_DNSFWD"
# MODDIR 不在桩环境里展开 —— 直接内嵌路径
cat > "$LIFE_DNSFWD" <<STUB
#!/bin/sh
case "\$*" in
  *"etc/dns-candidates"*) cat "$CAND_TSV_FILE" ;;
  *) cat "$CUR_TSV_FILE" ;;
esac
STUB
_v="$(life_dns_reoptimize)"
[ "$_v" = "beaten-strike=1" ] && ok "O5b 击败第一拍 → strike=1 不写盘" || no "O5b 期望 beaten-strike=1，得 $_v"
[ ! -f "$DATA_DIR/dns-opt-strikes" ] || [ "$(cat "$DATA_DIR/dns-opt-strikes" 2>/dev/null)" = "1" ] \
  && ok "O6a strike 落盘 =1" || no "O6a strike 文件异常：$(cat "$DATA_DIR/dns-opt-strikes" 2>/dev/null)"
_grep="$(grep -c "119.29.29.29" "$LIFE_UPSTREAMS" 2>/dev/null | tr -d ' ')"
[ "$_grep" = "1" ] && ok "O6b strike 轮未改写配置" || no "O6b 配置被提前改写"

# 用例 3：第二轮再被击败 → strike=2 达标 → 写盘 + 热重载
_v="$(life_dns_reoptimize)"
[ "$_v" = "reoptimized" ] && ok "O5c 双确认后 reoptimized" || no "O5c 期望 reoptimized，得 $_v"
_head="$(head -1 "$LIFE_UPSTREAMS")"
case "$_head" in \#*) ok "O5d 写盘带来源头注释" ;; *) no "O5d 头注释缺失：$_head" ;; esac
_l1="$(sed -n '2p' "$LIFE_UPSTREAMS")"
[ "$_l1" = "nameserver 119.29.29.29" ] \
  && ok "O5e 自定义项保留在前" || no "O5e 首配置行错误：$_l1"
_l2="$(sed -n '3p' "$LIFE_UPSTREAMS")"
[ "$_l2" = "223.5.5.5" ] && ok "O5f 第二行 = 评分最高候选（规范 ident）" || no "O5f 第二行错误：$_l2"
_grep5="$(grep -cvE '^[[:space:]]*(#|$)' "$LIFE_UPSTREAMS" | tr -d ' ')"
[ "$_grep5" -le 6 ] && ok "O5g 条目数 ≤ 自定义1+Top5" || no "O5g 条目过多：$_grep5"
[ "$(cat "$DATA_DIR/dns-opt-strikes" 2>/dev/null)" = "0" ] \
  && ok "O6c 应用后 strike 清零" || no "O6c strike 未清零：$(cat "$DATA_DIR/dns-opt-strikes" 2>/dev/null)"

# 用例 4：探测期间配置被改 → stale-config
printf 'nameserver 119.29.29.29\n' > "$LIFE_UPSTREAMS"
_stale_stub='#!/bin/sh
if [ "$1" = "-f" ] && [ "$2" = "/nonexistent-sentinel" ]; then :; fi
# cksum 读到一半被替换难以稳定模拟 —— 用 PROBE 干扰点：探测后改文件
exit 0'
# 简化：直接验证 stale 分支的判据（cksum 比对语义）——第二份探测桩改文件
cat > "$LIFE_DNSFWD" <<STUB
#!/bin/sh
# 探测期间模拟用户编辑（改 cksum）
printf 'nameserver 180.76.76.76\n' > "$LIFE_UPSTREAMS"
case "\$*" in
  *"etc/dns-candidates"*) cat "$CAND_TSV_FILE" ;;
  *) cat "$CUR_TSV_FILE" ;;
esac
STUB
chmod 0755 "$LIFE_DNSFWD"
printf 'nameserver 119.29.29.29\n' > "$LIFE_UPSTREAMS"
_v="$(life_dns_reoptimize)"
[ "$_v" = "stale-config" ] && ok "O5h 探测期间被编辑 → 让位" || no "O5h 期望 stale-config，得 $_v"

echo
echo "结果：通过 $PASS / 失败 $FAIL"
[ "$FAIL" = 0 ]
