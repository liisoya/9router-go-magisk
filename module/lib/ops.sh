#!/system/bin/sh
# ops.sh — 配置与运维编排（service.sh / action.sh / WebUI(ksu.exec) 的 seam）
#
# 职责边界（ADR-0005，「一切以模块实际源码为准」的分工声明）：
#   本文件   = 配置与数据（端口读写、库计数、出厂 key、引擎/模块安装编排）+ 状态聚合 + 对外子命令；
#   生命周期 = lib/lifecycle.sh（进程启停、用户意图、状态文件、cgroup 脱组）。本文件只 source 它，
#              **绝不自己读写任何 lifecycle 状态文件**（否则"状态无主"的老毛病会立刻回来）。
# 输出约定：机器可读的 key=value 行（WebUI 解析）；部分子命令输出状态词。
USAGE="ops.sh <status|panel|prep-db|start-engine|stop-engine|stop-all|stop-user|start-user|restart-engine|reload-dns|watchdog-start|hold [sec]|install-engine <file> [ver]|install-module <zip>|start-dns|stop-dns|enable-dns|port53-busy|seed-key [--force]|get-port>"
# 环境变量: DATA_DIR（默认 /data/adb/9router-go）、PORT（显式覆盖端口）

MODDIR="$(cd "$(dirname "$0")/.." && pwd)"
DATA_DIR="${DATA_DIR:-/data/adb/9router-go}"
DB_FILE="$DATA_DIR/db/data.sqlite"
SQLITE3="$MODDIR/bin/sqlite3"
FACTORY_KEY="sk-8b71f86e0a1f2fb5-nhz496-cfa1c800"
FACTORY_KEY_ID="seed-default-client-key"

LIFECYCLE="$MODDIR/lib/lifecycle.sh"
if [ ! -r "$LIFECYCLE" ]; then
  echo "ops.sh: 缺 lib/lifecycle.sh（生命周期唯一所有者）；模块不完整，拒绝猜测" >&2
  exit 1
fi
. "$LIFECYCLE"

# ── 本文件私有的原语（配置/元数据，与进程生命周期无关）────────────
module_version() { grep -E '^version=' "$MODDIR/module.prop" 2>/dev/null | cut -d= -f2; }
module_versioncode() { grep -E '^versionCode=' "$MODDIR/module.prop" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]'; }
engine_version() {
  # 真实引擎版本的唯一来源（绝不拿模块版本冒充）：
  #   1) $DATA_DIR/engine-version —— install-engine 运行期更新时写入
  #   2) 包内 etc/engine-version —— 构建期写入（首次安装/模块更新携带）
  if [ -s "$DATA_DIR/engine-version" ]; then cat "$DATA_DIR/engine-version"; return; fi
  [ -s "$MODDIR/etc/engine-version" ] && cat "$MODDIR/etc/engine-version"
}
lan_ips() {
  # 全局作用域 IPv4（排除 127.*），'|' 连接（panel 值不含空格），最多 3 个
  ip -4 addr show scope global 2>/dev/null \
    | sed -n 's/.*inet \([0-9.]*\).*/\1/p' | head -n 3 | tr '\n' '|' | sed 's/|$//'
}

# ── 子命令 ────────────────────────────────────
cmd_status() {
  # 单行输出（空格分隔 key=value）：兼容 WebUI 的 promise 降级形态
  # （该形态多行输出只剩末行）。值均不含空格。
  # 进程三态（dns/engine/watchdog）由 life_state 提供 —— 判定规则只有那一处。
  _cnts="$("$SQLITE3" "$DB_FILE" "SELECT (SELECT COUNT(*) FROM apiKeys WHERE key='$FACTORY_KEY') || '|' || (SELECT COUNT(*) FROM apiKeys);" 2>/dev/null | tr -d '[:space:]')"
  case "$_cnts" in
    # 读失败（引擎写事务锁住 / 表缺失）：显式 err，绝不冒充 0 —— 上层凭 0 会误判
    # 全新安装而自动写库（孤儿扫描同款护栏：读不到 = 不可信）
    ''|*[!0-9|]*) _fk=err; _kt=err ;;
    # mksh 的参数展开模式里 | 是"或"运算符（%%|* 会删空全部），必须转义 \| 才是字面量
    *) _fk="${_cnts%%\|*}"; _kt="${_cnts##*\|}" ;;
  esac
  # engine_version 来自真实来源（engine_version()），versioncode 来自 module.prop
  # 字段——此前从 "v1.9.1-r1" 正则提取 r1 当 versionCode 与远端 109010 比较，
  # 导致"没发新版却永远提示有更新"
  echo "port=$(life_get_port) bind=$(life_read_bind) module_version=$(module_version) versioncode=$(module_versioncode) engine_version=$(engine_version) lan_ip=$(lan_ips) $(life_state) factory_key=$_fk apikeys_total=$_kt"
}

