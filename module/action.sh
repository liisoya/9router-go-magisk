#!/system/bin/sh
# 9router-go · 管理器「操作」按钮：显示运行状态
# 数据来自 lib/ops.sh（唯一实现），详细管理请用模块 WebUI

MODDIR="$(cd "$(dirname "$0")" && pwd)"
OPS="$MODDIR/lib/ops.sh"

echo "9Router Go AI Proxy 状态"
# 取值走 ops.sh get（**解析的唯一所有者在 ops.sh 内**）。
# 过去这里自己 `grep "^key="` 解析 ops.sh 的**单行** status：行锚匹配行中间的键永远不中
# （engine= 在行中间 → 显示成空），而行首的 port= 会命中整行、把
# "20130 bind=loopback module_version=… engine=up …" 整串当端口吐出来 —— 于是连
# `curl http://127.0.0.1:$(kv port)/health` 都必然失败（2026-09-29 架构走查 A4）。
# 一次调用拿全部需要的键：get 每次只算一次 status（逐键调用会重复 sqlite3 查询）。
# 用 `sh "$OPS"` 而不是直接执行 `"$OPS"`：直接执行会同时依赖**执行位**与**Android 专有 shebang**
# （`#!/system/bin/sh`）—— 前者会随 checkout 丢（git 里 lib/ops.sh 记的是 644），后者让脚本
# 在任何非 Android 环境都无法运行、因而**无法离线断言**。显式给解释器两个依赖一起消掉。
GET="$(sh "$OPS" get port engine engine_pid engine_version dns dns_pid)"
kv() { printf '%s\n' "$GET" | grep "^$1=" | cut -d= -f2-; }
# 「读不到」不等于「空值」（本仓库的硬规矩）：一个键都没取到就要明说，别显示成一排空白
if [ -z "$GET" ]; then
  echo "  ⚠️ 读不到状态（ops.sh get 无输出）—— 生命周期或引擎可能尚未初始化"
fi

echo "  引擎     : $(kv engine) (PID $(kv engine_pid))"
echo "  端口     : $(kv port)"
echo "  版本     : $(kv engine_version)"
echo "  DNS      : $(kv dns) (PID $(kv dns_pid))"
code="$(curl -s -m 3 -o /dev/null -w '%{http_code}' "http://127.0.0.1:$(kv port)/health" 2>/dev/null)"
echo "  /health  : ${code:-无响应}"
echo "  Dashboard: http://127.0.0.1:$(kv port)"
echo "  WebUI    : 管理器 → 模块 → WebUI（DNS 管理/一致性/更新）"

exit 0
