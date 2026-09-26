#!/system/bin/sh
# lifecycle.sh — 服务生命周期的唯一所有者（可 source 的库；source 时零副作用）
#
# 为什么有这个 module（ADR-0005）：「服务该不该在跑、谁把它拉起来、用户是不是要它停」
# 这个事实原先被编码成 6 个状态文件，由 ops.sh / watchdog.sh / service.sh / uninstall.sh
# 四方各自读写，优先级只靠 if 的书写顺序表达 —— 已经因此出过 bug（restart-engine 删掉
# 用户的 watchdog-off，用户关不掉守护；watchdog-start 与 pidfile 落盘的竞态把重启退化成
# "在调用者 cgroup 里启动"）。现在状态文件是本文件的**实现细节**，调用方只表达意图。
#
# 接口（调用方只需要知道这些动词；加粗项是给用户表达意图的，其余是给守护/运维用的）：
#   意图：life_boot            开机：清"用户关闭"、武装并启动守护（只能在 init/ksud 上下文）
#         life_stop_user       用户显式停服务：停进程 + 记住意图（守护不再复活）
#         life_start_user      用户显式启服务：清意图 + 拉起
#         life_restart_engine  停 →（有守护则委托它）→ 起，内置等待，echo engine=up|down
#         life_stop_all [--user]
#   幂等：life_ensure_engine / life_ensure_dns（在跑就返回 running，尊重用户意图）
#   谓词：life_engine_healthy / life_dns_healthy / life_wd_should_supervise / life_wd_take_request
#   只读：life_state（机器可读一行，供 ops.sh status 组合）
#   准备：life_prep（引擎启动前必须为真的东西：schema / autoUpdate 闸门 / 密码 / DNS 兜底）
#
# 前置约定：调用方必须先给出 MODDIR（本库不猜模块路径 —— 猜错过一次：
# uninstall.sh 硬编码 /modules/ninerouter-go）。DATA_DIR 缺省 /data/adb/9router-go。
[ -n "${MODDIR:-}" ] || { echo "lifecycle.sh: 必须先设置 MODDIR（调用方自己知道模块路径）" >&2; return 1 2>/dev/null || exit 1; }

DATA_DIR="${DATA_DIR:-/data/adb/9router-go}"
# 日志策略（路径/上限/轮转）在 lib/log.sh —— 唯一所有者；本库只经它写日志
. "$MODDIR/lib/log.sh" || { echo "lifecycle.sh: 缺 lib/log.sh" >&2; return 1 2>/dev/null || exit 1; }
# 「等就绪 / 等消失」的唯一实现在 lib/wait.sh（本库、守护、真机门禁共用；零依赖）
. "$MODDIR/lib/wait.sh" || { echo "lifecycle.sh: 缺 lib/wait.sh" >&2; return 1 2>/dev/null || exit 1; }
LIFE_DB="$DATA_DIR/db/data.sqlite"
LIFE_SQLITE3="$MODDIR/bin/sqlite3"
LIFE_SCHEMA="$MODDIR/etc/schema.sql"
LIFE_BIN="$MODDIR/bin/9router-go"
LIFE_DNSFWD="$MODDIR/bin/dnsfwd"
LIFE_UPSTREAMS="$DATA_DIR/dns-upstreams.conf"
LIFE_DNS_BIND_FILE="$DATA_DIR/dns-bind"
LIFE_WATCHDOG="$MODDIR/lib/watchdog.sh"
LIFE_RUNTIME_ENV="$DATA_DIR/runtime.env"
# ── 状态文件（实现细节：只有本文件读写；别的文件不该知道这些名字）──
LIFE_ST_ENGINE="$DATA_DIR/9router.pid"
LIFE_ST_DNS="$DATA_DIR/dnsfwd.pid"
LIFE_ST_DNS_OFF="$DATA_DIR/dns-disabled"
LIFE_ST_USER_OFF="$DATA_DIR/service-off"
LIFE_ST_WD_PID="$DATA_DIR/watchdog.pid"
LIFE_ST_WD_ARMED="$DATA_DIR/watchdog-armed"
LIFE_ST_WD_HOLD="$DATA_DIR/watchdog-hold"
LIFE_ST_WD_REQ="$DATA_DIR/watchdog.req"
LIFE_ST_WD_OFF="$DATA_DIR/watchdog-off"
LIFE_WD_LOG="$LOG_WATCHDOG_PATH"
LIFE_DNS_LOG="$LOG_DNS_PATH"

