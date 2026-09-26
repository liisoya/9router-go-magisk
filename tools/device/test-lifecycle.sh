#!/system/bin/sh
# tools/device/test-lifecycle.sh — 生命周期门禁（真机执行，退出码即判定）
#
# 为什么有它：2026-09-26 事故的两种死法都"没有任何可断言的东西" —— 进程被系统连坐 SIGKILL、
# 或死了没人拉起。这条门禁把"引擎不在会被自己拉回来"和"启动者 cgroup 会被脱掉"变成
# 可重复执行的断言（修复前 T2/T3 必红）。
#
# 判据：
#   T1 守护在场：ops.sh status 的 watchdog=up
#   T2 自愈：kill -9 引擎 → 40s 内出现新 PID 且 /health 200（守护只判进程存在，不做健康度判据）
#   T3 逃逸：在"管理器应用 cgroup"里启动引擎 → 引擎最终 cgroup 必须是 `/`（不是应用 cgroup）
#           —— 复刻 WebUI ksu.exec 的上下文；修复前它落在 uid_<app>/pid_<app>，随应用清理被连坐
#
# 用法（设备侧）：
#   adb push tools/device/test-lifecycle.sh /data/local/tmp/
#   adb shell 'su -c "sh /data/local/tmp/test-lifecycle.sh"'
#
# 已知限制：T3 依赖"root 可写 cgroup 根 cgroup.procs"（cgroup v2 + su 域允许）。若设备不允许，
# T3 会红但 T1/T2 仍必须绿 —— 那种设备上守护是唯一的保命手段。

MODDIR="${1:-/data/adb/modules/ninerouter-go}"
DATA_DIR="${2:-/data/adb/9router-go}"
APP="${3:-me.weishu.kernelsu}"
OPS="$MODDIR/lib/ops.sh"
PIDFILE="$DATA_DIR/9router.pid"

PASS=0; FAIL=0
ok() { echo "  ✅ $1"; PASS=$((PASS + 1)); }
no() { echo "  ❌ $1"; FAIL=$((FAIL + 1)); }
info() { echo "  · $1"; }

[ -x "$OPS" ] || { echo "FAIL: ops.sh 不可执行: $OPS"; exit 2; }

port() { "$OPS" get-port; }
health() { curl -s -m 5 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$(port)/health" 2>/dev/null; }
cgof() { sed -n 's/^0:://p' "/proc/$1/cgroup" 2>/dev/null; }
engpid() { cat "$PIDFILE" 2>/dev/null; }

echo "== 目标：$MODDIR （数据目录 $DATA_DIR）=="

# ── T1 守护在场 ──
ST="$("$OPS" status)"
case "$ST" in
  *watchdog=up*) ok "T1 守护在跑（$(echo "$ST" | tr ' ' '\n' | grep '^watchdog_pid=' | cut -d= -f2)）" ;;
  *watchdog=stale*) no "T1 守护已武装但没在跑（watchdog-start 失败？）" ;;
  *) no "T1 守护未启用（$DATA_DIR/watchdog-armed 缺失 → 不是开机路径启动的）" ;;
esac

# ── T2 守护自愈：强杀引擎 ──
OLD="$(engpid)"
if [ -z "$OLD" ] || ! kill -0 "$OLD" 2>/dev/null; then
  "$OPS" start-engine >/dev/null 2>&1
  sleep 3
  OLD="$(engpid)"
fi
if [ -z "$OLD" ] || ! kill -0 "$OLD" 2>/dev/null; then
  no "T2 前置失败：引擎起不来（先看 $DATA_DIR/9router.log）"
else
  info "T2 强杀引擎 pid=$OLD（模拟系统连坐/OOM/崩溃）"
  kill -9 "$OLD" 2>/dev/null
  i=0; NEW=""
  while [ $i -lt 20 ]; do
    sleep 2
    N="$(engpid)"
    if [ -n "$N" ] && [ "$N" != "$OLD" ] && kill -0 "$N" 2>/dev/null; then NEW="$N"; break; fi
    i=$((i + 1))
  done
  if [ -z "$NEW" ]; then
    no "T2 40s 内未被拉起（守护没干活？看 $DATA_DIR/watchdog.log）"
  else
    H="$(health)"
    [ "$H" = "200" ] && ok "T2 已自愈：新 pid=$NEW cgroup=$(cgof "$NEW") /health=200" \
                     || no "T2 进程回来了但 /health=$H（引擎自身问题，看 9router.log）"
  fi
fi

# ── T3 逃逸：从应用 cgroup 里启动 ──
if ! command -v am >/dev/null 2>&1; then
  info "T3 跳过：无 am 命令"
