#!/system/bin/sh
# 9router-go · late_start service
# 职责：准备运行环境 → 拉起本地 DNS 转发器 → 拉起引擎（Dashboard 由引擎直接服务）
# 注意：不阻塞（Magisk/KernelSU 对 service 阶段有超时）
#
# 两个承载性组件，删了引擎就不能用：
#   1) dnsfwd：引擎是纯 Go 静态二进制，读不到 /etc/resolv.conf 时会回落到
#      127.0.0.1:53（真机实测该文件根本不存在），必须有本地转发器接住；
#   2) SSL_CERT_DIR：没有它所有 HTTPS 与更新检查都会失败。
#
# 与旧 panel-9router 模块的区别：无独立面板进程。Dashboard（Svelte SPA）
# 由引擎通过 go:embed 直接服务在引擎端口上；DNS 管理走模块 WebUI（webroot/）。

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

# 端口：$DATA_DIR/port 是持久值（WebUI 可写），读 + 严格校验；环境变量 PORT 优先。
# 默认与上游一致（20130），避免每次上游更新都要改配置。
if [ -z "${PORT:-}" ] && [ -f "$DATA_DIR/port" ]; then
  _p="$(cat "$DATA_DIR/port" 2>/dev/null | tr -d ' \n')"
  case "$_p" in
    ''|*[!0-9]*) _p= ;;
  esac
  if [ -n "$_p" ] && [ "$_p" -ge 1 ] && [ "$_p" -le 65535 ]; then
    PORT="$_p"
  fi
fi
PORT="${PORT:-20130}"

BIN="$MODDIR/bin/9router-go"
DNSFWD="$MODDIR/bin/dnsfwd"
UPSTREAMS="$DATA_DIR/dns-upstreams.conf"
DNS_BIND_FILE="$DATA_DIR/dns-bind"
LOG="$DATA_DIR/9router.log"
DNS_LOG="$DATA_DIR/dnsfwd.log"
PIDFILE="$DATA_DIR/9router.pid"
DNS_PIDFILE="$DATA_DIR/dnsfwd.pid"

mkdir -p "$DATA_DIR/db"

# --- 数据库 schema 引导 ---
# Go 引擎无迁移系统（schema 历来由 Node 版官方程序创建），全新安装的空库缺业务表，
# Dashboard 会报 "no such table: settings / apiKeys"。这里用模块内置 schema.sql
# 幂等建表（全部 IF NOT EXISTS，每次启动执行也无副作用，还能自愈半初始化的库）。
SQLITE3="$MODDIR/bin/sqlite3"
SCHEMA_SQL="$MODDIR/etc/schema.sql"
DB_FILE="$DATA_DIR/db/data.sqlite"
if [ -x "$SQLITE3" ] && [ -f "$SCHEMA_SQL" ]; then
  if ! "$SQLITE3" "$DB_FILE" "SELECT 1 FROM settings LIMIT 1;" >/dev/null 2>&1; then
    "$SQLITE3" "$DB_FILE" < "$SCHEMA_SQL" 2>>"$LOG" \
      && echo "[$(date)] schema bootstrap: applied to $DB_FILE" >>"$LOG"
  fi
fi

# --- 阻断引擎自更新（模块更新一律走 zip）---
# 引擎在 AUTO_UPDATE env 为 false 时会回读 DB 的 settings.AutoUpdate（server.go OnStart），
# 导入含 autoUpdate:true 的备份会重新触发自更新、绕过模块管理。
# 每次启动把该键压回 false（json_set 幂等；键不存在时不新增）。
# 注意：引擎/模块二进制以 root 运行，json1 函数在自带 sqlite3 上已验证可用。
if [ -x "$SQLITE3" ] && [ -s "$DB_FILE" ]; then
  _AUTOUPD_SQL='UPDATE settings SET data = json_set(data, '\''$.autoUpdate'\'', json('\''false'\'')) WHERE json_type(data, '\''$.autoUpdate'\'') IS NOT NULL;'
  "$SQLITE3" "$DB_FILE" "$_AUTOUPD_SQL" 2>>"$LOG"
fi

# --- Dashboard 出厂客户端 key（仅 apiKeys 表为空时补入）---
# 前端 getAuthHeaders() 在浏览器无 localStorage['9router_key'] 时回退到出厂 key
# sk-8b71f86e...（web/src/api/client.ts 硬编码）。该 key 由 Node 版官方 DB 模板
# 出厂自带；Go 版无 seed，全新安装缺它 → 所有走 RequireApiKey 的 dashboard
# 调用（模型测试、SSE 控制台等）报 "Invalid API key"。
# 只在表为空（全新安装）时补入：用户在 Dashboard 删除该 key 后不会被加回
# （表非空但 key 缺失 = 用户有意删除，尊重之；备份导入清空表的场景由
# 模块 WebUI 一致性页提供手动补入按钮）。
if [ -x "$SQLITE3" ] && [ -s "$DB_FILE" ]; then
  _keycount="$("$SQLITE3" "$DB_FILE" "SELECT COUNT(*) FROM apiKeys;" 2>/dev/null | tr -d '[:space:]')"
  if [ "$_keycount" = "0" ]; then
    "$SQLITE3" "$DB_FILE" "INSERT OR IGNORE INTO apiKeys (id, key, name, isActive, createdAt) VALUES ('seed-default-client-key', 'sk-8b71f86e0a1f2fb5-nhz496-cfa1c800', 'Default client key (dashboard)', 1, datetime('now'));" 2>>"$LOG"
    echo "[$(date)] seeded factory client key (empty apiKeys table)" >>"$LOG"
  fi
