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
#   T13 引擎更新链路（只读）：版本清单 → SHA256SUMS → arm64 资产，摘要与 ELF 必须对得上
#           —— 不安装、不改引擎版本；前置不可达即如实 SKIP，绝不假绿
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

PASS=0; FAIL=0; FAILED_TXT=""
ok() { echo "  ✅ $1"; PASS=$((PASS + 1)); }
no() { echo "  ❌ $1"; FAIL=$((FAIL + 1)); FAILED_TXT="$FAILED_TXT|$1"; }
info() { echo "  · $1"; }

[ -x "$OPS" ] || { echo "FAIL: ops.sh 不可执行: $OPS"; exit 2; }

port() { "$OPS" get-port; }
health() { curl -s -m 5 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$(port)/health" 2>/dev/null; }
cgof() { sed -n 's/^0:://p' "/proc/$1/cgroup" 2>/dev/null; }
engpid() { cat "$PIDFILE" 2>/dev/null; }
# 等就绪/等消失的唯一实现（零依赖，可直接 source）：谓词在**当前 shell** 执行，顺手把 pid 带出来
. "$MODDIR/lib/wait.sh" || { echo "FAIL: 缺 $MODDIR/lib/wait.sh（等就绪/等消失的唯一实现）"; exit 2; }
livepid() { N="$(engpid)"; [ -n "$N" ] && kill -0 "$N" 2>/dev/null; }
newpid() { N="$(engpid)"; [ -n "$N" ] && [ "$N" != "$1" ] && kill -0 "$N" 2>/dev/null; }
# 自检查询：必须**一次 panel 快照**取多个字段 —— 每次 panel 都是新进程，
# "刚自愈"标记只在那一次调用里为 1，分多次取会读到第二次的 0（这正是 T12 第一版写错的地方）
panel_snapshot() { "$OPS" panel; }
snap_field() { printf '%s' "$1" | tr ' ' '\n' | grep "^$2=" | cut -d= -f2; }

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
  N=""; NEW=""
  if wait_for 20 2 newpid "$OLD"; then NEW="$N"; fi
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
  N=""; HEAL=""
  if wait_for 15 2 livepid; then HEAL="$N"; fi
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
N=""; UP=""
if wait_for 15 2 livepid; then UP="$N"; fi
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

# ── T13 引擎更新链路的「下载 + 校验」只读门禁（2026-09-27 用户实测：点下载必失败）──
# 原缺陷不在安装，而在**地址契约**：面板拿 version.json 的 latestVersion（"1.9.3"，裸版本号）
# 当 tag 拼 release URL，而上游 tag 是 "v1.9.3" → 404（9 字节 "Not Found"，与 2026-09-26
# 「404 正文被当引擎装上」是同一个东西）；同时 KB.fetch 少 -L，302 的资产地址只剩空正文。
# 这里在真机上把**修好之后**的链路走一遍：清单 → SHA256SUMS → arm64 资产 → 比对摘要与 ELF。
# 只读：不执行 install-engine、不改引擎版本；引擎已是该版本时同样能跑（验链路，不验"有更新"）。
ACCEL13="$(cat "$DATA_DIR/github-accel" 2>/dev/null | tr -d '[:space:]')"
TMP13="${TMPDIR:-/data/local/tmp}/9r-t13"
if ! curl -sL -m 20 "${ACCEL13}https://raw.githubusercontent.com/luqman-v1/9router-go/main/version.json" -o "$TMP13.json" 2>/dev/null \
   || [ ! -s "$TMP13.json" ]; then
  info "T13 跳过：上游版本清单取不到（加速节点「$ACCEL13」/ 外网不可达）"
else
  LATEST="$(tr ',' '\n' < "$TMP13.json" | sed -n 's/.*"latestVersion"[^"]*"\([^"]*\)".*/\1/p' | head -n 1)"
  if [ -z "$LATEST" ]; then
    no "T13 清单解析不出 latestVersion（上游格式变了？）"
  else
    case "$LATEST" in v*|V*) TAG="$LATEST" ;; *) TAG="v$LATEST" ;; esac
    BASE="${ACCEL13}https://github.com/luqman-v1/9router-go/releases/download/$TAG"
    SUMS=""
    if curl -sL -m 30 "$BASE/SHA256SUMS.txt" -o "$TMP13.sums" 2>/dev/null && [ -s "$TMP13.sums" ]; then
      SUMS="$(awk '$2=="9router-go-linux-arm64"||$2=="*9router-go-linux-arm64"{print $1}' "$TMP13.sums" | head -n 1)"
    fi
    if [ -z "$SUMS" ]; then
      no "T13 取不到 SHA256SUMS 里的 arm64 摘要（$TAG；加速节点「$ACCEL13」）→ 面板会按 fail-closed 拒绝更新"
    elif curl -fsSL -m 180 "$BASE/9router-go-linux-arm64" -o "$TMP13.bin" 2>/dev/null; then
      GOT="$(sha256sum "$TMP13.bin" 2>/dev/null | cut -d' ' -f1)"
      MAGIC="$(head -c 4 "$TMP13.bin" 2>/dev/null | od -An -tx1 | tr -d '[:space:]')"
      SZ13="$(wc -c < "$TMP13.bin" 2>/dev/null | tr -d '[:space:]')"
      [ "$GOT" = "$SUMS" ] && ok "T13a $TAG 的 arm64 摘要与 SHA256SUMS 一致（${SZ13}B）" \
                           || no "T13a 摘要不一致：实际 $GOT / 期望 $SUMS"
      if [ -n "$SZ13" ] && [ "$SZ13" -ge 5242880 ] && [ "$MAGIC" = "7f454c46" ]; then
        ok "T13b 下载物过 engine_src_ok 的两个判据（体积 ${SZ13}B / 魔数 $MAGIC）"
      else
        no "T13b 下载物不像引擎（体积 ${SZ13:-?} / 魔数 ${MAGIC:-?}）"
      fi
      info "T13 只读校验完成（未安装；设备引擎 $("$OPS" panel | tr ' ' '\n' | sed -n 's/^engine_version=//p')，上游最新 $LATEST）"
    else
      no "T13 arm64 资产下载失败（$TAG；加速节点「$ACCEL13」）"
    fi
  fi
