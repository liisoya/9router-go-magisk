#!/bin/sh
# tools/test-export-logs.sh — 诊断导出（ops.sh cmd_export_logs）的**离线**自证
#
# 为什么必须有这条门禁（2026-10-04）：诊断包是**唯一一条会把本地数据发到设备外**
# 的路径，而它的核心保障是 diag_redact 那一串 sed 正则。正则改错了有两个方向，
# 而且都很安静：
#   - 漏抹 → 用户把包发给维护者，邮箱/连接 id 一起走；
#   - 误伤 → 抹掉排障要用的字段（真机已踩过一次：早期用 [Kk]ey= 试探，结果
#     factory_key=1 被改成 factory_***=1，状态快照的字段名毁了）。
# 两者都不会让任何现有门禁变红 —— 真机验证也只是事后抽查，改完这一轮就没人再看。
#
# 本门禁钉住的是**行为**（喂样本、断言输出），不是源码形状。正则怎么写随意，
# 但下面这些断言必须成立。
#
# 运行：sh tools/test-export-logs.sh（已接进 tools/check.sh --offline）
set -u
cd "$(dirname "$0")/.." || exit 1

_TMPD="$(mktemp -d 2>/dev/null)" || { echo "❌ 无法创建临时目录"; exit 2; }
trap 'rm -rf "$_TMPD"' EXIT

# 隔离环境：ops.sh 顶部按 MODDIR/DATA_DIR 拼路径，绝不能碰真机或宿主目录。
MODDIR="$PWD/module"
DATA_DIR="$_TMPD"
export MODDIR DATA_DIR

PASS=0
FAIL=0
ok() { PASS=$((PASS + 1)); echo "  ✅ $*"; }
no() { FAIL=$((FAIL + 1)); echo "  ❌ $*"; }

# 加载函数定义而不跑 dispatch（守卫见 ops.sh:525-533）
if ! OPS_LIB_ONLY=1 . module/lib/ops.sh 2>/dev/null; then
  echo "❌ 无法以 OPS_LIB_ONLY=1 加载 module/lib/ops.sh"; exit 2
fi
command -v diag_redact >/dev/null 2>&1 || { echo "❌ ops.sh 里没有 diag_redact"; exit 2; }
command -v cmd_export_logs >/dev/null 2>&1 || { echo "❌ ops.sh 里没有 cmd_export_logs"; exit 2; }

# 样本用**真机上真的出现过**的形态，否则等于自证。
SAMPLE="$_TMPD/sample.log"
printf '%s\n' \
  '2026/10/03 WRN [oauth] saving connection email=xxx3515575@gmail.com connID=1f978fde-0cb9-447f-b5c7-4f9d7a277915' \
  '2026/10/03 INF [usage] connName=xxx3515575@gmail.com conn=1f978fde-0cb9' \
  '2026/10/03 WRN [proxy] invalid proxy url=http://user:secretpass@10.0.0.1:8080' \
  '2026/10/03 INF [auth] token=dt-abc123456 apiKey=sk-c74caf7ccfe refreshToken=rt-xyz Authorization=Bearer eyJhbGciOi' \
  '2026/10/03 INF [panel] factory_key=0 apikeys_total=1 port=20128 dns=up' \
  '2026/10/03 WRN [fallback] upstream failed provider=codebuddy-intl model=deepseek-v4.1-flash status=0' \
  > "$SAMPLE"
# NUL 字节：真机三份日志里都有（9router.log 280 个 / watchdog.log 220 / dnsfwd.log 99），
# 来源是引擎自己写的。混进来是为了让"忘了清 NUL"的实现当场失败。
printf 'boot: watchdog=started\n\000\000boot: noise\n' > "$SAMPLE.nul"

R="$_TMPD/redacted"
diag_redact < "$SAMPLE" > "$R" 2>/dev/null
OUT="$(cat "$R" 2>/dev/null)"

echo "== E1 身份信息：邮箱必须被抹掉 =="
case "$OUT" in
  *xxx3515575@gmail.com*) no "E1 邮箱原样出现在脱敏输出里（这正是要防的那件事）" ;;
  *) ok "E1 邮箱已抹值" ;;
esac
case "$OUT" in
  *email=****) ok "E1b 保留了 email= 键名（维护者要靠它知道这里原本是什么）" ;;
  *) no "E1b 键名被一起抹了，排障时看不出这里原本是邮箱" ;;
esac

echo "== E2 连接标识：只留前 8 位 =="
# 8 位是既有做法（media.go:711 的 conn.ID[:8]）：够定位一个连接，又不构成身份。
case "$OUT" in
  *connID=1f978fde\**) ok "E2 connID 截断到 8 位（1f978fde***）" ;;
  *) no "E2 connID 没有被截断：$(echo "$OUT" | grep -o 'connID=[^ ]*' | head -1)" ;;