# ── 原语 ────────────────────────────────────────
life_pid_alive() { [ -f "$1" ] && kill -0 "$(cat "$1" 2>/dev/null)" 2>/dev/null; }
life_pid_of() { cat "$1" 2>/dev/null; }
life_pid_gone() {
  # "已经退出"判定（含僵尸态）—— 实现唯一所有者在 lib/wait.sh，这里只保留历史名字给既有调用方
  wait_pid_gone "${1:-}"
}
life_cgroup_of() { sed -n 's/^0:://p' "/proc/$1/cgroup" 2>/dev/null; }
life_pid_is_exe() {
  # 进程身份校验：pidfile 里的号可能被无关进程复用（Android pid_max 不大，长跑会绕回）。
  # 杀之前必须确认"这就是我们的二进制"。模块更新后二进制被 mv 掉，exe 链接会带 " (deleted)"。
  _pid="$1"; _want="$2"
  case "$_pid" in ''|*[!0-9]*) return 1 ;; esac
  _exe="$(readlink "/proc/$_pid/exe" 2>/dev/null)"
  case "$_exe" in "$_want"|"$_want (deleted)") return 0 ;; esac
  return 1
}
life_cgroup_escape() {
  # 把进程从"启动者的 cgroup"里挪出去（连坐免疫）。真机实测：WebUI 经 ksu.exec 启动的
  # 进程落在管理器应用的 cgroup（0::/uid_10235/pid_X），系统清理该应用时整组 SIGKILL；
  # setsid/nohup 只换会话不换 cgroup，救不了。
  _pid="${1:-}"
  case "$_pid" in ''|*[!0-9]*) return 1 ;; esac
  [ -r "/proc/$_pid/cgroup" ] || return 1
  _cur="$(life_cgroup_of "$_pid")"
  case "$_cur" in
    '') return 1 ;;                 # 非 cgroup v2（拿不到统一层级）→ 如实返回失败
    '/') return 0 ;;                # 已在根 cgroup（init/ksud 上下文，天然免疫）
  esac
  for _r in /sys/fs/cgroup /dev/cgroup; do
    [ -w "$_r/cgroup.procs" ] || continue
    echo "$_pid" > "$_r/cgroup.procs" 2>/dev/null && return 0
  done
  return 1
}
life_port53_busy() {
  # 优先 ss（Android netstat 对 UDP 监听展示不可靠），netstat 兜底
  if command -v ss >/dev/null 2>&1; then
    ss -tuln 2>/dev/null | grep -qE '[:.]53[[:space:]]'
  else
    netstat -tuln 2>/dev/null | grep -qE '[:.]53[[:space:]]'
  fi
}
life_read_bind() {
  _b="$(cat "$LIFE_DNS_BIND_FILE" 2>/dev/null | tr -d ' \n')"
  [ "$_b" = "any" ] && echo any || echo loopback
}
life_get_port() {
  p=""
  if [ -z "${PORT:-}" ] && [ -f "$DATA_DIR/port" ]; then
    p="$(cat "$DATA_DIR/port" 2>/dev/null | tr -d ' \n')"
    case "$p" in ''|*[!0-9]*) p= ;; esac
    if [ -n "$p" ] && [ "$p" -ge 1 ] && [ "$p" -le 65535 ]; then PORT="$p"; fi
  fi
  echo "${PORT:-20130}"
}
life_log() { log_write engine "[$(date '+%F %T')] $*"; }

