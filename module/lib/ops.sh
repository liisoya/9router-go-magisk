#!/system/bin/sh
# ops.sh — 模块运维唯一实现（seam）
# service.sh / action.sh / WebUI(ksu.exec) 三方共用，消灭重复实现的环境差异分叉。
# 输出约定：机器可读的 key=value 行（WebUI 解析）；部分子命令输出状态词。
#
USAGE="ops.sh <status|panel|stop-all|restart-engine|reload-dns|install-engine <file> [ver]|install-module <zip>|start-dns|stop-dns|enable-dns|port53-busy|seed-key [--force]|get-port>"
# 用法（唯一来源，见 $USAGE；末尾兜底分支复用）:
# 环境变量: DATA_DIR（默认 /data/adb/9router-go）、PORT（显式覆盖端口）

MODDIR="$(cd "$(dirname "$0")/.." && pwd)"
DATA_DIR="${DATA_DIR:-/data/adb/9router-go}"
DB_FILE="$DATA_DIR/db/data.sqlite"
SQLITE3="$MODDIR/bin/sqlite3"
DNSFWD="$MODDIR/bin/dnsfwd"
UPSTREAMS="$DATA_DIR/dns-upstreams.conf"
DNS_BIND_FILE="$DATA_DIR/dns-bind"
DNS_PIDFILE="$DATA_DIR/dnsfwd.pid"
ENG_PIDFILE="$DATA_DIR/9router.pid"
DNS_DISABLED="$DATA_DIR/dns-disabled"
FACTORY_KEY="sk-8b71f86e0a1f2fb5-nhz496-cfa1c800"
FACTORY_KEY_ID="seed-default-client-key"

# ── 内部原语（私有）─────────────────────────────
pid_alive() { [ -f "$1" ] && kill -0 "$(cat "$1" 2>/dev/null)" 2>/dev/null; }
read_bind() {
  # dns-bind 归一化唯一实现（曾在 status 与 start-dns 各写一份）
  _b="$(cat "$DNS_BIND_FILE" 2>/dev/null | tr -d ' \n')"
  [ "$_b" = "any" ] && echo any || echo loopback
}
port53_busy() {
  # 优先 ss（Android netstat 对 UDP 监听展示不可靠），netstat 兜底
  if command -v ss >/dev/null 2>&1; then
    ss -tuln 2>/dev/null | grep -qE '[:.]53[[:space:]]'
  else
    netstat -tuln 2>/dev/null | grep -qE '[:.]53[[:space:]]'
  fi
}
module_version() { grep -E '^version=' "$MODDIR/module.prop" 2>/dev/null | cut -d= -f2; }
module_versioncode() { grep -E '^versionCode=' "$MODDIR/module.prop" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]'; }
engine_version() {
  # 真实引擎版本的唯一来源（绝不拿模块版本冒充）：
  #   1. $DATA_DIR/engine-version —— install-engine 运行期更新时写入
  #   2. 包内 etc/engine-version —— 构建期写入（首次安装/模块更新携带）
  if [ -s "$DATA_DIR/engine-version" ]; then cat "$DATA_DIR/engine-version"; return; fi
  [ -s "$MODDIR/etc/engine-version" ] && cat "$MODDIR/etc/engine-version"
}
lan_ips() {
  # 全局作用域 IPv4（排除 127.*），'|' 连接（panel 值不含空格），最多 3 个
  ip -4 addr show scope global 2>/dev/null \
    | sed -n 's/.*inet \([0-9.]*\).*/\1/p' | head -n 3 | tr '\n' '|' | sed 's/|$//'
}
get_port() {
  p=""
  if [ -z "${PORT:-}" ] && [ -f "$DATA_DIR/port" ]; then
    p="$(cat "$DATA_DIR/port" 2>/dev/null | tr -d ' \n')"
    case "$p" in ''|*[!0-9]*) p= ;; esac
    if [ -n "$p" ] && [ "$p" -ge 1 ] && [ "$p" -le 65535 ]; then PORT="$p"; fi
  fi
  echo "${PORT:-20130}"
}