esac
case "$OUT" in
  *1f978fde-0cb9-447f*) no "E2b connID 保留了完整 UUID（截断失效）" ;;
  *) ok "E2b 完整 UUID 未残留" ;;
esac
case "$OUT" in
  *connName=xxx35155*) ok "E2c connName 截断到 8 位（它的值通常是邮箱）" ;;
  *) no "E2c connName 形态异常：$(echo "$OUT" | grep -o 'connName=[^ ]*' | head -1)" ;;
esac

echo "== E3 凭据：token / key / 授权头必须抹值 =="
for k in token apiKey refreshToken Authorization; do
  case "$OUT" in
    *"$k=***"*) ok "E3 $k 已抹值" ;;
    *) no "E3 $k 未抹值：$(echo "$OUT" | grep -o "$k=[^ ]*" | head -1)" ;;
  esac
done
case "$OUT" in
  *dt-abc123456*|*sk-c74caf7ccfe*|*rt-xyz*|*eyJhbGciOi*) no "E3b 凭据明文仍出现在输出里" ;;
  *) ok "E3b 凭据明文无残留" ;;
esac

echo "== E4 URL 里的 user:pass 必须抹掉 =="
case "$OUT" in
  *secretpass*) no "E4 代理 URL 的密码仍在" ;;
  *) ok "E4 URL 凭据已抹" ;;
esac
case "$OUT" in
  *http://***:***@10.0.0.1:8080*) ok "E4b URL 主体保留（还能看出是哪个代理）" ;;
  *) no "E4b URL 结构被破坏：$(echo "$OUT" | grep -o 'http[^ ]*' | head -1)" ;;
esac

echo "== E5 不得误伤：状态快照的字段名必须活着 =="
# 这条是本门禁最该记住的一条 —— 早期用 [Kk]ey= 试探就毁在这里：
# factory_key=1 被改成 factory_***=1，字段名没了，而"看起来仍然有输出"。
for k in factory_key apikeys_total port dns; do
  case "$OUT" in
    *"$k="*) ok "E5 字段 $k 未被误伤" ;;
    *) no "E5 字段 $k 被误伤了（键名被抹掉，排障时读不到该状态）" ;;
  esac
done

echo "== E6 模型名 / provider 必须留着 =="
# 脱敏刻意不做到底：抹掉模型名会让"哪个模型在失败"这条主线线索消失。
case "$OUT" in
  *deepseek-v4.1-flash*) ok "E6 模型名保留" ;;
  *) no "E6 模型名被抹了：$(echo "$OUT" | grep -o 'model=[^ ]*' | head -1)" ;;
esac
case "$OUT" in
  *codebuddy-intl*) ok "E6b provider 名保留" ;;
  *) no "E6b provider 名被抹了" ;;
esac

echo "== E7 NUL 字节必须清掉 =="
# 不清的话维护者一 grep 就得到 "binary file matches" 且**什么都不显示** ——
# 那恰好是这个功能要解决的问题本身。
diag_redact < "$SAMPLE.nul" > "$_TMPD/nul.out" 2>/dev/null
_n="$(tr -dc '\000' < "$_TMPD/nul.out" 2>/dev/null | wc -c | tr -d ' ')"
[ "$_n" = "0" ] && ok "E7 NUL 已清除" || no "E7 仍有 $_n 个 NUL（grep 会把文件当二进制）"

echo "== E8 含 NUL 的源日志必须能被过滤出内容 =="
# 不带 grep -a 时 grep 判定 binary 后只打一行提示、**不输出任何内容** ——
# 于是 engine-errors.log 会是空的，而导出仍然报"成功"。
_F="$_TMPD/engine.log"
printf '2026/10/03 WRN [fallback] upstream failed provider=x\000\n' > "$_F"
printf '2026/10/03 INF [request] GET /models status=401\000\n' >> "$_F"
_l="$(diag_engine_lines "$_F" 2>/dev/null | wc -l | tr -d ' ')"
[ "$_l" -ge 1 ] && ok "E8 含 NUL 的日志仍能过滤出行（$_l 行）" \
                || no "E8 过滤结果为空 —— diag_engine_lines 少了 grep -a"

echo "== E9a 日志路径必须经 log_path_of 取（路径概念只能有一个家）==="
# 这条锁的是 locality：log.sh 是路径与上限的唯一声明并提供访问器，cmd_export_logs
# 若自己再写一份 "$DATA_DIR/9router.log"，加第 4 份日志时诊断包会带上它、轮转却不管它。
_body0="$(sed -n '/^cmd_export_logs()/,/^}/p' module/lib/ops.sh | grep -v '^[[:space:]]*#')"
case "$_body0" in
  *'log_path_of engine'*) ok "E9a 引擎日志经 log_path_of 取" ;;
  *) no "E9a 引擎日志没有经 log_path_of 取（路径概念又有了第二个家）" ;;