# ── 引擎运行环境（承载性 env 的唯一声明）──────────────
# 「引擎需要哪些 env 才能正常工作」原先只活在 export 语句与散文注释里，其中两个是**承载性**的：
# 没有 SSL_CERT_DIR 则所有 HTTPS 与更新检查全废；没有 AUTO_UPDATE=false 则引擎会按 DB 开启
# 自更新、绕过模块管理。现在清单是唯一声明，产出物是 $DATA_DIR/runtime.env（0600，含密码），
# 启动前 source 它；门禁 T7 逐条断言"键在文件里 + 真的进了引擎进程"。
life_env_q() {
  # 单引号安全包裹（值里带空格/引号也不会破坏 runtime.env 的语法）
  printf "'%s'" "$(printf '%s' "${1:-}" | sed "s/'/'\\\\''/g")"
}
life_carrier_env_keys() {
  # 唯一声明：这些键缺失 = 引擎不可用或不受管
  cat <<'EOF'
SSL_CERT_DIR
AUTO_UPDATE
PORT
DATA_DIR
MODDIR
INITIAL_PASSWORD
EOF
}
life_write_runtime_env() {
  _p="$(life_get_port)"
  {
    echo "# 由 lib/lifecycle.sh 生成（勿手改）：引擎运行环境（承载性 env 的单一来源）"
    echo "SSL_CERT_DIR=$(life_env_q /system/etc/security/cacerts)"
    echo "AUTO_UPDATE=$(life_env_q false)"
    echo "PORT=$(life_env_q "$_p")"
    echo "DATA_DIR=$(life_env_q "$DATA_DIR")"
    echo "MODDIR=$(life_env_q "$MODDIR")"
    echo "INITIAL_PASSWORD=$(life_env_q "$(cat "$DATA_DIR/initial-password" 2>/dev/null)")"
  } > "$LIFE_RUNTIME_ENV" 2>/dev/null || return 1
  chmod 600 "$LIFE_RUNTIME_ENV" 2>/dev/null
  echo "$LIFE_RUNTIME_ENV"
}
life_load_runtime_env() {
  # set -a 让文件里的赋值全部导出给子进程（引擎）
  if [ -r "$LIFE_RUNTIME_ENV" ]; then
    set -a
    . "$LIFE_RUNTIME_ENV"
    set +a
    return 0
  fi
  return 1
}

# ── 用户意图 ────────────────────────────────────
life_user_stopped() { [ -f "$LIFE_ST_USER_OFF" ]; }
life_wd_disabled() { [ -f "$LIFE_ST_WD_OFF" ]; }
life_wd_armed() { [ -f "$LIFE_ST_WD_ARMED" ]; }
life_wd_alive() { life_pid_alive "$LIFE_ST_WD_PID"; }
life_wd_hold_active() {
  # 维护窗口用**绝对到期时间**：调用方崩了也不会把守护永久卡死
  [ -f "$LIFE_ST_WD_HOLD" ] || return 1
  _exp="$(cat "$LIFE_ST_WD_HOLD" 2>/dev/null | tr -d ' \n')"
  case "$_exp" in ''|*[!0-9]*) rm -f "$LIFE_ST_WD_HOLD"; return 1 ;; esac
  [ "$(date +%s)" -lt "$_exp" ] && return 0
  rm -f "$LIFE_ST_WD_HOLD"
  return 1
}
life_wd_should_supervise() {
  # 守护每轮只问这一个问题：现在该不该插手？
  life_wd_armed || return 1
  life_wd_disabled && return 1
  life_user_stopped && return 1
  life_wd_hold_active && return 1
  return 0
}
life_wd_hold() {
  _sec="${1:-180}"
  case "$_sec" in ''|*[!0-9]*) _sec=180 ;; esac
  echo "$(( $(date +%s) + _sec ))" > "$LIFE_ST_WD_HOLD" 2>/dev/null
}
life_wd_hold_release() { rm -f "$LIFE_ST_WD_HOLD"; }
life_wd_request() { printf '%s\n' "${1:-start}" > "$LIFE_ST_WD_REQ" 2>/dev/null; }
life_wd_take_request() {
  # 取走即删（消费语义）；请求**优先于** hold —— 运维重启必须能立刻生效
  [ -s "$LIFE_ST_WD_REQ" ] || return 0
  _c="$(tr -d ' \n' < "$LIFE_ST_WD_REQ" 2>/dev/null)"
  rm -f "$LIFE_ST_WD_REQ"
  echo "$_c"
}
life_wd_announce() {
  # 守护进程登记自己的 pid（它不该知道状态文件名 —— 由本库代劳）
  echo "$$" > "$LIFE_ST_WD_PID" 2>/dev/null
}
life_wd_retire() {
  # 守护体面退场：清掉自己的 pidfile 与"关闭"标志（标志不该永久留在数据目录）
  rm -f "$LIFE_ST_WD_PID" "$LIFE_ST_WD_OFF"
}
life_shutdown() {
  # 卸载语义：记住"用户要它停" + 关守护 + 停进程（保留数据目录）
  printf 'stop\n' > "$LIFE_ST_USER_OFF"
  printf 'off\n' > "$LIFE_ST_WD_OFF"
  life_stop_all >/dev/null
  rm -f "$LIFE_ST_WD_PID" "$LIFE_ST_WD_HOLD" "$LIFE_ST_WD_REQ"
  echo "shutdown"
}
life_wd_start() {
  # 守护只允许武装后启动：非开机上下文起的守护会随启动者一起被系统清掉，等于没守护
  life_wd_armed || { echo "not-armed"; return 0; }
  life_wd_alive && { echo "running"; return 0; }
  if command -v setsid >/dev/null 2>&1; then
    setsid "$LIFE_WATCHDOG" >>"$LIFE_WD_LOG" 2>&1 &
  else
    "$LIFE_WATCHDOG" >>"$LIFE_WD_LOG" 2>&1 &
  fi
  life_cgroup_escape "$!"
  # setsid 是异步的，pidfile 要下一拍才落盘：等它就位再返回，否则调用方会误判"守护不在"
  # 而退回本地启动 —— 那正好把引擎放回调用者的 cgroup。
  # 轮询语义唯一实现在 lib/wait.sh（30×0.1s ≈ 3s）
  wait_for 30 0.1 life_wd_alive && { echo "started"; return 0; }
  echo "start-failed"
}

