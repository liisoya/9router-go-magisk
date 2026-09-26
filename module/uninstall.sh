#!/system/bin/sh
# 9router-go · 卸载钩子：停进程（含守护），保留数据目录（供应商配置不含在模块里）
#
# 正常路径：把意图与状态交给 lib/lifecycle.sh 的 life_shutdown（先关守护 —— 否则它会按
# pidfile 把刚停掉的引擎再拉起来）。
# 兜底路径：**刻意保留**一小段"按 pidfile 杀"的独立实现 —— 卸载是最后一道保险，即使
# lifecycle.sh 缺失或被改坏，也必须能把进程停下来（这是唯一被允许的重复实现，故此处
# 直接写状态文件名）。
case "$0" in
  */*) MODDIR="${0%/*}" ;;
  *)   MODDIR="/data/adb/modules/ninerouter-go" ;;
esac
DATA_DIR="${DATA_DIR:-/data/adb/9router-go}"

if [ -r "$MODDIR/lib/lifecycle.sh" ]; then
  . "$MODDIR/lib/lifecycle.sh"
  life_shutdown
else
  echo "[$(date)] uninstall: lib/lifecycle.sh 不可读，走兜底路径" >> "$DATA_DIR/9router.log" 2>/dev/null
  for f in "$DATA_DIR/9router.pid" "$DATA_DIR/dnsfwd.pid" "$DATA_DIR/watchdog.pid"; do
    if [ -f "$f" ]; then
      kill "$(cat "$f" 2>/dev/null)" 2>/dev/null
      rm -f "$f"
    fi
  done
  : > "$DATA_DIR/service-off" 2>/dev/null
  : > "$DATA_DIR/watchdog-off" 2>/dev/null
  rm -f "$DATA_DIR/watchdog-armed" 2>/dev/null
fi

exit 0