esac
# 代码是循环形态（for _k in dns watchdog + log_path_of "$_k"），不是两句字面量
case "$_body0" in
  *'for _k in dns watchdog'*'log_path_of "$_k"'*) ok "E9b dns/watchdog 也经 log_path_of 取（循环形态）" ;;
  *) no "E9b dns/watchdog 日志没有经 log_path_of 取" ;;
esac
# 唯一允许的裸路径是 gate-lifecycle.log —— 它不是模块的日志（log.sh 不声明它），
# 必须在场且必须带"不是模块日志"的说明，否则将来有人会把它塞进 log.sh 的声明式循环。
case "$_body0" in
  *'gate-lifecycle.log'*) ok "E9c gate-lifecycle.log 显式处理（它是真机门禁产物，不归 log.sh）" ;;
  *) no "E9c gate-lifecycle.log 处理被删了" ;;
esac
case "$_body0" in
  *'"$DATA_DIR/9router.log"'*) no "E9d 又出现了硬编码的 9router.log 路径" ;;
  *) ok "E9d 无硬编码的模块日志路径" ;;
esac

echo "== E9/E10 真跑一次导出，开 tar 验清单与回执 =="
# 这一段刻意**不用扫源码文本**判断"有没有拷凭据文件"：扫不出来的。源码里
# runtime.env / data.sqlite 出现在 README.txt 的 echo 文案里（那句恰恰是
# "**不含** runtime.env 与 db/data.sqlite"），扫全文必然假红 —— 本门禁第一版就栽在这。
# 唯一可靠的是把导出真的跑一遍，看 tar 里面有什么。
_OUT="$_TMPD/out"; mkdir -p "$_OUT"
# 造一点现场：缺日志文件时也不能崩（设备上 gate-lifecycle.log 常常不存在）
printf '2026/10/03 WRN [fallback] upstream failed provider=qoder model=qfmodel email=xxx3515575@gmail.com\n' > "$DATA_DIR/9router.log"
printf 'dnsfwd: started\n' > "$DATA_DIR/dnsfwd.log"
printf 'off\n' > "$DATA_DIR/runtime.env"          # 假装有密码文件
mkdir -p "$DATA_DIR/db"; printf 'sqlite-ish\n' > "$DATA_DIR/db/data.sqlite"
REPLY="$(DIAG_OUT_DIR="$_OUT" cmd_export_logs 2>/dev/null)"

[ "$REPLY" = "exported" ] && ok "E10b 回执恰好是 exported（actionOk 全等匹配能过）" \
                          || no "E10b 回执是 [$REPLY]，不是 exported —— actionOk 会判失败"
[ -s "$DATA_DIR/last-export" ] && ok "E10a 真实落点写进了 last-export" \
                              || no "E10a 没写 last-export —— 面板只能靠猜，兜底时会谎报 Download"
_LE="$(cat "$DATA_DIR/last-export" 2>/dev/null | head -1)"
case "$_LE" in
  "$_OUT"/*) ok "E10c last-export 里的落点与实际产��一致（$_LE）" ;;
  *) no "E10c last-export 写的是 [$_LE]，实际在 $_OUT —— 面板会展示错的路径" ;;
esac

_ZIP="$_OUT/9router-diag-"
_ZIPF="$(find "$_OUT" -name '9router-diag-*.tar.gz' 2>/dev/null | head -1)"
if [ -n "$_ZIPF" ] && [ -s "$_ZIPF" ]; then
  ok "E10d 产物存在且非空（$(basename "$_ZIPF")）"
  mkdir -p "$_TMPD/x"; (cd "$_TMPD/x" && tar -xzf "$_ZIPF" 2>/dev/null)
  LIST="$(cd "$_TMPD/x" && find . -type f | sed 's|^\./||' | sort | tr '\n' ' ')"
  echo "     清单: $LIST"
  for f in runtime.env data.sqlite jwt-secret initial-password last-module.zip; do
    case "$LIST" in
      *"$f"*) no "E9 $f 在 tar 里 —— 凭据文件被拷进包了" ;;
      *) ok "E9 $f 不在 tar 里" ;;
    esac
  done
  for f in summary.txt README.txt engine-errors.log; do
    case "$LIST" in
      *"$f"*) ok "E9f $f 在 tar 里（该有的都有）" ;;
      *) no "E9f $f 缺失 —— 清单不对，断言可能假绿" ;;
    esac
  done
  # 包内也必须已脱敏（不能只验函数输出，还要验落到包里��那份）
  if grep -rq "xxx3515575@gmail.com" "$_TMPD/x" 2>/dev/null; then
    no "E9g tar 里仍有邮箱明文 —— 脱敏只做了一半"
  else
    ok "E9g tar 里无邮箱明文"
  fi
else
  no "前一步没能产出 tar 包（落点 $_OUT），后续清单断言全部无法进行"
fi

echo "== 结果：通过 $PASS / 失败 $FAIL ="
[ "$FAIL" = 0 ] || exit 1
exit 0