# ── 引擎 ────────────────────────────────────────
life_engine_healthy() { life_pid_alive "$LIFE_ST_ENGINE"; }

life_prep() {
  # 引擎启动前必须为真的一切（幂等）：schema 引导、autoUpdate 压回 false、DNS 上游兜底、
  # 初始密码。输出 ok / degraded —— **绝不谎报成功**（schema 没建 = 引擎报 no such table；
  # 自更新闸门开着 = 更新绕过模块管理）。
  _rc=ok
  mkdir -p "$DATA_DIR/db" 2>/dev/null
  if [ -x "$LIFE_SQLITE3" ] && [ -f "$LIFE_SCHEMA" ]; then
    if ! "$LIFE_SQLITE3" "$LIFE_DB" "SELECT 1 FROM settings LIMIT 1;" >/dev/null 2>&1; then
      if "$LIFE_SQLITE3" "$LIFE_DB" < "$LIFE_SCHEMA" 2>>"$LOG_ENGINE_PATH"; then
        life_log "prep: schema bootstrap applied to $LIFE_DB"
      else
        _rc=degraded
        life_log "prep: schema 引导失败（引擎可能报 no such table）"
      fi
    fi
  fi
  if [ -x "$LIFE_SQLITE3" ] && [ -s "$LIFE_DB" ]; then
    _SQL='UPDATE settings SET data = json_set(data, '\''$.autoUpdate'\'', json('\''false'\'')) WHERE json_type(data, '\''$.autoUpdate'\'') IS NOT NULL;'
    if ! "$LIFE_SQLITE3" "$LIFE_DB" "$_SQL" 2>>"$LOG_ENGINE_PATH"; then
      _rc=degraded
      life_log "prep: autoUpdate 压回 false 失败（引擎会按 DB 开启自更新，绕过模块管理）"
    fi
  fi
  if [ ! -s "$LIFE_UPSTREAMS" ]; then
    {
      echo "# 9router-go 生成：公共 DNS 兜底（无优选）"
      echo "nameserver 223.5.5.5"
      echo "nameserver 119.29.29.29"
      echo "nameserver 1.1.1.1"
    } > "$LIFE_UPSTREAMS"
  fi
  if [ ! -f "$DATA_DIR/initial-password" ]; then
    printf '123456\n' > "$DATA_DIR/initial-password"
    chmod 600 "$DATA_DIR/initial-password"
  fi
  echo "$_rc"
}

life_ensure_engine() {
  # 引擎启动的唯一实现（service.sh / 守护 / WebUI 重启 / 更新后重启都走这里）
  life_user_stopped && { echo "off-by-user"; return 0; }
  if life_engine_healthy; then
    life_cgroup_escape "$(life_pid_of "$LIFE_ST_ENGINE")"   # 已在跑的那个也补一次逃逸
    echo "running"; return 0
  fi
  _prep="$(life_prep)"
  [ "$_prep" = "ok" ] || life_log "ensure-engine: prep=$_prep（后果见上方日志）"

  # 承载性 env 从唯一来源加载（C3）：runtime.env 由 life_write_runtime_env 生成，
  # 清单是 life_carrier_env_keys。生成失败才退回内联（不静默降级）。
  if life_write_runtime_env >/dev/null && life_load_runtime_env; then
    :
  else
    life_log "ensure-engine: runtime.env 生成/加载失败，退回内联 env"
    export SSL_CERT_DIR=/system/etc/security/cacerts DATA_DIR MODDIR AUTO_UPDATE=false
    PORT="$(life_get_port)"; export PORT
    export INITIAL_PASSWORD="$(cat "$DATA_DIR/initial-password" 2>/dev/null)"
  fi

  life_log "ensure-engine: port=$PORT caller=${LIFE_CALLER:-shell} cgroup=$(life_cgroup_of self)"
  if command -v setsid >/dev/null 2>&1; then
    setsid "$LIFE_BIN" >>"$LOG_ENGINE_PATH" 2>&1 &
  else
    "$LIFE_BIN" >>"$LOG_ENGINE_PATH" 2>&1 &
  fi
  _p=$!
  echo "$_p" > "$LIFE_ST_ENGINE"
  if life_cgroup_escape "$_p"; then
    life_log "ensure-engine: pid=$_p 已迁出启动者 cgroup"
  else
    life_log "ensure-engine: pid=$_p cgroup 迁移未成功（依赖守护兜底）"
  fi
  echo "started"
}