else
  am start -n "$APP/.ui.MainActivity" >/dev/null 2>&1
  sleep 4
  APID="$(pidof "$APP" 2>/dev/null)"
  if [ -z "$APID" ]; then
    info "T3 跳过：$APP 未运行（无法复刻其 cgroup）"
  else
    ACG="$(cgof "$APID")"
    if [ -z "$ACG" ] || [ "$ACG" = "/" ]; then
      info "T3 跳过：拿不到应用 cgroup（$APP 的 cgroup=$ACG）"
    else
      info "T3 应用 cgroup=$ACG，在其中启动引擎（复刻 WebUI ksu.exec 上下文）"
      "$OPS" hold 120 >/dev/null 2>&1   # 让守护别插手，否则它会替我们在别的上下文里起引擎
      "$OPS" stop-engine >/dev/null 2>&1
      sh -c "echo \$\$ > /sys/fs/cgroup$ACG/cgroup.procs 2>/dev/null; exec \"$OPS\" start-engine" >/dev/null 2>&1
      sleep 3
      EPID="$(engpid)"
      EC="$(cgof "$EPID")"
      if [ -z "$EPID" ] || ! kill -0 "$EPID" 2>/dev/null; then
        no "T3 启动失败（pid=$EPID）"
      elif [ "$EC" = "/" ]; then
        ok "T3 已脱组：pid=$EPID cgroup=$EC（不会再随应用被清理）"
      else
        no "T3 未脱组：pid=$EPID cgroup=$EC（与 $APP 同组 → 会被连坐 SIGKILL）"
      fi
      "$OPS" hold 0 >/dev/null 2>&1
    fi
  fi
fi

# ── T5 维护窗口（hold）：窗口内不许插手，到期后必须能自愈（否则 hold 会永久卡死守护）──
"$OPS" hold 8 >/dev/null 2>&1
P="$(engpid)"
if [ -z "$P" ] || ! kill -0 "$P" 2>/dev/null; then
  no "T5 前置失败：引擎不在"
else
  kill -9 "$P" 2>/dev/null
  sleep 6
  N="$(engpid)"
  if [ -n "$N" ] && kill -0 "$N" 2>/dev/null; then
    no "T5 维护窗口内被拉起（hold 没生效）"
  else
    ok "T5 维护窗口生效：窗口内未插手"
  fi
  i=0; HEAL=""
  while [ $i -lt 15 ]; do
    sleep 2; N="$(engpid)"
    if [ -n "$N" ] && kill -0 "$N" 2>/dev/null; then HEAL="$N"; break; fi
    i=$((i + 1))
  done
  if [ -n "$HEAL" ]; then ok "T5 窗口到期后自愈（pid=$HEAL）"
  else no "T5 窗口到期后仍未拉起（hold 把守护卡死了）"; fi
fi

# ── T4 用户停服意图被尊重：守护绝不能把用户停掉的服务复活（C1 的核心价值）──
"$OPS" stop-user >/dev/null 2>&1
ST2="$("$OPS" status)"
case "$ST2" in
  *engine=stopped*) ok "T4 停服后状态如实为 engine=stopped（与未运行可区分）" ;;
  *) no "T4 停服后状态不是 engine=stopped（当前：$(echo "$ST2" | tr ' ' '\n' | grep '^engine='))" ;;
esac
info "T4 等 30s（> 守护 2 个周期）观察是否被复活"
sleep 30
N="$(engpid)"
if [ -n "$N" ] && kill -0 "$N" 2>/dev/null; then
  no "T4 停服后仍被拉起（pid=$N）——守护不尊重用户意图"
else
  ok "T4 用户停服意图被尊重（30s 未被复活）"
fi
"$OPS" start-user >/dev/null 2>&1
i=0; UP=""
while [ $i -lt 15 ]; do
  sleep 2; N="$(engpid)"
  if [ -n "$N" ] && kill -0 "$N" 2>/dev/null; then UP="$N"; break; fi
  i=$((i + 1))
done
if [ -n "$UP" ]; then ok "T4 显式启动恢复正常（pid=$UP /health=$(health)）"
else no "T4 显式启动失败"; fi

# ── T6 日志轮转（按 lib/log.sh 的声明；隔离数据目录，不碰生产日志）──
T="${TMPDIR:-/data/local/tmp}/logtest"
rm -rf "$T"; mkdir -p "$T"
yes "0123456789" 2>/dev/null | head -n 40000 > "$T/dnsfwd.log" 2>/dev/null
BEFORE="$(wc -c < "$T/dnsfwd.log" 2>/dev/null | tr -d ' ')"
DATA_DIR="$T" sh -c ". '$MODDIR/lib/log.sh'; log_rotate_all" 2>/dev/null
AFTER="$(wc -c < "$T/dnsfwd.log" 2>/dev/null | tr -d ' ')"
if [ -n "$AFTER" ] && [ "$AFTER" -lt 262144 ]; then
  ok "T6 日志轮转生效（${BEFORE}B → ${AFTER}B）"
else
  no "T6 轮转未生效（${BEFORE}B → ${AFTER}B）"
fi
rm -rf "$T"

# ── T7 承载性 env 的单一来源真的进了引擎 ──
MISS=""
KEYS="$(MODDIR="$MODDIR" sh -c ". '$MODDIR/lib/lifecycle.sh'; life_carrier_env_keys" 2>/dev/null)"
EP="$(engpid)"
if [ ! -f "$DATA_DIR/runtime.env" ]; then
  no "T7 runtime.env 不存在（承载性 env 没有单一来源）"