fi
rm -f "$TMP13.json" "$TMP13.sums" "$TMP13.bin"

# ── T11 「等就绪 / 等消失」原语本身（唯一实现 lib/wait.sh）──
# 门禁自己用同一实现跑一遍：语义坏了（比如假谓词也报成功）会在这里先红，
# 而不是等到 T2/T4/T5 超时才发现"门禁自己不可信"
if wait_for 3 0.2 true; then ok "T11a wait_for：谓词为真 → 成功"; else no "T11a wait_for 对真谓词报了失败"; fi
if wait_for 2 0.1 false; then no "T11a2 wait_for 对假谓词报了成功（会谎报拉起）"; else ok "T11a2 wait_for：谓词为假 → 如实失败"; fi
if wait_for 0 0.1 true; then no "T11a3 wait_for 次数 0 却成功"; else ok "T11a3 wait_for：次数非法 → 拒绝"; fi
sleep 0.1 &
_tp=$!
wait "$_tp" 2>/dev/null
if wait_gone 3 0.2 "$_tp"; then ok "T11b wait_gone：已退出 → 判消失"; else no "T11b wait_gone 对已退出的 pid 报了失败"; fi
sleep 30 &
_ap=$!
if wait_gone 2 0.2 "$_ap"; then no "T11c wait_gone 对活着的 pid 报了消失"; else ok "T11c wait_gone：仍活着 → 不谎报消失"; fi
kill -9 "$_ap" 2>/dev/null