life_stop_engine() {
  # 先按 pidfile 精确杀（**并校验身份**：pid 可能已被别的进程复用），再按"整条命令行就是
  # 我们的二进制"兜底（pidfile 失联时的孤儿）。用 ^...$ 锚定：裸子串匹配会误杀任何命令行
  # 里含该路径的进程。
  _p="$(life_pid_of "$LIFE_ST_ENGINE")"
  if [ -n "$_p" ] && kill -0 "$_p" 2>/dev/null; then
    if life_pid_is_exe "$_p" "$LIFE_BIN"; then kill "$_p" 2>/dev/null
    else life_log "stop-engine: pidfile 里的 $_p 不是本模块引擎，跳过（防误杀）"; fi
  fi
  for p in $(pgrep -f "^$LIFE_BIN\$" 2>/dev/null); do
    [ "$p" != "$$" ] && [ "$p" != "${PPID:-}" ] && kill "$p" 2>/dev/null
  done
  rm -f "$LIFE_ST_ENGINE"
}

# ── dnsfwd ──────────────────────────────────────
life_dns_disabled() { [ -f "$LIFE_ST_DNS_OFF" ]; }
life_dns_running() { life_pid_alive "$LIFE_ST_DNS"; }
life_dns_healthy() {
  # 谓词的唯一实现：用户关闭 / 在跑 / 已让路（:53 被第三方占用，是正常稳态而非"死了"）
  life_dns_disabled && return 0
  life_dns_running && return 0
  life_port53_busy && return 0
  return 1
}
life_ensure_dns() {
  life_user_stopped && { echo "off-by-user"; return 0; }
  life_dns_disabled && { echo "disabled"; return 0; }
  if life_dns_running; then
    life_cgroup_escape "$(life_pid_of "$LIFE_ST_DNS")"
    echo "running"; return 0
  fi
  life_port53_busy && { echo "yielded"; return 0; }
  # 让位宽限窗口：Magisk 服务早于普通 App 启动，第三方 DNS 服务可能还没起。
  sleep 5
  life_port53_busy && { echo "yielded"; return 0; }
  _BIND="$(life_read_bind)"
  if command -v setsid >/dev/null 2>&1; then
    setsid "$LIFE_DNSFWD" -f "$LIFE_UPSTREAMS" -b "$_BIND" >>"$LIFE_DNS_LOG" 2>&1 &
  else
    "$LIFE_DNSFWD" -f "$LIFE_UPSTREAMS" -b "$_BIND" >>"$LIFE_DNS_LOG" 2>&1 &
  fi
  echo $! > "$LIFE_ST_DNS"
  life_cgroup_escape "$(life_pid_of "$LIFE_ST_DNS")"
  echo "started"
}
life_stop_dns() {
  # 先按 pidfile 精确杀（含身份校验）；再按完整二进制路径兜底（pidfile 失联会留下占
  # 127.0.0.1:53 的孤儿，"关闭"就成了假关闭 —— 真机实测发生过）。
  # 模式必须带 `-b `（只有守护形态带它）：`dnsfwd -P -j 8` 是面板探测进程，共享同一路径。
  _p="$(life_pid_of "$LIFE_ST_DNS")"
  if [ -n "$_p" ] && kill -0 "$_p" 2>/dev/null; then
    if life_pid_is_exe "$_p" "$LIFE_DNSFWD"; then kill "$_p" 2>/dev/null
    else life_log "stop-dns: pidfile 里的 $_p 不是本模块 dnsfwd，跳过（防误杀）"; fi
  fi
  for p in $(pgrep -f "$LIFE_DNSFWD .*-b " 2>/dev/null); do
    [ "$p" != "$$" ] && [ "$p" != "${PPID:-}" ] && kill "$p" 2>/dev/null
  done
  rm -f "$LIFE_ST_DNS"
}
life_disable_dns() { life_stop_dns; printf 'off\n' > "$LIFE_ST_DNS_OFF"; }
life_enable_dns() { rm -f "$LIFE_ST_DNS_OFF"; life_ensure_dns; }
life_reload_dns() {
  # 热重载上游（SIGHUP）：只对"确实在跑的 dnsfwd"发信号（身份校验避免误伤复用 pid）
  _p="$(life_pid_of "$LIFE_ST_DNS")"
  if [ -n "$_p" ] && kill -0 "$_p" 2>/dev/null && life_pid_is_exe "$_p" "$LIFE_DNSFWD" \
     && kill -HUP "$_p" 2>/dev/null; then
    echo "reloaded"
  else
    echo "fail"
  fi
}

