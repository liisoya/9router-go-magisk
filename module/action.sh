#!/system/bin/sh
# 9router-go · 管理器「操作」按钮：显示运行状态
# 数据来自 lib/ops.sh（唯一实现），详细管理请用模块 WebUI

MODDIR="$(cd "$(dirname "$0")" && pwd)"
OPS="$MODDIR/lib/ops.sh"

echo "9Router Go AI Proxy 状态"
ST="$("$OPS" status)"
kv() { echo "$ST" | grep "^$1=" | cut -d= -f2-; }

echo "  引擎     : $(kv engine) (PID $(kv engine_pid))"
echo "  端口     : $(kv port)"
echo "  版本     : $(kv engine_version)"
echo "  DNS      : $(kv dns) (PID $(kv dns_pid))"
code="$(curl -s -m 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$(kv port)/health" 2>/dev/null)"
echo "  /health  : ${code:-无响应}"
echo "  Dashboard: http://127.0.0.1:$(kv port)"
echo "  WebUI    : 管理器 → 模块 → WebUI（DNS 管理/一致性/更新）"

exit 0
