#!/bin/bash
# tools/inject-mod-id.sh — `__MOD_ID__` 占位符注入的**唯一实现**（build.sh 打包 / deploy-device.sh 直推共用）
#
# 为什么有它：注入原先是两份 —— build.sh 第 6 步的 `sed -i` 循环（认 index.html + app.js）
# 与 deploy-device.sh 的另一次 `sed`（认 4 个文件），两条路径各写一份清单、各写一次残留检查。
# 往新文件里加占位符时只改一处，另一条路径就带着占位符上线 → 设备上 CFG.MODDIR 错误、
# 面板"获取不到信息"（deploy 脚本的注释里就记着这次事故）。**清单本身才是会漂移的东西**，
# 所以这里不维护清单：注入对象 = 目标树里所有含 `__MOD_ID__` 的文件。
#
# 语义
#   1) MOD_ID 唯一来源 = 源树的 module.prop（`id=` 行）；可用环境变量 MOD_ID 覆盖
#   2) 全树注入（不维护文件清单）
#   3) 结束前**自己**断言零残留（不靠调用方记得检查）；不可读文件一律拒绝 ——
#      读不到的文件无法证明没有残留，宁可失败
#   4) 打印被注入的文件清单（可审计）
#
# 用法
#   tools/inject-mod-id.sh <src_root> <dst_root>
#     src_root：含 module.prop 的源树（例：module/ ；build 时也传 module/，dst 是 staging 副本）
#     dst_root：要注入的目标树（build：$STAGING/module ；deploy：$STAGING）
set -eu

SRC="${1:-}"
DST="${2:-}"
if [ -z "$SRC" ] || [ -z "$DST" ]; then
  echo "用法：tools/inject-mod-id.sh <src_root> <dst_root>" >&2
  exit 2
fi
[ -d "$DST" ] || { echo "❌ 目标树不存在：$DST" >&2; exit 2; }
[ -f "$SRC/module.prop" ] || { echo "❌ $SRC/module.prop 不存在（MOD_ID 的唯一来源）" >&2; exit 2; }

MOD_ID="${MOD_ID:-$(grep '^id=' "$SRC/module.prop" | head -1 | cut -d= -f2)}"
[ -n "$MOD_ID" ] || { echo "❌ $(basename "$SRC")/module.prop 里没有 id= 行" >&2; exit 2; }

LIST="$(mktemp)"
trap 'rm -f "$LIST"' EXIT

# ── ① 可读性：读不到 = 无法证明零残留 → 拒绝 ──
find "$DST" -type f ! -readable -print > "$LIST"
if [ -s "$LIST" ]; then
  echo "❌ 目标树里存在不可读文件，无法保证零残留：" >&2
  sed 's/^/   /' "$LIST" >&2
  exit 1
fi

# ── ② 全树注入（清单不存在于代码里）──
grep -rl -- '__MOD_ID__' "$DST" 2>/dev/null > "$LIST" || true
N=0
while IFS= read -r f; do
  [ -n "$f" ] || continue
  sed -i "s/__MOD_ID__/${MOD_ID}/g" "$f"
  echo "  注入 $f"
  N=$((N + 1))
done < "$LIST"

# ── ③ 零残留断言（本脚本自己负责，调用方不必再写一遍）──
if grep -rq -- '__MOD_ID__' "$DST" 2>/dev/null; then
  echo "❌ 注入后仍有 __MOD_ID__ 残留：" >&2
  grep -rl -- '__MOD_ID__' "$DST" 2>/dev/null | sed 's/^/   /' >&2
  exit 1
fi

echo "占位符注入完成：${N} 个文件，0 残留（MOD_ID=${MOD_ID}）"