# ── 意图编排（调用方的主入口）─────────────────────
life_boot() {
  # 开机 = 新的用户意图：清除"用户停止服务"与"关闭守护"两个标志，武装并启动守护。
  # 只能在 init/ksud 上下文调用（见 ADR-0004）。
  rm -f "$LIFE_ST_USER_OFF" "$LIFE_ST_WD_OFF"
  : > "$LIFE_ST_WD_ARMED"
  life_wd_start >/dev/null
  echo "booted"
}
life_stop_all() {
  # 只停进程并清 pidfile；不触碰 dns-disabled（用户显式开关）。
  # 必须**等到它们真的退出**再返回：引擎收到 SIGTERM 后要 drain SSE、关监听才走
  # （真机见过"sleep 1 不够 → 新实例 bind 失败 → 守护日志写'拉起失败'，下一轮才成"）。
  _ep="$(life_pid_of "$LIFE_ST_ENGINE")"
  _dp="$(life_pid_of "$LIFE_ST_DNS")"
  life_stop_engine
  life_stop_dns
  # 等两个进程真的退出（含僵尸态）：语义唯一实现在 lib/wait.sh
  wait_gone 50 0.2 "$_ep" "$_dp"
  echo "stopped"
}
life_stop_user() {
  # 用户显式停服务：记住意图，守护不再复活（这正是"意图有主"的价值）
  life_stop_all >/dev/null
  printf 'stop\n' > "$LIFE_ST_USER_OFF"
  echo "stopped"
}
life_start_user() {
  rm -f "$LIFE_ST_USER_OFF"
  life_ensure_engine
  life_ensure_dns >/dev/null
  echo "started"
}
life_restart_engine() {
  # 有守护时**委托守护**执行停止+启动：重启后的进程天然落在免疫上下文里；
  # 没守护时才退回本上下文（不理想，但至少能用，并把守护顺手拉回来）。
  rm -f "$LIFE_ST_USER_OFF"
  if ! life_wd_alive && life_wd_armed; then life_wd_start >/dev/null; fi
  life_wd_hold 180
  if life_wd_alive; then
    life_wd_request restart
  else
    life_stop_all >/dev/null
    ( LIFE_CALLER=restart-engine; export LIFE_CALLER; life_ensure_engine ) >/dev/null
  fi
  if wait_for 20 2 life_engine_healthy; then
    life_wd_hold_release
    echo "engine=up"
    return 0
  fi
  life_wd_hold_release
  echo "engine=down"
  return 0
}
life_state() {
  # 只读：机器可读一行（值都不含空格），供 ops.sh status 组合。dnsfwd/引擎/守护三个状态
  # 的判定规则只在这里写一次（此前散在 status / panel / 守护三处，各写一遍）。
  _dns=down; _dns_pid=""
  if life_dns_disabled; then _dns=disabled
  elif life_dns_running; then _dns=up; _dns_pid="$(life_pid_of "$LIFE_ST_DNS")"
  elif life_port53_busy; then _dns=yielded; fi
  _eng=down; _eng_pid=""
  if life_engine_healthy; then _eng=up; _eng_pid="$(life_pid_of "$LIFE_ST_ENGINE")"
  elif life_user_stopped; then _eng=stopped; fi
  _wd=down; _wd_pid=""
  if life_wd_alive; then _wd=up; _wd_pid="$(life_pid_of "$LIFE_ST_WD_PID")"
  elif life_wd_armed; then _wd=stale; fi
  echo "dns=$_dns dns_pid=$_dns_pid engine=$_eng engine_pid=$_eng_pid watchdog=$_wd watchdog_pid=$_wd_pid"
}