# ── T12 运行期引擎版本自愈（整包更新不跑我们的代码 → 面板曾谎报旧版本，看着像"假更新"）──
# 2026-09-26 用户实测：包已是 r2、引擎实际在跑 1.9.2，面板却写 1.9.1（运行期文件停在上一个版本）。
PKG_VER="$(cat "$MODDIR/etc/engine-version" 2>/dev/null)"
if [ -n "$PKG_VER" ]; then
  # T12a：整包更新（由旧代码/管理器执行，不会写运行期文件）→ 记录落后 → 自愈 + 来源自检如实
  printf '0.0.1\n' > "$DATA_DIR/engine-version"
  printf '1\n' > "$DATA_DIR/engine-version-code"     # 模拟"包已换成新 versionCode，但运行期还是旧的"
  SNAP="$(panel_snapshot)"
  [ "$(snap_field "$SNAP" engine_version)" = "$PKG_VER" ] && ok "T12a 整包更新后运行期版本自愈（0.0.1 → $PKG_VER）" \
                                                          || no "T12a 未自愈：panel=$(snap_field "$SNAP" engine_version)，包内=$PKG_VER"
  if [ "$(snap_field "$SNAP" engine_ver_src)" = "package" ] && [ "$(snap_field "$SNAP" engine_ver_healed)" = "1" ]; then
    ok "T12a2 自检字段如实：来源=包内 · 刚自愈"
  else
    no "T12a2 自检字段不对（src=$(snap_field "$SNAP" engine_ver_src) healed=$(snap_field "$SNAP" engine_ver_healed)）"
  fi
  # T12b：稳态再读一次 → 不该再"自愈"（值来自运行期记录）
  SNAP="$(panel_snapshot)"
  if [ "$(snap_field "$SNAP" engine_ver_src)" = "runtime" ] && [ "$(snap_field "$SNAP" engine_ver_healed)" = "0" ]; then
    ok "T12b 稳态：来源=运行期记录 · 未重复自愈"
  else
    no "T12b 稳态字段不对（src=$(snap_field "$SNAP" engine_ver_src) healed=$(snap_field "$SNAP" engine_ver_healed)）"
  fi
  # T12c：同 versionCode 的重装（只能靠 mtime 判；-nt 精度是秒，所以这里 sleep 1）
  printf '0.0.2\n' > "$DATA_DIR/engine-version"
  printf '%s\n' "$(grep '^versionCode=' "$MODDIR/module.prop" | cut -d= -f2)" > "$DATA_DIR/engine-version-code"
  sleep 1
  touch "$MODDIR/module.prop"                        # 包比运行期文件新 = 刚重装过
  SNAP="$(panel_snapshot)"
  [ "$(snap_field "$SNAP" engine_version)" = "$PKG_VER" ] && ok "T12c 同版本重装也自愈（0.0.2 → $PKG_VER）" \
                                                          || no "T12c 未自愈：panel=$(snap_field "$SNAP" engine_version)，包内=$PKG_VER"
  # T12d：运行期更新过引擎（install-engine 会同时写记录）→ 绝不能被包内旧值覆盖
  printf '9.9.9\n' > "$DATA_DIR/engine-version"
  printf '%s\n' "$(grep '^versionCode=' "$MODDIR/module.prop" | cut -d= -f2)" > "$DATA_DIR/engine-version-code"
  touch "$DATA_DIR/engine-version"
  SNAP="$(panel_snapshot)"
  [ "$(snap_field "$SNAP" engine_version)" = "9.9.9" ] && ok "T12d 运行期更新不被包内旧值覆盖" \
                                                       || no "T12d 运行期版本被覆盖：panel=$(snap_field "$SNAP" engine_version)（应为 9.9.9）"
  # T12e：运行期文件缺失 → 从包内补齐（并留下记录，供下次判断）
  rm -f "$DATA_DIR/engine-version"
  SNAP="$(panel_snapshot)"
  [ "$(snap_field "$SNAP" engine_version)" = "$PKG_VER" ] && ok "T12e 运行期文件缺失时从包内补齐（$PKG_VER）" \
                                                          || no "T12e 未补齐：panel=$(snap_field "$SNAP" engine_version)"
else
  info "T12 跳过：包内 etc/engine-version 不存在"
fi

# ── T15 快路径的前提校正（2026-09-29 架构走查 A2）──
# 守护用 eng_ours 判断"引擎是不是我亲手起的"来决定快慢路径。该值若陈旧（典型时序：守护起过引擎
# → 用户「停止服务」让 stop_all 先 kill 再删 pidfile → 判据不成立 → 不清空 → 用户再「启动服务」
# 起的新引擎不是守护子进程），就会既收不到 CHLD、又因为变量非空而停在 60s 长周期 ——
# 自愈从秒级退化成最多 60s。这里复刻那条时序，并给一个**能区分的上限**：
#   修好 → 退回 10s 短周期兜底（两次判死 ≈ 20s，+启动）；没修 → 最多 2×60 = 120s。
info "T15 复刻：停服 → 启服（新引擎不再是守护子进程）→ 强杀，看自愈是否退化成 60s 长周期"
"$OPS" stop-user >/dev/null 2>&1
"$OPS" start-user >/dev/null 2>&1
sleep 3
P15="$(engpid)"
if [ -z "$P15" ] || ! kill -0 "$P15" 2>/dev/null; then
  no "T15 前置失败：停/启之后引擎不在（pid=$P15）"
else
  kill -9 "$P15" 2>/dev/null
  S15="$(date +%s)"
  if wait_for 30 2 livepid; then
    ok "T15 停/启之后仍能自愈（$(( $(date +%s) - S15 ))s ≤30s；若 >60s 说明退回了长周期）"
  else
    no "T15 自愈超时（>30s）—— 快路径前提未被校正（eng_ours 陈旧 → 停在 60s 长周期）"
  fi
fi