cmd_panel() {
  # 概览页单命令数据源（WebUI 一次 ksu.exec 拿全，替代原先 9 次串行 shell）：
  # status 全量 + meminfo + 引擎/dnsfwd RSS + upstreams(base64 单行) + mod_url + accel_sel。
  # base64 保证多行 upstreams 不含空白，promise 降级形态（只剩末行）下也完整。
  _s="$(cmd_status)"
  _mem="$(awk '/^MemTotal:/{mt=$2}/^MemAvailable:/{ma=$2}END{print mt+0, ma+0}' /proc/meminfo 2>/dev/null)"
  _mem="${_mem:-0 0}"
  _ep="$(life_pid_of "$LIFE_ST_ENGINE")"
  _dp="$(life_pid_of "$LIFE_ST_DNS")"
  _er="$(awk '/^VmRSS:/{print $2+0; exit}' "/proc/$_ep/status" 2>/dev/null)"; _er="${_er:-0}"
  _dr="$(awk '/^VmRSS:/{print $2+0; exit}' "/proc/$_dp/status" 2>/dev/null)"; _dr="${_dr:-0}"
  _ub="$(base64 "$DATA_DIR/dns-upstreams.conf" 2>/dev/null | tr -d '\n')"
  _mu="$(cat "$DATA_DIR/module-update-url" 2>/dev/null)"
  _as="$(cat "$DATA_DIR/github-accel" 2>/dev/null)"
  echo "$_s mem_total=${_mem%% *} mem_avail=${_mem##* } engine_rss=$_er dns_rss=$_dr upstreams_b64=$_ub mod_url=$_mu accel_sel=$_as"
}

cmd_install_engine() {
  # 引擎二进制安装唯一入口：备份 → 替换 → 重启。src 为已下载到本地的临时文件；
  # 第二参数为引擎版本（可选），写入 $DATA_DIR/engine-version 供 engine_version() 读取。
  # 全程 hold 住守护：换文件的窗口里它去拉起旧/半份二进制会造成"text file busy"或假启动。
  [ -f "${1:-}" ] || { echo "no-src"; return 0; }
  life_wd_hold 300
  life_stop_all >/dev/null
  cp "$MODDIR/bin/9router-go" "$MODDIR/bin/9router-go.bak" 2>/dev/null
  if mv "$1" "$MODDIR/bin/9router-go" && chmod 0755 "$MODDIR/bin/9router-go"; then
    [ -n "${2:-}" ] && printf '%s\n' "$2" > "$DATA_DIR/engine-version"
    life_restart_engine
  else
    life_wd_hold_release
    echo "install-failed"
  fi
}

cmd_install_module() {
  # 模块 zip 安装唯一入口：备份 → 解压覆盖 → 权限兜底（含 lib/，勿漏）→ 清理 → 重启
  [ -f "${1:-}" ] || { echo "no-src"; return 0; }
  life_wd_hold 300
  life_stop_all >/dev/null
  cp "$1" "$DATA_DIR/last-module.zip" 2>/dev/null
  if (cd "$MODDIR" && unzip -oq "$1") && chmod 0755 "$MODDIR"/*.sh "$MODDIR"/lib/*.sh "$MODDIR"/bin/* 2>/dev/null; then
    rm -f "$1"
    life_restart_engine
  else
    life_wd_hold_release
    echo "install-failed"
  fi
}

cmd_seed_key() {
  [ -x "$SQLITE3" ] && [ -s "$DB_FILE" ] || { echo "no-db"; exit 0; }
  _has="$("$SQLITE3" "$DB_FILE" "SELECT COUNT(*) FROM apiKeys WHERE key='$FACTORY_KEY';" 2>/dev/null | tr -d '[:space:]')"
  # POSIX 否定必须是 [!...]：mksh/dash 里 [^0-9] 的 ^ 是字面量（类= ^+数字），
  # 会把 "0"（key 不存在）误判为读取失败 → 永远 error（真机实锤，勿改回 [^...]）
  case "$_has" in ''|*[!0-9]*) echo "error"; exit 0 ;; esac  # 读失败不得谎报 present
  if [ "$_has" != "0" ] && [ "${1:-}" != "--force" ]; then echo "present"; exit 0; fi
  _cnt="$("$SQLITE3" "$DB_FILE" "SELECT COUNT(*) FROM apiKeys;" 2>/dev/null | tr -d '[:space:]')"
  case "$_cnt" in ''|*[!0-9]*) echo "error"; exit 0 ;; esac  # 读失败不得谎报 user-deleted
  if [ "$_cnt" = "0" ] || [ "${1:-}" = "--force" ]; then
    # INSERT 成功与否要如实上报，不得无条件谎报 seeded
    if "$SQLITE3" "$DB_FILE" "INSERT OR IGNORE INTO apiKeys (id, key, name, isActive, createdAt) VALUES ('$FACTORY_KEY_ID', '$FACTORY_KEY', 'Default client key (dashboard)', 1, datetime('now'));" 2>>"$LOG_ENGINE_PATH"; then
      echo "seeded"
    else
      echo "error"
    fi
  else
    # 表非空但 key 缺失 = 用户有意删除，不自动加回
    echo "user-deleted"
  fi
}

case "${1:-}" in
  status)          cmd_status ;;
  panel)           cmd_panel ;;
  prep-db)         life_prep ;;
  start-engine)    life_ensure_engine ;;
  stop-engine)     life_stop_engine; echo "stopped" ;;
  stop-all)        life_stop_all ;;
  stop-user)       life_stop_user ;;
  start-user)      life_start_user ;;
  restart-engine)  life_restart_engine ;;
  reload-dns)      life_reload_dns ;;
  watchdog-start)  life_wd_start ;;
  hold)            shift; life_wd_hold "${1:-180}"; echo "held" ;;
  install-engine)  shift; cmd_install_engine "$@" ;;
  install-module)  shift; cmd_install_module "$@" ;;
  start-dns)       life_ensure_dns ;;
  stop-dns)        life_disable_dns; echo "stopped" ;;
  enable-dns)      life_enable_dns ;;
  port53-busy)     if life_port53_busy; then echo 1; else echo 0; fi ;;
  seed-key)        shift; cmd_seed_key "$@" ;;
  get-port)        life_get_port ;;
  *)               echo "usage: $USAGE"; exit 1 ;;
esac