else
  for k in $KEYS; do
    grep -q "^$k=" "$DATA_DIR/runtime.env" || MISS="$MISS $k(runtime.env)"
    if [ -n "$EP" ]; then
      tr '\0' '\n' < "/proc/$EP/environ" 2>/dev/null | grep -q "^$k=" || MISS="$MISS $k(engine)"
    fi
  done
  if [ -z "$MISS" ]; then ok "T7 承载性 env 齐全（runtime.env + 引擎进程）"
  else no "T7 缺：$MISS"; fi
fi

# ── T8 引擎安装门禁（2026-09-26 事故：加速节点 404 正文被当引擎装上）──
SZ_BEFORE="$(wc -c < "$MODDIR/bin/9router-go" 2>/dev/null | tr -d '[:space:]')"
VER_BEFORE="$(cat "$DATA_DIR/engine-version" 2>/dev/null)"
printf 'Not Found' > /data/local/tmp/9r-bogus.new
OUT8="$("$OPS" install-engine /data/local/tmp/9r-bogus.new 9.9.9 2>&1 | tr -d '\r')"
case "$OUT8" in
  *install-rejected-src*) ok "T8a 9 字节 404 正文被拒（$OUT8）" ;;
  *) no "T8a 坏源未被拒绝：$OUT8" ;;
esac
SZ_AFTER="$(wc -c < "$MODDIR/bin/9router-go" 2>/dev/null | tr -d '[:space:]')"
if [ -n "$SZ_BEFORE" ] && [ "$SZ_BEFORE" = "$SZ_AFTER" ]; then ok "T8b 现有引擎未被改动（$SZ_AFTER 字节）"
else no "T8b 现有引擎被动过：$SZ_BEFORE → $SZ_AFTER"; fi
[ "$(cat "$DATA_DIR/engine-version" 2>/dev/null)" = "$VER_BEFORE" ] && ok "T8c engine-version 未被谎报（$VER_BEFORE）" || no "T8c engine-version 被改"
[ "$("$OPS" panel | tr ' ' '\n' | grep '^engine=')" = "engine=up" ] && ok "T8d 引擎仍 up（门禁没有惊动服务）" || no "T8d 引擎不在跑了"
rm -f /data/local/tmp/9r-bogus.new

# ── T9 版本一致性：面板 engine_version 必须等于引擎自报版本 ──
# 2026-09-26：整包更新（install-module，KernelSU 的常规升级路径）装了新引擎，但运行期
# engine-version 文件停在旧值 → 面板谎报"当前 1.9.1"并永远提示有更新。
SELF="$(curl -s -m 5 "http://127.0.0.1:$(port)/version" 2>/dev/null | sed -n 's/.*"currentVersion":"\([^"]*\)".*/\1/p')"
PANEL_VER="$("$OPS" panel | tr ' ' '\n' | sed -n 's/^engine_version=//p')"
if [ -n "$SELF" ]; then
  if [ "$PANEL_VER" = "$SELF" ]; then ok "T9 引擎版本一致（面板 $PANEL_VER = 引擎自报 $SELF）"
  else no "T9 版本不一致：面板 $PANEL_VER / 引擎自报 $SELF（状态谎报）"; fi
else
  info "T9 跳过：/version 未公开（v1.9.2 之前的引擎）或不可达"
fi

# ── T10 整包安装不得自毁：install-module 必须能跑完（执行中被覆写的回归）──
# 2026-09-26 实测：直接 `unzip -oq` 到 $MODDIR 会覆写正在执行的 lib/ops.sh（同 inode）→
# mksh 报 "ops.sh[193]: syntax error"、安装中途夭折。现改为"暂存 + mv 换 inode"。
# 用 $DATA_DIR/last-module.zip 的副本跑（install-module 成功后会 rm 掉入参，不能直接用它）。
ZIP="$DATA_DIR/last-module.zip"
if [ -f "$ZIP" ]; then
  cp "$ZIP" /data/local/tmp/9r-gate.zip
  OUT10="$("$OPS" install-module /data/local/tmp/9r-gate.zip 2>&1 | tr -d '\r')"
  case "$OUT10" in
    *syntax\ error*|*no\ closing\ quote*|*bad\ substitution*|*unexpected\ *)
      no "T10a install-module 报了 shell 解析错误（执行中被覆写？）：$OUT10" ;;
    *) ok "T10a install-module 跑完无解析错误（$(echo "$OUT10" | tail -n 1)）" ;;
  esac
  [ "$("$OPS" panel | tr ' ' '\n' | grep '^engine=')" = "engine=up" ] && ok "T10b 安装后引擎 up" || no "T10b 安装后引擎不在跑"
  rm -f /data/local/tmp/9r-gate.zip
else
  info "T10 跳过：$ZIP 不存在（先跑一次 install-module 生成）"
fi

echo "== 结果：通过 $PASS / 失败 $FAIL =="
[ "$FAIL" = 0 ] || exit 1
exit 0