# ── 子命令 ────────────────────────────────────
cmd_status() {
  # 单行输出（空格分隔 key=value）：兼容 WebUI 的 promise 降级形态
  # （该形态多行输出只剩末行）。值均不含空格。
  _bind="$(read_bind)"
  _dns=down; _dns_pid=""
  if [ -f "$DNS_DISABLED" ]; then _dns=disabled
  elif pid_alive "$DNS_PIDFILE"; then _dns=up; _dns_pid="$(cat "$DNS_PIDFILE")"
  elif port53_busy; then _dns=yielded; fi
  _eng=down; _eng_pid=""
  if pid_alive "$ENG_PIDFILE"; then _eng=up; _eng_pid="$(cat "$ENG_PIDFILE")"; fi
  # 两个 COUNT 合并进一次 sqlite3 调用（此前 spawn 两次，WebUI 每次 status 多花一倍时间）
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
  echo "port=$(get_port) bind=$_bind module_version=$(module_version) versioncode=$(module_versioncode) engine_version=$(engine_version) lan_ip=$(lan_ips) dns=$_dns dns_pid=$_dns_pid engine=$_eng engine_pid=$_eng_pid factory_key=$_fk apikeys_total=$_kt"
}

cmd_panel() {
  # 概览页单命令数据源（WebUI 一次 ksu.exec 拿全，替代原先 9 次串行 shell）：
  # status 全量 + meminfo + 引擎/dnsfwd RSS + upstreams(base64 单行) + mod_url + accel_sel。
  # base64 保证多行 upstreams 不含空白，promise 降级形态（只剩末行）下也完整。
  _s="$(cmd_status)"
  _mem="$(awk '/^MemTotal:/{mt=$2}/^MemAvailable:/{ma=$2}END{print mt+0, ma+0}' /proc/meminfo 2>/dev/null)"
  _mem="${_mem:-0 0}"
  _ep="$(cat "$ENG_PIDFILE" 2>/dev/null)"
  _dp="$(cat "$DNS_PIDFILE" 2>/dev/null)"
  _er="$(awk '/^VmRSS:/{print $2+0; exit}' "/proc/$_ep/status" 2>/dev/null)"; _er="${_er:-0}"
  _dr="$(awk '/^VmRSS:/{print $2+0; exit}' "/proc/$_dp/status" 2>/dev/null)"; _dr="${_dr:-0}"
  _ub="$(base64 "$UPSTREAMS" 2>/dev/null | tr -d '\n')"
  _mu="$(cat "$DATA_DIR/module-update-url" 2>/dev/null)"
  _as="$(cat "$DATA_DIR/github-accel" 2>/dev/null)"
  echo "$_s mem_total=${_mem%% *} mem_avail=${_mem##* } engine_rss=$_er dns_rss=$_dr upstreams_b64=$_ub mod_url=$_mu accel_sel=$_as"
}

cmd_start_dns() {
  # 用户关闭开关（dns-disabled）：尊重，不自动清除（开机与手动拉起都走这里）
  if [ -f "$DNS_DISABLED" ]; then echo "disabled"; exit 0; fi
  pid_alive "$DNS_PIDFILE" && { echo "running"; exit 0; }
  # 127.0.0.1:53 只能有一个所有者；被占即自动让路（引擎解析由已有服务接管）
  if port53_busy; then echo "yielded"; exit 0; fi
  # 让位宽限窗口：Magisk 服务早于普通 App 启动，第三方 DNS 服务可能还没起。
  # 空闲时等 5s 复查一次，避免抢跑占死 :53（第二次仍空闲才绑定；残余竞态见 MAGISK.md）
  sleep 5
  if port53_busy; then echo "yielded"; exit 0; fi
  _BIND="$(read_bind)"
  if command -v setsid >/dev/null 2>&1; then
    setsid "$DNSFWD" -f "$UPSTREAMS" -b "$_BIND" >>"$DATA_DIR/dnsfwd.log" 2>&1 &
  else
    "$DNSFWD" -f "$UPSTREAMS" -b "$_BIND" >>"$DATA_DIR/dnsfwd.log" 2>&1 &
  fi
  echo $! > "$DNS_PIDFILE"
  echo "started"
}

kill_our_dnsfwd() {
  # 先按 pidfile 精确杀；再按完整二进制路径兜底（pgrep -f 只匹配我们自己的
  # dnsfwd，绝不误杀设备上第三方 DNS 服务）——pidfile 失联时会留下孤儿进程
  # 继续占着 127.0.0.1:53，"关闭"就成了假关闭（真机实测发生过）
  [ -f "$DNS_PIDFILE" ] && kill "$(cat "$DNS_PIDFILE" 2>/dev/null)" 2>/dev/null
  for p in $(pgrep -f "$MODDIR/bin/dnsfwd" 2>/dev/null); do
    [ "$p" != "$$" ] && kill "$p" 2>/dev/null
  done
  rm -f "$DNS_PIDFILE"
}

