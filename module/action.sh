#!/system/bin/sh
# 9router-go · 管理器「操作」按钮：显示运行状态
# 详细管理（DNS 上游/探测/热重载）请用模块 WebUI

MODDIR="${0%/*}"
DATA_DIR="${DATA_DIR:-/data/adb/9router-go}"
PORT="$(cat "$DATA_DIR/port" 2>/dev/null | tr -d ' \n')"
case "$PORT" in ''|*[!0-9]*) PORT=20130 ;; esac
PIDFILE="$DATA_DIR/9router.pid"
DNS_PIDFILE="$DATA_DIR/dnsfwd.pid"

echo "9Router Go AI Proxy 状态"
echo "  数据目录 : $DATA_DIR"
echo "  引擎端口 : $PORT"

if [ -f "$PIDFILE" ] && kill -0 "$(cat "$PIDFILE" 2>/dev/null)" 2>/dev/null; then
  echo "  引擎 PID : $(cat "$PIDFILE")"
else
  echo "  引擎 PID : （未运行）"
fi

if [ -f "$DNS_PIDFILE" ] && kill -0 "$(cat "$DNS_PIDFILE" 2>/dev/null)" 2>/dev/null; then
  echo "  DNS PID  : $(cat "$DNS_PIDFILE")  [bind=$(cat "$DATA_DIR/dns-bind" 2>/dev/null || echo loopback)]"
else
  echo "  DNS PID  : （未运行 —— 引擎将无法解析域名）"
fi

code="$(curl -s -m 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/health" 2>/dev/null)"
echo "  /health  : ${code:-无响应}"
echo "  Dashboard: http://127.0.0.1:$PORT"
echo "  WebUI    : 管理器 → 模块 → WebUI（DNS 管理）"

exit 0
