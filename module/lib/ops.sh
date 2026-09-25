#!/system/bin/sh
# ops.sh — 模块运维唯一实现（seam）
# service.sh / action.sh / WebUI(ksu.exec) 三方共用，消灭重复实现的环境差异分叉。
# 输出约定：机器可读的 key=value 行（WebUI 解析）；部分子命令输出状态词。
#
# 用法: ops.sh <status|start-dns|stop-dns|enable-dns|port53-busy|seed-key [--force]|get-port>
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
port53_busy() {
  # 优先 ss（Android netstat 对 UDP 监听展示不可靠），netstat 兜底
  if command -v ss >/dev/null 2>&1; then
    ss -tuln 2>/dev/null | grep -qE '[:.]53[[:space:]]'
  else
    netstat -tuln 2>/dev/null | grep -qE '[:.]53[[:space:]]'
  fi
}
module_version() { grep -E '^version=' "$MODDIR/module.prop" 2>/dev/null | cut -d= -f2; }
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
  echo "port=$(get_port)"
  echo "bind=$(cat "$DNS_BIND_FILE" 2>/dev/null | tr -d ' \n')"
  echo "module_version=$(module_version)"
  echo "engine_version=$(module_version | sed 's/-r[0-9]*$//')"
  if [ -f "$DNS_DISABLED" ]; then
    echo "dns=disabled"
  elif pid_alive "$DNS_PIDFILE"; then
    echo "dns=up"; echo "dns_pid=$(cat "$DNS_PIDFILE")"
  elif port53_busy; then
    echo "dns=yielded"
  else
    echo "dns=down"
  fi
  if pid_alive "$ENG_PIDFILE"; then
    echo "engine=up"; echo "engine_pid=$(cat "$ENG_PIDFILE")"
  else
    echo "engine=down"
  fi
  echo "factory_key=$("$SQLITE3" "$DB_FILE" "SELECT COUNT(*) FROM apiKeys WHERE key='$FACTORY_KEY';" 2>/dev/null | tr -d '[:space:]')"
  echo "apikeys_total=$("$SQLITE3" "$DB_FILE" "SELECT COUNT(*) FROM apiKeys;" 2>/dev/null | tr -d '[:space:]')"
}

cmd_start_dns() {
  # 用户关闭开关（dns-disabled）：尊重，不自动清除（开机与手动拉起都走这里）
  if [ -f "$DNS_DISABLED" ]; then echo "disabled"; exit 0; fi
  pid_alive "$DNS_PIDFILE" && { echo "running"; exit 0; }
  # 127.0.0.1:53 只能有一个所有者；被占即自动让路（引擎改用设备已有 DNS 方案）
  if port53_busy; then echo "yielded"; exit 0; fi
  _BIND="$(cat "$DNS_BIND_FILE" 2>/dev/null | tr -d ' \n')"
  [ "$_BIND" = "any" ] || _BIND=loopback
  if command -v setsid >/dev/null 2>&1; then
    setsid "$DNSFWD" -f "$UPSTREAMS" -b "$_BIND" >>"$DATA_DIR/dnsfwd.log" 2>&1 &
  else
    "$DNSFWD" -f "$UPSTREAMS" -b "$_BIND" >>"$DATA_DIR/dnsfwd.log" 2>&1 &
  fi
  echo $! > "$DNS_PIDFILE"
  echo "started"
}

cmd_stop_dns() {
  [ -f "$DNS_PIDFILE" ] && kill "$(cat "$DNS_PIDFILE")" 2>/dev/null
  rm -f "$DNS_PIDFILE"
  printf 'off\n' > "$DATA_DIR/dns-disabled"
  echo "stopped"
}

cmd_enable_dns() {
  rm -f "$DNS_DISABLED"
  cmd_start_dns
}

cmd_seed_key() {
  [ -x "$SQLITE3" ] && [ -s "$DB_FILE" ] || { echo "no-db"; exit 0; }
  _has="$("$SQLITE3" "$DB_FILE" "SELECT COUNT(*) FROM apiKeys WHERE key='$FACTORY_KEY';" 2>/dev/null | tr -d '[:space:]')"
  if [ "$_has" != "0" ] && [ "${1:-}" != "--force" ]; then echo "present"; exit 0; fi
  _cnt="$("$SQLITE3" "$DB_FILE" "SELECT COUNT(*) FROM apiKeys;" 2>/dev/null | tr -d '[:space:]')"
  if [ "$_cnt" = "0" ] || [ "${1:-}" = "--force" ]; then
    "$SQLITE3" "$DB_FILE" "INSERT OR IGNORE INTO apiKeys (id, key, name, isActive, createdAt) VALUES ('$FACTORY_KEY_ID', '$FACTORY_KEY', 'Default client key (dashboard)', 1, datetime('now'));" 2>>"$DATA_DIR/9router.log"
    echo "seeded"
  else
    # 表非空但 key 缺失 = 用户有意删除，不自动加回
    echo "user-deleted"
  fi
}

case "${1:-}" in
  status)       cmd_status ;;
  start-dns)    cmd_start_dns ;;
  stop-dns)     cmd_stop_dns ;;
  enable-dns)   cmd_enable_dns ;;
  port53-busy)  if port53_busy; then echo 1; else echo 0; fi ;;
  seed-key)     shift; cmd_seed_key "$@" ;;
  get-port)     get_port ;;
  *)            echo "usage: ops.sh <status|start-dns|stop-dns|enable-dns|port53-busy|seed-key [--force]|get-port>"; exit 1 ;;
esac