fi

# --- 承载性 2/2：CA 目录 ---
export SSL_CERT_DIR=/system/etc/security/cacerts
export DATA_DIR PORT

# 引擎自动更新关闭：更新一律走模块 zip 刷入，避免二进制自更新绕过模块管理
export AUTO_UPDATE=false

# --- 首次启动生成随机管理密码（Dashboard 登录用）---
# 引擎约定：INITIAL_PASSWORD 为空则要求显式设置；无头模块场景在首启生成并落盘。
if [ ! -f "$DATA_DIR/initial-password" ]; then
  _pw="$(head -c 16 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | cut -c1-16)"
  [ -n "$_pw" ] || _pw="9router-$(date +%s)"
  printf '%s\n' "$_pw" > "$DATA_DIR/initial-password"
  chmod 600 "$DATA_DIR/initial-password"
fi
export INITIAL_PASSWORD="$(cat "$DATA_DIR/initial-password" 2>/dev/null)"

# --- DNS 上游文件（首次生成公共兜底；严禁出现 127.0.0.1，否则转发器自我循环）---
if [ ! -s "$UPSTREAMS" ]; then
  {
    echo "# 9router-go 生成：公共 DNS 兜底（无优选）"
    echo "nameserver 223.5.5.5"
    echo "nameserver 119.29.29.29"
    echo "nameserver 1.1.1.1"
  } > "$UPSTREAMS"
fi

dns_running() {
  [ -f "$DNS_PIDFILE" ] || return 1
  _p="$(cat "$DNS_PIDFILE" 2>/dev/null)"
  [ -n "$_p" ] || return 1
  kill -0 "$_p" 2>/dev/null
}

engine_running() {
  [ -f "$PIDFILE" ] || return 1
  _p="$(cat "$PIDFILE" 2>/dev/null)"
  [ -n "$_p" ] || return 1
  kill -0 "$_p" 2>/dev/null
}

start_dnsfwd() {
  [ -x "$DNSFWD" ] || return 1
  # 用户关闭开关（$DATA_DIR/dns-disabled）：设备上可能已有其他转发器，
  # 用户手动关闭本模块的 dnsfwd 以避免冲突。开机与手动拉起都尊重该开关。
  if [ -f "$DATA_DIR/dns-disabled" ]; then
    echo "[$(date)] dnsfwd disabled by user flag, skip start" >>"$DNS_LOG"
    return 1
  fi
  dns_running && return 0
  # 127.0.0.1:53 只能有一个所有者（如旧模块共存期已占用则跳过，引擎照常可用 :53）。
  # 优先 ss（Android netstat 对 UDP 监听展示不可靠），netstat 兜底。
  if command -v ss >/dev/null 2>&1; then
    _BUSY="ss -tuln"
  else
    _BUSY="netstat -tuln"
  fi
  if $_BUSY 2>/dev/null | grep -qE '[:.]53[[:space:]]'; then
    echo "[$(date)] :53 已被占用（可能是其他转发器），跳过启动 dnsfwd" >>"$DNS_LOG"
    return 1
  fi
  # 绑定范围：dns-bind 文件写 any = 开放局域网；默认 loopback（安全值）
  _BIND="$(cat "$DNS_BIND_FILE" 2>/dev/null | tr -d ' \n')"
  case "$_BIND" in
    any) _BIND=any ;;
    *)   _BIND=loopback ;;
  esac
  if command -v setsid >/dev/null 2>&1; then
    setsid "$DNSFWD" -f "$UPSTREAMS" -b "$_BIND" >>"$DNS_LOG" 2>&1 &
  else
    "$DNSFWD" -f "$UPSTREAMS" -b "$_BIND" >>"$DNS_LOG" 2>&1 &
  fi
  echo $! > "$DNS_PIDFILE"
}

# 引擎已在跑（如 service.sh 被再次触发）：只补 DNS，不重复启动
if engine_running; then
  start_dnsfwd
  exit 0
fi

start_dnsfwd

# 等网络就绪（最多 15s，避免开机时无网络导致首启失败）
i=0
while [ $i -lt 15 ]; do
  if ping -c1 -W1 223.5.5.5 >/dev/null 2>&1 || ping -c1 -W1 1.1.1.1 >/dev/null 2>&1; then
    break
  fi
  i=$((i + 1))
  sleep 1
done

echo "[$(date)] boot: 启动引擎 port=$PORT" >>"$LOG"

if command -v setsid >/dev/null 2>&1; then
  setsid "$BIN" >>"$LOG" 2>&1 &
else
  "$BIN" >>"$LOG" 2>&1 &
fi
echo $! > "$PIDFILE"

exit 0