cmd_stop_dns() {
  kill_our_dnsfwd
  printf 'off\n' > "$DATA_DIR/dns-disabled"
  echo "stopped"
}

cmd_enable_dns() {
  rm -f "$DNS_DISABLED"
  cmd_start_dns
}

cmd_stop_all() {
  # 仅停进程并清 pidfile；不触碰 dns-disabled（那是用户的显式开关，重启引擎不得篡改）
  [ -f "$ENG_PIDFILE" ] && kill "$(cat "$ENG_PIDFILE" 2>/dev/null)" 2>/dev/null
  # 引擎同样按完整二进制路径兜底，防 pidfile 失联留下孤儿
  for p in $(pgrep -f "$MODDIR/bin/9router-go" 2>/dev/null); do
    [ "$p" != "$$" ] && kill "$p" 2>/dev/null
  done
  kill_our_dnsfwd
  sleep 1  # 等进程退出，调用方（如更新覆盖二进制）才能安全操作文件
  echo "stopped"
}

cmd_restart_engine() {
  # 生命周期唯一入口（deletion test：app.js 4 处内联 kill→rm→sh service.sh 全部删除）：
  # 停止 → 复用 service.sh（唯一启动脚本，自带幂等守卫）→ 内置等待，调用即知结果。
  # WebUI 与 action.sh（管理器「操作」按钮）共用同一 interface。
  cmd_stop_all >/dev/null
  sh "$MODDIR/service.sh"
  i=0
  while [ $i -lt 10 ]; do
    sleep 2
    if pid_alive "$ENG_PIDFILE"; then echo "engine=up"; return 0; fi
    i=$((i + 1))
  done
  echo "engine=down"
  return 0
}

cmd_reload_dns() {
  if pid_alive "$DNS_PIDFILE" && kill -HUP "$(cat "$DNS_PIDFILE")" 2>/dev/null; then
    echo "reloaded"
  else
    echo "fail"
  fi
}

cmd_install_engine() {
  # 引擎二进制安装唯一入口：备份 → 替换 → 重启。src 为已下载到本地的临时文件；
  # 第二参数为引擎版本（可选），写入 $DATA_DIR/engine-version 供 engine_version() 读取
  [ -f "${1:-}" ] || { echo "no-src"; return 0; }
  cmd_stop_all >/dev/null
  cp "$MODDIR/bin/9router-go" "$MODDIR/bin/9router-go.bak" 2>/dev/null
  if mv "$1" "$MODDIR/bin/9router-go" && chmod 0755 "$MODDIR/bin/9router-go"; then
    [ -n "${2:-}" ] && printf '%s\n' "$2" > "$DATA_DIR/engine-version"
    cmd_restart_engine
  else
    echo "install-failed"
  fi
}

cmd_install_module() {
  # 模块 zip 安装唯一入口：备份 → 解压覆盖 → 权限兜底（含 lib/，勿漏）→ 清理 → 重启
  [ -f "${1:-}" ] || { echo "no-src"; return 0; }
  cmd_stop_all >/dev/null
  cp "$1" "$DATA_DIR/last-module.zip" 2>/dev/null
  if (cd "$MODDIR" && unzip -oq "$1") && chmod 0755 "$MODDIR"/*.sh "$MODDIR"/lib/*.sh "$MODDIR"/bin/* 2>/dev/null; then
    rm -f "$1"
    cmd_restart_engine
  else
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
    if "$SQLITE3" "$DB_FILE" "INSERT OR IGNORE INTO apiKeys (id, key, name, isActive, createdAt) VALUES ('$FACTORY_KEY_ID', '$FACTORY_KEY', 'Default client key (dashboard)', 1, datetime('now'));" 2>>"$DATA_DIR/9router.log"; then
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
  stop-all)        cmd_stop_all ;;
  restart-engine)  cmd_restart_engine ;;
  reload-dns)      cmd_reload_dns ;;
  install-engine)  shift; cmd_install_engine "$@" ;;
  install-module)  shift; cmd_install_module "$@" ;;
  start-dns)       cmd_start_dns ;;
  stop-dns)        cmd_stop_dns ;;
  enable-dns)      cmd_enable_dns ;;
  port53-busy)     if port53_busy; then echo 1; else echo 0; fi ;;
  seed-key)        shift; cmd_seed_key "$@" ;;
  get-port)        get_port ;;
  *)               echo "usage: $USAGE"; exit 1 ;;
esac
