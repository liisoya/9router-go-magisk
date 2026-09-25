#!/system/bin/sh
# 9router-go · late_start service
# 职责：准备运行环境 → 拉起 DNS 转发器 → 拉起引擎（Dashboard 由引擎直接服务）
#
# 生命周期语义的唯一实现是 lib/ops.sh（seam）：:53 占用检查、dnsfwd 拉起/开关、
# 出厂 key 补入、端口读取都由它提供，本脚本与 action.sh / WebUI 一致调用。
#
# 两个承载性组件，删了引擎就不能用：
#   1) dnsfwd：引擎是纯 Go 静态二进制，读不到 /etc/resolv.conf 时会回落到
#      127.0.0.1:53（真机实测该文件根本不存在），必须有本地转发器接住；
#   2) SSL_CERT_DIR：没有它所有 HTTPS 与更新检查都会失败。

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
OPS="$MODDIR/lib/ops.sh"

BIN="$MODDIR/bin/9router-go"
LOG="$DATA_DIR/9router.log"
PIDFILE="$DATA_DIR/9router.pid"

mkdir -p "$DATA_DIR/db"

# --- 数据库 schema 引导 ---
# Go 引擎无迁移系统（schema 历来由 Node 版官方程序创建），全新安装的空库缺业务表，
# Dashboard 会报 "no such table: settings / apiKeys"。用模块内置 schema.sql 幂等建表
# （全部 IF NOT EXISTS，每次启动执行也无副作用，还能自愈半初始化的库）。
SQLITE3="$MODDIR/bin/sqlite3"
DB_FILE="$DATA_DIR/db/data.sqlite"
if [ -x "$SQLITE3" ] && [ -f "$MODDIR/etc/schema.sql" ]; then
  if ! "$SQLITE3" "$DB_FILE" "SELECT 1 FROM settings LIMIT 1;" >/dev/null 2>&1; then
    "$SQLITE3" "$DB_FILE" < "$MODDIR/etc/schema.sql" 2>>"$LOG" \
      && echo "[$(date)] schema bootstrap: applied to $DB_FILE" >>"$LOG"
  fi
fi

# --- 阻断引擎自更新（模块内更新一律走 zip）---
# 引擎在 AUTO_UPDATE env 为 false 时会回读 DB 的 settings.AutoUpdate（server.go OnStart），
# 导入含 autoUpdate:true 的备份会重新触发自更新、绕过模块管理。每次启动压回 false。
if [ -x "$SQLITE3" ] && [ -s "$DB_FILE" ]; then
  _AUTOUPD_SQL='UPDATE settings SET data = json_set(data, '\''$.autoUpdate'\'', json('\''false'\'')) WHERE json_type(data, '\''$.autoUpdate'\'') IS NOT NULL;'
  "$SQLITE3" "$DB_FILE" "$_AUTOUPD_SQL" 2>>"$LOG"
fi

# --- 承载性 2/2：CA 目录 ---
export SSL_CERT_DIR=/system/etc/security/cacerts
export DATA_DIR MODDIR

# 引擎自动更新关闭：更新一律走模块 zip 刷入，避免二进制自更新绕过模块管理
export AUTO_UPDATE=false

# --- 端口（ops.sh 统一实现：$DATA_DIR/port 持久值 + 严格校验，默认 20130）---
PORT="$("$OPS" get-port)"
export PORT

# --- 初始管理密码：默认 123456（首次登录后请在 Dashboard 修改）---
if [ ! -f "$DATA_DIR/initial-password" ]; then
  printf '123456\n' > "$DATA_DIR/initial-password"
  chmod 600 "$DATA_DIR/initial-password"
fi
export INITIAL_PASSWORD="$(cat "$DATA_DIR/initial-password" 2>/dev/null)"

# --- DNS 上游文件（首次生成公共兜底；严禁出现 127.0.0.1，否则转发器自我循环）---
if [ ! -s "$DATA_DIR/dns-upstreams.conf" ]; then
  {
    echo "# 9router-go 生成：公共 DNS 兜底（无优选）"
    echo "nameserver 223.5.5.5"
    echo "nameserver 119.29.29.29"
    echo "nameserver 1.1.1.1"
  } > "$DATA_DIR/dns-upstreams.conf"
fi

# 引擎已在跑（如 service.sh 被再次触发）：只补 DNS，不重复启动
if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE" 2>/dev/null)" 2>/dev/null; then
  "$OPS" start-dns >>"$DATA_DIR/dnsfwd.log" 2>&1
  exit 0
fi

# 拉起 DNS 转发器（ops.sh 内含 dns-disabled 开关、:53 占用自动让路）
"$OPS" start-dns >>"$DATA_DIR/dnsfwd.log" 2>&1

# 出厂客户端 key：仅在 apiKeys 表为空（全新安装）时补入，策略在 ops.sh
"$OPS" seed-key >>"$LOG" 2>&1

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
