#!/system/bin/sh
# 9router-go · late_start service
# 本脚本只负责"在开机这个时机、按这个顺序"做事；每个动作的语义都在 lib/lifecycle.sh
# （唯一所有者，ADR-0005）。开机路径是守护唯一被允许武装的上下文（ADR-0004）：
# init/ksud 上下文（cgroup 为 `/`）起的守护不会被"系统清理管理器应用"波及。
#
# 两个承载性组件，删了引擎就不能用（它们的准备都在 life_prep / life_ensure_engine 里）：
#   1) dnsfwd：引擎是纯 Go 静态二进制，读不到 /etc/resolv.conf 时会回落到
#      127.0.0.1:53（真机实测该文件根本不存在），必须有本地转发器接住；
#   2) SSL_CERT_DIR：没有它所有 HTTPS 与更新检查都会失败。

# PATH 显式声明（架构审查 S2）：开机路径的 PATH 由 init/ksud 决定，真机实测过某些上下文
# 连 /system/bin/sleep 都没有 —— 而 ping 等待与引擎拉起全依赖这些工具。
export PATH="/data/adb/magisk:/data/adb/ksu/bin:/sbin:/system/sbin:/system/bin:/system/xbin:/vendor/bin:/odm/bin:/product/bin:$PATH"

# MODDIR 必须解析为绝对路径：以 `sh service.sh`（相对路径）调用时
# ${0%/*} 会得到 "service.sh"，导致 bin 路径拼错、引擎起不来。
case "$0" in
  */*) MODDIR="${0%/*}" ;;
  *)   MODDIR="$(cd "$(dirname "$0")" 2>/dev/null && pwd)" ;;
esac
case "$MODDIR" in
  /*) ;;
  *)  MODDIR="$(pwd)" ;;
esac
DATA_DIR="${DATA_DIR:-/data/adb/9router-go}"
. "$MODDIR/lib/lifecycle.sh" || exit 1
LOG="$DATA_DIR/9router.log"

# --- 开机意图：清"用户停止 / 关闭守护"两个标志、武装并启动守护（只有这里能清）---
life_boot

# --- 数据与环境准备（schema 引导 / autoUpdate 压回 false / 密码 / DNS 兜底），幂等 ---
# 输出 ok / degraded：degraded 会写进日志（不谎报成功），但不阻塞启动。
life_log "prep: $(life_prep)"

# --- DNS 转发器（内含用户开关与 :53 占用让路）---
life_ensure_dns >>"$DATA_DIR/dnsfwd.log" 2>&1

# --- 出厂客户端 key：仅在 apiKeys 表为空（全新安装）时补入（数据动作，留在 ops.sh）---
"$MODDIR/lib/ops.sh" seed-key >>"$LOG" 2>&1

# --- 等网络就绪（最多 15s，避免开机时无网络导致首启失败）---
i=0
while [ $i -lt 15 ]; do
  if ping -c1 -W1 223.5.5.5 >/dev/null 2>&1 || ping -c1 -W1 1.1.1.1 >/dev/null 2>&1; then
    break
  fi
  i=$((i + 1))
  sleep 1
done

# --- 拉起引擎（唯一实现；幂等：已在跑则只补 cgroup 逃逸）---
( LIFE_CALLER=service.sh; export LIFE_CALLER; life_ensure_engine ) >>"$LOG" 2>&1

# --- 守护第二次机会 ---
# life_boot 里那次是在系统最繁忙的时刻（引擎/DNS 都还没起），2026-09-29 真机实测就在那里
# 失败过（且失败被静默丢弃）。守护是**唯一的自愈者**，缺了它整机失去自愈能力 —— 所以等
# 网络等待与引擎启动都过去、系统缓和之后再确认一次，并把判据如实写进日志。
life_log "wd-ensure: $(life_wd_start)"

exit 0
