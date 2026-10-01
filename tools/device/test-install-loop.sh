#!/system/bin/sh
# tools/device/test-install-loop.sh — 安装路径的循环诊断闭环（**按需运行**，不在 T*/A* 默认档里）
#
# 为什么有它：2026-09-26 一次整包安装出现 `ops.sh[291]: no closing quote` 且**行号 291 > 文件 284 行**
# —— 说明当时读的是另一份内容（"脚本在执行中被替换"这一类）。当时无法复现，于是把闭环固化下来：
#   · 断言 1：每次 install-module 的输出里**不得**出现 shell 解析错误签名
#   · 断言 2：每次都必须 `engine=up`（否则"没报错"可能只是静默失败）
# 复现率是这类 bug 的唯一抓手：单次跑不出来就加大 N（每次约 40s）。
#
# 用法（真机）：
#   adb push tools/device/test-install-loop.sh /data/local/tmp/
#   adb shell 'su -c "sh /data/local/tmp/test-install-loop.sh 6"'
# 需要 $DATA_DIR/last-module.zip（跑过一次 install-module 就会留下）
MODDIR="${MODDIR:-/data/adb/modules/ninerouter-go}"
DATA_DIR="${DATA_DIR:-/data/adb/9router-go}"
N="${1:-3}"
i=1; PARSE=0; NOTUP=0
echo "== 循环 install-module × $N（$MODDIR）=="
[ -f "$DATA_DIR/last-module.zip" ] || { echo "跳过：$DATA_DIR/last-module.zip 不存在（先跑一次 install-module）"; exit 0; }
while [ "$i" -le "$N" ]; do
  cp "$DATA_DIR/last-module.zip" /data/local/tmp/9r-loop.zip 2>/dev/null
  OUT="$("$MODDIR/lib/ops.sh" install-module /data/local/tmp/9r-loop.zip 2>&1 | tr -d '\r')"
  case "$OUT" in
    *"syntax error"*|*"no closing quote"*|*"bad substitution"*|*"unexpected"*)
      echo "iter $i: PARSE-ERR → $OUT"; PARSE=$((PARSE + 1)) ;;
  esac
  if echo "$OUT" | grep -q "engine=up"; then
    echo "iter $i: ok (engine=up)"
  else
    echo "iter $i: NOT-UP → $(echo "$OUT" | tail -n 2 | tr '\n' '|')"; NOTUP=$((NOTUP + 1))
  fi
  i=$((i + 1))
done
rm -f /data/local/tmp/9r-loop.zip
echo "== 结果：N=$N 解析错误=$PARSE 未起来=$NOTUP =="
[ "$PARSE" = 0 ] && [ "$NOTUP" = 0 ] || exit 1
exit 0
