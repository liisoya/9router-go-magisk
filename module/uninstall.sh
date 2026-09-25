#!/system/bin/sh
# 9router-go · 卸载钩子：停进程，保留数据目录（供应商配置不含在模块里）

DATA_DIR="${DATA_DIR:-/data/adb/9router-go}"

for f in "$DATA_DIR/9router.pid" "$DATA_DIR/dnsfwd.pid"; do
  if [ -f "$f" ]; then
    kill "$(cat "$f" 2>/dev/null)" 2>/dev/null
    rm -f "$f"
  fi
done

exit 0