# ── T14 守护"必然在跑"的三要素（2026-09-29 真机事故：开机那次守护没起来，且日志无痕）──
# 为什么是这三条：守护是**唯一的自愈者** —— 它不在，引擎任何死因都不会再被拉起（用户看到
# "要手动开"）。而它的失败方式恰好都很隐蔽：① 保护是"继承"来的（换个启动路径就变可杀）
# ② pidfile 的号会被无关进程复用（"活着"≠"是我们的守护"）③ 失败了不留任何日志。
# 放在 T10 之前：T10 会替换设备上的 lib/（破坏性），必须在它之前跑。
WDPID="$(cat "$DATA_DIR/watchdog.pid" 2>/dev/null | tr -d ' \n')"
case "$WDPID" in ''|*[!0-9]*) WDPID="" ;; esac
if [ -n "$WDPID" ] && kill -0 "$WDPID" 2>/dev/null; then
  ADJ14="$(cat "/proc/$WDPID/oom_score_adj" 2>/dev/null)"
  [ "$ADJ14" = "-1000" ] && ok "T14a 守护 oom_score_adj=-1000（内存压力下不会被杀）" \
                         || no "T14a 守护 oom_score_adj=[$ADJ14]（期望 -1000：被杀即整机失去自愈）"
  CMD14="$(tr '\0' ' ' < "/proc/$WDPID/cmdline" 2>/dev/null)"
  case "$CMD14" in
    *watchdog.sh*) ok "T14b 身份可按 cmdline 认出（pid=$WDPID）" ;;
    *) no "T14b pidfile 的 pid 不是守护（cmdline=$CMD14）" ;;
  esac
  [ "$("$OPS" panel | tr ' ' '\n' | grep '^watchdog=')" = "watchdog=up" ] \
    && ok "T14c 面板 watchdog=up（身份校验没误判）" \
    || no "T14c 面板没报 watchdog=up（身份校验把活着的守护判死了？）"
else
  no "T14a/b/c 守护不在跑（pidfile=[$WDPID]）—— 整机当前没有自愈能力"
fi
grep -q 'watchdog: 引导中' "$DATA_DIR/watchdog.log" 2>/dev/null \
  && ok "T14d 守护留下了引导证据行（用它区分「没被执行」与「在 source 里就死」）" \
  || no "T14d 守护日志缺引导证据行（正在跑的是旧代码？）"
if grep -q 'boot: watchdog=' "$DATA_DIR/9router.log" 2>/dev/null; then
  ok "T14e 开机判据已入日志：$(grep 'boot: watchdog=' "$DATA_DIR/9router.log" | tail -n 1 | sed 's/.*\] //')"
else
  info "T14e 跳过：本次运行还没重启过（boot: watchdog= 只在开机路径写）"
fi

# ── T10 整包安装不得自毁：install-module 必须能跑完（执行中被覆写的回归）──
# 2026-09-26 实测：直接 `unzip -oq` 到 $MODDIR 会覆写正在执行的 lib/ops.sh（同 inode）→
# mksh 报 "ops.sh[193]: syntax error"、安装中途夭折。现改为"暂存 + mv 换 inode"。
# 用 $DATA_DIR/last-module.zip 的副本跑（install-module 成功后会 rm 掉入参，不能直接用它）。
#
# **必须放在最后**：这一步会把设备上的 lib/ 换成 zip 里的版本（那正是它的目的）。2026-09-26 验收时
# 它夹在中间，把 dev 直推的 lib/wait.sh 换掉了，导致后面的 T11 只能靠"脚本开头已 source 进内存"
# 侥幸通过、复跑必红 —— 破坏性断言放在末尾，其余断言才在同一个代码状态下运行。
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
  # **必须说清副作用**：本步把设备 lib/ 换成了包内（发布版）那份 —— 开发直推
  # （tools/deploy-device.sh）带来的新代码从此失效，下次开机跑的就是包内那份。
  # 不说清的话，下一轮 T14 会在**旧代码**上跑，把"新代码的问题"和"代码已被换回"混成一团。
  info "⚠️ 设备 lib/ 已被换成包内版本（开发直推态已失效）；回到开发态请重跑 tools/deploy-device.sh"
else
  info "T10 跳过：$ZIP 不存在（先跑一次 install-module 生成）"
fi

echo "== 结果：通过 $PASS / 失败 $FAIL =="
# 证据留存：2026-09-26 观测到一次偶发失败（1/4 次）但在 stdout 之外没有痕迹 ——
# 每次运行把「时间 + 计数 + 失败项」追加到设备日志，偶发失败的现场不再随终端滚走。
if [ -n "${DATA_DIR:-}" ] && [ -d "$DATA_DIR" ]; then
  {
    printf '== %s 通过 %s / 失败 %s\n' "$(date '+%F %T')" "$PASS" "$FAIL"
    if [ -n "$FAILED_TXT" ]; then echo "$FAILED_TXT" | tr '|' '\n' | sed '/^$/d'; fi
  } >> "$DATA_DIR/gate-lifecycle.log" 2>/dev/null
  tail -n 200 "$DATA_DIR/gate-lifecycle.log" > "$DATA_DIR/gate-lifecycle.log.tmp" 2>/dev/null \
    && mv "$DATA_DIR/gate-lifecycle.log.tmp" "$DATA_DIR/gate-lifecycle.log" 2>/dev/null
  echo "（摘要已追加到 $DATA_DIR/gate-lifecycle.log）"
fi
[ "$FAIL" = 0 ] || exit 1
exit 0
