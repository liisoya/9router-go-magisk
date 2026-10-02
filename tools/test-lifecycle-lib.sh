#!/bin/sh
# tools/test-lifecycle-lib.sh — lib/lifecycle.sh 的**离线**自证（与 tools/test-wait-lib.sh 同风格）
#
# 为什么需要（2026-09-29 真机事故）：开机那次守护没起来，而 life_boot 把 life_wd_start 的
# 判据丢进 /dev/null —— start-failed 被静默，日志里一个字都没有。用户只能从面板看到
# "未运行"，无从排查；而守护不在 = 引擎任何死因都不会再自愈。
#
# 做法：lifecycle.sh 可 source（零副作用），所以"判据有没有被丢弃""pidfile 复用会不会
# 误判""非法参数会不会被写入 /proc"这些都能脱离真机断言。真机只留"写 -1000 是否成功"
# 那一条（需 root，见 tools/device/test-lifecycle.sh）。
#
# 运行：sh tools/test-lifecycle-lib.sh（已接进 tools/check.sh --offline）
set -u
cd "$(dirname "$0")/.." || exit 1

# 隔离数据目录：本测试会真写日志与状态文件，绝不碰真机/宿主的数据目录
_TMPD="$(mktemp -d 2>/dev/null)" || { echo "❌ 无法创建临时目录"; exit 2; }
trap 'rm -rf "$_TMPD"' EXIT

MODDIR="$PWD/module"
DATA_DIR="$_TMPD"
export MODDIR DATA_DIR
. module/lib/lifecycle.sh || { echo "❌ 无法 source module/lib/lifecycle.sh"; exit 2; }

PASS=0
FAIL=0
SKIP=0
ok() { PASS=$((PASS + 1)); echo "  ✅ $*"; }
no() { FAIL=$((FAIL + 1)); echo "  ❌ $*"; }
# 「跳过」必须有自己的计数器（2026-09-29 诊断）：此前没有 skp，L7g 这类"本机观察不到条件"的断言
# 只能写成 ok() —— 于是未执行的断言被计成通过，门禁看起来全绿却少跑了一条。跳过要看得见。
skp() { SKIP=$((SKIP + 1)); echo "  ⏭ $*"; }
log_has() { grep -q "$1" "$DATA_DIR/9router.log" 2>/dev/null; }

echo "== L1 life_boot：守护启动失败必须**写进日志**（判据不许静默）=="
# 调用计数用**文件**：life_boot 里是 `_w="$(life_wd_start)"`，函数体跑在子 shell 里，
# 变量计数的副作用传不回主 shell（第一次写这个测试时正好踩到，见 L1b 的红灯）。
_wd_cnt() { wc -l < "$_TMPD/wd.calls" 2>/dev/null | tr -d ' '; }
rm -f "$DATA_DIR/9router.log" "$_TMPD/wd.calls"
life_wd_start() { echo x >> "$_TMPD/wd.calls"; echo "start-failed"; }
life_boot >/dev/null 2>&1
if log_has 'boot: watchdog=start-failed'; then
  ok "L1 失败判据进了日志"
else
  no "L1 守护启动失败却没有任何日志（修复前的行为）"
fi
[ "$(_wd_cnt)" = "2" ] && ok "L1b 失败后重试了一次（调用 $(_wd_cnt) 次）" \
                       || no "L1b 期望重试一次，实际调用 $(_wd_cnt) 次"

echo "== L2 life_boot：成功时只启动一次、判据如实 =="
rm -f "$DATA_DIR/9router.log" "$_TMPD/wd.calls"
life_wd_start() { echo x >> "$_TMPD/wd.calls"; echo "started"; }
life_boot >/dev/null 2>&1
if log_has 'boot: watchdog=started'; then ok "L2 成功判据进了日志"; else no "L2 成功判据没写日志"; fi
[ "$(_wd_cnt)" = "1" ] && ok "L2b 成功时不重复启动（调用 $(_wd_cnt) 次）" \
                       || no "L2b 期望调用 1 次，实际 $(_wd_cnt) 次"

echo "== L3 守护身份校验：pidfile 被无关进程复用 → 必须判"不在" =="
# 真机现场：watchdog.pid 是个长期跑过的号，长跑 pid_max 绕回后可能落到别的进程上。
sleep 30 &
_other=$!
printf '%s\n' "$_other" > "$LIFE_ST_WD_PID"
if life_wd_alive; then
  no "L3 把无关进程（pid=$_other，活着的 sleep）当成了守护 —— 面板会永久误报 up"
else
  ok "L3 无关进程不被认作守护"
fi
if life_pid_is_watchdog "$_other"; then no "L3b life_pid_is_watchdog 误认"; else ok "L3b 身份判定为假"; fi
kill -9 "$_other" 2>/dev/null

echo "== L3c 引擎身份校验：pidfile 被无关进程复用 → 必须判"不在"（2026-09-30 真机事故）=="
# 真机现场：用户设备 82 分钟内 pid 推进了 12943 个号（6965 → 19908）—— 引擎早已死掉、号被
# 别的进程占住，而面板照报 engine=up、守护 39 分钟一次都没判死（自愈形同不存在），
# 连"引擎内存 571.7MB"都是**那个无关进程**的内存。判据与 L3（守护侧）同源，但引擎这半边
# 一直缺：life_engine_healthy 只看 pidfile 存活。
sleep 30 &
_e_other=$!
printf '%s\n' "$_e_other" > "$LIFE_ST_ENGINE"
if life_engine_healthy; then
  no "L3c 把无关进程（pid=$_e_other，活着的 sleep）当成了引擎 —— 面板会误报 up、守护永不拉起"
else
  ok "L3c 无关进程不被认作引擎"
fi
if [ "$(life_state | sed -n 's/.*engine=\([a-z]*\).*/\1/p')" = down ]; then
  ok "L3d life_state 报 engine=down"
else
  no "L3d life_state 仍报 up（面板会如实告诉用户'引擎在跑'）"
fi
kill -9 "$_e_other" 2>/dev/null

echo "== L3e 反向：pidfile 里确实是我们的引擎 → 必须判"在"（防过度严格引发重启风暴）=="
# 为什么必须锁这条：身份校验一旦过严（模块目录经符号链接到达、二进制被 mv 后带 " (deleted)"、
# SELinux 下读不到 /proc/<pid>/exe），就会把**活着的**引擎判成不在 → 守护反复重启、
# 杀掉用户正在用的代理。所以"松"是刻意的：读不到身份信息时判"在"。
_OLD_LIFE_BIN="$LIFE_BIN"
LIFE_BIN="$_TMPD/bin/9router-go"
mkdir -p "$_TMPD/bin"
printf '#!/bin/sh\nsleep 30\n' > "$LIFE_BIN"
chmod 0755 "$LIFE_BIN"
"$LIFE_BIN" &
_e_fake=$!
printf '%s\n' "$_e_fake" > "$LIFE_ST_ENGINE"
if life_engine_healthy; then
  ok "L3e 自己的引擎 → 判在"
else
  no "L3e 自己的引擎被判不在（会反复重启，杀掉用户正在用的代理）"
fi
kill -9 "$_e_fake" 2>/dev/null
LIFE_BIN="$_OLD_LIFE_BIN"
printf '%s\n' "" > "$LIFE_ST_ENGINE"

echo "== L4 身份校验：非法输入一律判假（不猜）=="
for bad in '' abc 0 -1 '1 2'; do
  if life_pid_is_watchdog "$bad"; then no "L4 非法 pid [$bad] 被判为真"; else ok "L4 非法 pid [$bad] → 假"; fi
done
printf '%s\n' "" > "$LIFE_ST_WD_PID"
if life_wd_alive; then no "L4b 空 pidfile 被判为活着"; else ok "L4b 空 pidfile → 不在"; fi

echo "== L5 life_rss_kb：读得到数字 / 读不到不冒充 0 =="
_r="$(life_rss_kb $$)"
case "$_r" in ''|*[!0-9]*) no "L5 当前 shell 的 RSS 读不到（得到 [$_r]）" ;; *) ok "L5 读到 RSS ${_r}kB" ;; esac
if life_rss_kb 999999 >/dev/null 2>&1; then no "L5b 不存在的 pid 却报成功"; else ok "L5b 不存在的 pid → 如实失败"; fi
if life_rss_kb '' >/dev/null 2>&1; then no "L5c 空参数却报成功"; else ok "L5c 空参数 → 如实失败"; fi

echo "== L6 life_oom_protect：非法参数绝不写 /proc（越权写入比不写更危险）=="
if life_oom_protect '' -1000; then no "L6 空 pid 却报成功"; else ok "L6 空 pid → 拒绝"; fi
if life_oom_protect 1 abc; then no "L6b 非数字 adj 却报成功"; else ok "L6b 非数字 adj → 拒绝"; fi
if life_oom_protect 1 -99999; then no "L6c 超范围 adj 却报成功"; else ok "L6c 超范围 adj → 拒绝"; fi
if life_oom_protect 999999 -1000; then no "L6d 不存在的 pid 却报成功"; else ok "L6d 不存在的 pid → 如实失败"; fi

echo "== L7 life_pid_alive：热路径改写（$() 换成内建 read）后语义必须一字不变 =="
# 为什么单独锁这组：它是守护每 5s 要问两次的谓词，为了省 fork 改写，而"活着/不在"的判定
# 一旦有偏差，后果是"守护以为引擎还活着"或"以为守护自己不在"—— 两种都很难在真机上察觉。
sleep 30 &
_live=$!
printf '%s\n' "$_live" > "$_TMPD/alive.pid"
life_pid_alive "$_TMPD/alive.pid" && ok "L7 活着的 pid → 在" || no "L7 活着的 pid 被判不在"
sleep 0.1 &
_dead=$!
wait "$_dead" 2>/dev/null
printf '%s\n' "$_dead" > "$_TMPD/dead.pid"
life_pid_alive "$_TMPD/dead.pid" && no "L7b 已退出的 pid 被判在" || ok "L7b 已退出的 pid → 不在"
: > "$_TMPD/empty.pid"
life_pid_alive "$_TMPD/empty.pid" && no "L7c 空 pidfile 被判在" || ok "L7c 空 pidfile → 不在"
life_pid_alive "$_TMPD/nonexistent.pid" && no "L7d 缺文件被判在" || ok "L7d 缺文件 → 不在"
printf 'not-a-number\n' > "$_TMPD/bad.pid"
life_pid_alive "$_TMPD/bad.pid" && no "L7e 非数字被判在" || ok "L7e 非数字 → 不在"
printf '%s\ngarbage\n' "$_live" > "$_TMPD/multi.pid"
life_pid_alive "$_TMPD/multi.pid" && ok "L7f 多行 pidfile 取首行（read 比 \$(cat) 更稳）" \
                                  || no "L7f 多行 pidfile 读错"
kill -9 "$_live" 2>/dev/null

# 僵尸态：子进程已退出但尚未被 wait 回收 —— `kill -0` 对它仍然成功，
# 若判成"在"，守护就永远不会去拉起（这是"引擎崩了却没人管"的一条静默路径）。
sleep 30 &
_zombie=$!
kill -9 "$_zombie" 2>/dev/null
sleep 0.3
printf '%s\n' "$_zombie" > "$_TMPD/zombie.pid"
if [ -r "/proc/$_zombie/stat" ] && grep -q ') Z ' "/proc/$_zombie/stat" 2>/dev/null; then
  life_pid_alive "$_TMPD/zombie.pid" && no "L7g 僵尸进程被判在（会让守护永不拉起）" \
                                     || ok "L7g 僵尸态 → 不在（kill -0 会误报，已用 state 字段兜住）"
else
  # **跳过就要记成跳过**（2026-09-29 诊断）：这里原本调 ok()，把一条"未执行的断言"计成通过 ——
  # 僵尸判定于是只剩纯字符串用例（L7h–L7k）覆盖，集成路径静默回归也不会红。
  skp "L7g 本机立即回收子进程，没有可观察的僵尸窗口（僵尸判定由 L7h–L7k 覆盖）"
fi
wait "$_zombie" 2>/dev/null

# 僵尸解析本身（纯字符串）：僵尸窗口在测试机上转瞬即逝，所以把判定抽成纯函数、用真实样本锁
_z() { life_stat_is_zombie "$1" && echo yes || echo no; }
[ "$(_z '1234 (sh) Z 1 2 3')" = yes ] && ok "L7h state=Z → 判僵尸" || no "L7h Z 没认出来"
[ "$(_z '1234 (sh) S 1 2 3')" = no ] && ok "L7i state=S → 不是僵尸" || no "L7i S 被误判成僵尸"
[ "$(_z '1234 (a b) Z 1')" = yes ] && ok "L7j comm 含空格也认得出（按形状匹配，不切字段）" || no "L7j 含空格的 comm 解析错位"
[ "$(_z '')" = no ] && ok "L7k 空行 → 不是僵尸" || no "L7k 空行判错"

# **静默性**：真机事故 —— `read v < /proc/<pid>/stat 2>/dev/null` 的重定向顺序写反时，
# 刚死的进程会让 "can't open /proc/<pid>/stat" 混进 ops.sh panel 的键值输出。
printf '999999\n' > "$_TMPD/ghost.pid"
_noise="$( { life_pid_alive "$_TMPD/ghost.pid" || true; } 2>&1 )"
[ -z "$_noise" ] && ok "L7l /proc 条目已消失时完全静默（stderr 会污染 panel 输出）" \
                || no "L7l 泄漏了错误输出：$_noise"
_noise2="$( { life_pid_alive "$_TMPD/nonexistent.pid" || true; } 2>&1 )"
[ -z "$_noise2" ] && ok "L7m 缺 pidfile 时静默" || no "L7m 缺文件时泄漏输出：$_noise2"

echo "== L8 life_rss_log_due：内存证据策略（超阈值限流 + 每小时基线，单位是**秒**）=="
# 之前只记"超 100MB"，会漏掉"缓慢爬到 90MB"这种最需要证据的形态（真机那份报障正是它）。
# 单位从"轮次"改成"秒"是 Phase 33.12 的必修：轮次会随轮询周期变（5s→60s 时"每 12 轮"
# 会从 60 秒悄悄变成 12 分钟），把意图静默改掉。
_W="$LIFE_RSS_WARN_KB"
[ "$(life_rss_log_due "$((_W + 1024))" 1000 0)" = "1" ] && ok "L8 超阈值且隔够 12 分钟 → 记" || no "L8 超阈值却不记"
[ "$(life_rss_log_due "$((_W + 1024))" 100 0)" = "0" ] && ok "L8b 超阈值但只隔 100 秒 → 不记（防刷屏）" || no "L8b 限流失效"
[ "$(life_rss_log_due "$((_W + 1024))" 1000 900)" = "0" ] && ok "L8c 与上一行只隔 100 秒 → 不记" || no "L8c 间隔判定失效"
[ "$(life_rss_log_due 50000 3600 0)" = "1" ] && ok "L8d 低于阈值但满一小时 → 记基线（趋势不断线）" || no "L8d 基线没记"
[ "$(life_rss_log_due 50000 3599 0)" = "0" ] && ok "L8e 低于阈值且差 1 秒未满一小时 → 不记" || no "L8e 基线记早了"
[ "$(life_rss_log_due 50000 3600 3600)" = "0" ] && ok "L8f 基线也受限流（刚记过 0 秒）" || no "L8f 基线绕过了限流"
[ "$(life_rss_log_due '' 1000 0)" = "0" ] && ok "L8g 读不到 RSS → 不记（不冒充 0）" || no "L8g 空 RSS 却记了"
[ "$(life_rss_log_due abc 1000 0)" = "0" ] && ok "L8h 非数字 RSS → 不记" || no "L8h 非数字 RSS 却记了"

echo "== L9 life_exit_reason：退出原因解码（'为什么又死了'的唯一客观证据）=="
# 128+N = 被信号 N 杀死；其余是退出码。守护只在"引擎是自己的子进程"时拿得到它。
case "$(life_exit_reason 0)" in *正常退出*) ok "L9 退出码 0 → 正常退出" ;; *) no "L9 0 解码错" ;; esac
case "$(life_exit_reason 137)" in *SIGKILL*) ok "L9b 137 → SIGKILL（kill -9 / 内存回收 / 连坐）" ;; *) no "L9b 137 解码错" ;; esac
case "$(life_exit_reason 143)" in *SIGTERM*) ok "L9c 143 → SIGTERM（优雅停止）" ;; *) no "L9c 143 解码错" ;; esac
case "$(life_exit_reason 139)" in *SIGSEGV*) ok "L9d 139 → SIGSEGV（自身崩溃）" ;; *) no "L9d 139 解码错" ;; esac
case "$(life_exit_reason 200)" in *"信号 72"*) ok "L9e 未列举的信号也如实换算（200→信号 72）" ;; *) no "L9e 200 解码错：$(life_exit_reason 200)" ;; esac
case "$(life_exit_reason 3)" in *"退出码 3"*) ok "L9f 普通退出码如实报" ;; *) no "L9f 3 解码错" ;; esac
case "$(life_exit_reason '')" in *未知*) ok "L9g 空值 → 原因未知（没冒充成功）" ;; *) no "L9g 空值判错" ;; esac
case "$(life_exit_reason abc)" in *未知*) ok "L9h 非数字 → 原因未知" ;; *) no "L9h 非数字判错" ;; esac

echo "== L10 life_wd_notify：pidfile 陈旧时**绝不能误杀无辜进程**（USR1 默认动作是终止）=="
# 为什么单独锁这条：notify 是"写完请求顺手叫醒守护"，而 pidfile 可能陈旧（pid 被复用）
# → 不校验就发 USR1 = 可能杀掉一个毫不相干的进程。真机事故里这种最难查。
sleep 30 &
_innocent=$!
printf '%s\n' "$_innocent" > "$_TMPD/watchdog.pid"
LIFE_ST_WD_PID="$_TMPD/watchdog.pid"
life_wd_notify && no "L10 对非守护进程仍返回成功" || ok "L10 拒绝向非守护进程发信号"
kill -0 "$_innocent" 2>/dev/null && ok "L10b 无辜进程毫发无伤（没被 USR1 杀掉）" || no "L10b **误杀了无辜进程**！"
kill -9 "$_innocent" 2>/dev/null
wait "$_innocent" 2>/dev/null

# 正向：造一个"cmdline 里带 watchdog.sh"的进程 → 应当被成功叫醒（USR1 会终止它，说明信号真的发出去了）
# 用**脚本**而不是"改名后的二进制"：当进程以 `sh xxx/watchdog.sh` 形式运行时 cmdline 同样
# 带指纹（life_pid_is_watchdog 就是查这个），而本机沙箱会拒绝执行被改名的二进制。
# 脚本体内用 `sleep 1` 循环：被 USR1 打断后最多 1 秒那个 sleep 就自己退掉，不留孤儿。
cat > "$_TMPD/watchdog.sh" <<'EOF'
i=0
while [ "$i" -lt 30 ]; do sleep 1; i=$((i + 1)); done
EOF
sh "$_TMPD/watchdog.sh" &
_fakewd=$!
printf '%s\n' "$_fakewd" > "$_TMPD/watchdog.pid"
sleep 0.3
# **先证明它活着**：否则"信号送达"的断言会因为主体不存在而假绿（本轮真的踩了一次）
if kill -0 "$_fakewd" 2>/dev/null; then
  ok "L10c 前置：假守护已就绪（否则后面的断言会是假绿）"
  life_wd_notify && ok "L10d 认得 cmdline 指纹 → 发出信号" || no "L10d 对真守护形态却没发信号"
  sleep 0.5
  kill -0 "$_fakewd" 2>/dev/null && no "L10e 信号没送到（进程还活着）" || ok "L10e 信号确实送达（USR1 终结了它）"
else
  no "L10c 造进程失败（本机限制），无法验证正向路径"
fi
kill -9 "$_fakewd" 2>/dev/null
wait "$_fakewd" 2>/dev/null

echo "== L11 意图必须先于动作：快路径会放大任何"先动手后落盘"的顺序错误 =="
# 真机 T4 抓到的缺陷：life_stop_user 先停进程、后写 service-off —— 守护被 CHLD 毫秒级唤醒，
# 此刻意图还不存在 → 把用户刚停掉的服务复活。这里断言"动作发生的那一刻意图是否已可见"，
# 不依赖真机（比在真机上 sleep 30 观察可靠、也快得多）。
LIFE_ST_USER_OFF="$_TMPD/user-off"
LIFE_ST_DNS_OFF="$_TMPD/dns-off"
rm -f "$LIFE_ST_USER_OFF" "$LIFE_ST_DNS_OFF" "$_TMPD/eng-intent" "$_TMPD/dns-intent"
life_stop_all() { if [ -f "$LIFE_ST_USER_OFF" ]; then echo visible > "$_TMPD/eng-intent"; else echo missing > "$_TMPD/eng-intent"; fi; }
life_stop_dns() { if [ -f "$LIFE_ST_DNS_OFF" ]; then echo visible > "$_TMPD/dns-intent"; else echo missing > "$_TMPD/dns-intent"; fi; }
life_stop_user >/dev/null 2>&1
if [ "$(cat "$_TMPD/eng-intent" 2>/dev/null)" = "visible" ]; then
  ok "L11 stop-user：停进程那一刻 service-off 已落盘"
else
  no "L11 stop-user 先停进程才写意图（CHLD 快路径会把用户停掉的服务复活）"
fi
life_disable_dns >/dev/null 2>&1
if [ "$(cat "$_TMPD/dns-intent" 2>/dev/null)" = "visible" ]; then
  ok "L11b disable-dns：停 dnsfwd 那一刻 dns-disabled 已落盘"
else
  no "L11b disable-dns 顺序反了（关了又自己开）"
fi
rm -f "$LIFE_ST_USER_OFF" "$LIFE_ST_DNS_OFF"

echo "== L12 life_restart_all：「停了它就要负责把它起回来」三件事必须齐备且有序（A3）=="
# 真机缺陷：无守护分支过去只 ensure_engine —— stop_all 已经把 DNS 一起停了却没人拉回，
# 而 Android 无 /etc/resolv.conf、引擎只认 127.0.0.1:53，DNS 不在就等于域名解析全挂
# （按钮文案写的却是「重启引擎 + DNS」）。用桩记录顺序，把"三件齐备"变成可断言的事实。
# 注意用**文件**记序：life_restart_all 内部经子 shell 调 ensure（为透传 LIFE_CALLER），
# 变量副作用传不回来 —— 这正是 33.7 记过的教训。
_ORD="$_TMPD/order.txt"
: > "$_ORD"
life_stop_all()      { printf 'stop '   >> "$_ORD"; }
life_ensure_engine() { printf 'engine ' >> "$_ORD"; }
life_ensure_dns()    { printf 'dns '    >> "$_ORD"; }
life_restart_all test-caller >/dev/null 2>&1
if [ "$(cat "$_ORD")" = "stop engine dns " ]; then
  ok "L12 停 → 引擎 → DNS 三件齐备且有序（DNS 不会再被漏掉）"
else
  no "L12 组合不对：「$(cat "$_ORD")」，期望「stop engine dns 」"
fi

echo "== L13 承载性 env：清单驱动写出（键集合必须逐键一致）=="
# 为什么（2026-09-30 架构扫描 C6）：清单 / 六行 echo / 内联 export 曾是**三份并行实现** ——
# 改一处忘另一处离线看不见（只有真机 T7 兜，而 T7 要有设备）。现在清单是唯一声明：
# 写出物必须与它**逐键一致**（多一个、少一个都红），内联兜底也走同一清单。
_ENVD="$_TMPD/envcheck"; mkdir -p "$_ENVD"
printf 'pw-123\n' > "$_ENVD/initial-password"
_OLD_DATA="$DATA_DIR"; _OLD_ENV="${LIFE_RUNTIME_ENV:-}"
DATA_DIR="$_ENVD"; LIFE_RUNTIME_ENV="$_ENVD/runtime.env"
if life_write_runtime_env >/dev/null 2>&1; then ok "L13 runtime.env 可生成"; else no "L13 生成失败"; fi
_list="$(life_carrier_env_keys | sort | tr '\n' ' ')"
_written="$(sed -n 's/^\([A-Z_][A-Z_0-9]*\)=.*/\1/p' "$_ENVD/runtime.env" 2>/dev/null | sort | tr '\n' ' ')"
[ "$_list" = "$_written" ] && ok "L13 写出键集合 == 清单（$_list）" \
  || no "L13 清单与写出不一致：清单[$_list] 写出[$_written]"
_missing=""
for _k in $(life_carrier_env_keys); do
  case "$_k" in INITIAL_PASSWORD) continue ;; esac   # 初始密码文件可不存在（首启前）
  grep -q "^$_k=." "$_ENVD/runtime.env" 2>/dev/null || _missing="$_missing $_k"
done
[ -z "$_missing" ] && ok "L13 承载性键都有非空值" || no "L13 这些键没有值：$_missing"
# 内联兜底（runtime.env 生成/加载失败时走它）必须导出**同一批键**，不能是第三份手写清单
_fallback="$( ( unset SSL_CERT_DIR AUTO_UPDATE PORT DATA_DIR MODDIR INITIAL_PASSWORD 2>/dev/null; \
  life_carrier_env_export; env ) 2>/dev/null \
  | sed -n 's/^\(SSL_CERT_DIR\|AUTO_UPDATE\|PORT\|DATA_DIR\|MODDIR\|INITIAL_PASSWORD\)=.*/\1/p' | sort | tr '\n' ' ')"
[ "$_fallback" = "$_list" ] && ok "L13 内联兜底导出的键 == 清单" \
  || no "L13 兜底与清单不一致：清单[$_list] 兜底[$_fallback]"
DATA_DIR="$_OLD_DATA"; LIFE_RUNTIME_ENV="$_OLD_ENV"

echo "== L14 死因取证的纯函数：cgroup→memory.events 路径映射 + oom_kill 计数（2026-09-30 教训）=="
# 判死时进程已消失，唯一能回答「是不是被内存回收杀的」的客观证据是 memory.events 的
# oom_kill 计数。路径拼错在真机上只会表现为"读不到"，然后被当成"内核不支持"漏过 ——
# 所以把映射与读取锁在这里（Phase 37 的遗留项①）。
_e="$(life_cgroup_memory_events /)"
[ "$_e" = "/sys/fs/cgroup/memory.events" ] && ok "L14 根 cgroup → /sys/fs/cgroup/memory.events" \
                                          || no "L14 根 cgroup 映射错：[$_e]"
_e="$(life_cgroup_memory_events /apps/uid_10342/pid_17059)"
[ "$_e" = "/sys/fs/cgroup/apps/uid_10342/pid_17059/memory.events" ] \
  && ok "L14 子 cgroup 映射 ✓" || no "L14 子 cgroup 映射错：[$_e]"
if life_cgroup_memory_events "../etc" >/dev/null 2>&1; then no "L14 相对路径被接受（越权拼接）"; else ok "L14 相对路径 → 拒绝"; fi
if life_cgroup_memory_events "/a/../b" >/dev/null 2>&1; then no "L14 含 .. 被接受（越权拼接）"; else ok "L14 含 .. → 拒绝"; fi
if life_cgroup_memory_events "" >/dev/null 2>&1; then no "L14 空参数被接受"; else ok "L14 空参数 → 拒绝"; fi
_evf="$_TMPD/memory.events"
printf 'pgscan 1\noom_kill 3\n' > "$_evf"
[ "$(life_oom_kill_count "$_evf")" = "3" ] && ok "L14 oom_kill 计数读取 ✓" \
                                          || no "L14 计数读取错：[$(life_oom_kill_count "$_evf")]"
printf 'pgscan 1\n' > "$_evf"
if life_oom_kill_count "$_evf" >/dev/null 2>&1; then no "L14 缺 oom_kill 行却报成功（冒充 0）"; else ok "L14 无 oom_kill 行 → 如实失败"; fi
if life_oom_kill_count "$_TMPD/不存在" >/dev/null 2>&1; then no "L14 文件缺失却报成功"; else ok "L14 文件缺失 → 如实失败"; fi

echo "== L15 life_start_user：输出必须只有**一行**（UI 按全等匹配状态词）=="
# 真机 2026-10-01：life_ensure_engine 吐的 started/running 泄漏进 stdout，界面拿到的是
# "started\nengine=up"，而 runOpsAction 用 got === 'engine=up' 全等匹配 → 恒判失败，
# 用户看到「❌ 服务未启动（shell 回：running\nengine=up）」——点启动永远是红的。
# 锁这条 = 锁住"ops.sh 每个子命令只吐一个状态词"这条契约在启动路径上也成立。
life_wd_ensure_started() { echo "not-armed"; }
life_wd_alive() { return 1; }
life_ensure_engine() { echo "started"; }   # 刻意保留"会吐一行"的真实形状
life_ensure_dns() { echo "started"; }
life_engine_healthy() { return 0; }
life_dns_healthy() { return 0; }   # 已在终态：start-user 会等 DNS 到终态，不桩这条会空等 30s
_out="$(life_start_user)"
_lines="$(printf '%s\n' "$_out" | wc -l | tr -d ' ')"
[ "$_lines" = "1" ] && ok "L15 start-user 只输出一行（[$_out]）" || no "L15 输出了 $_lines 行：[$_out]"
case "$_out" in
  engine=up|engine=down|engine=running) ok "L15b 状态词合法（$_out）" ;;
  *) no "L15b 状态词非法：[$_out]" ;;
esac

echo "== L18 life_wd_stop：pidfile 与 armed 必须一起清（否则面板 watchdog=stale）=="
# life_state 的口径：`alive → up`，否则 `armed → stale`。只 kill 不清这两个文件，
# 面板会显示"已武装但不在跑"，用户读成故障 —— 而"停止"要的是干干净净的 down。
# **必须排在 L16/L17 之前**：那两组用例会拿桩盖掉 life_wd_stop / life_pid_of，
# 而 `unset -f` 是"删除"不是"还原"（同名真身一起没了），调序比还原可靠。
LIFE_ST_WD_PID="$_TMPD/wd18.pid"; LIFE_ST_WD_ARMED="$_TMPD/wd18.armed"
LIFE_ST_WD_OFF="$_TMPD/wd18.off";  LIFE_ST_WD_HOLD="$_TMPD/wd18.hold"
LIFE_ST_WD_REQ="$_TMPD/wd18.req"
printf '999999\n' > "$LIFE_ST_WD_PID"   # 不存在的 pid：真杀进程那一段自然跳过
: > "$LIFE_ST_WD_ARMED"
life_wd_stop >/dev/null 2>&1
[ -f "$LIFE_ST_WD_PID" ] && no "L18 pidfile 没清" || ok "L18 pidfile 已清"
[ -f "$LIFE_ST_WD_ARMED" ] && no "L18b armed 没清（面板会显示 watchdog=stale）" || ok "L18b armed 已清"
[ -f "$LIFE_ST_WD_OFF" ] && ok "L18c 关闭意图已落盘（守护若被别处叫醒也会自尽）" \
                         || no "L18c 没写 watchdog-off"

echo "== L16 life_stop_user：停止 = **全停**（引擎 / DNS / 守护）=="
# 真机 2026-10-01：stop-user 只停进程，守护照跑 —— `ops.sh status` 仍回 watchdog=up
# （pid 7296 还活着）。用户点「停止」的语义是"这台机器上属于它的都停"，而界面上
# watchdog=up 与这个语义直接打架。
rm -f "$_TMPD/all.stopped" "$_TMPD/wd.stopped"
life_stop_all() { echo stopped >> "$_TMPD/all.stopped"; echo stopped; }
life_wd_stop() { echo stopped >> "$_TMPD/wd.stopped"; echo stopped; }
life_stop_user >/dev/null 2>&1
[ -f "$_TMPD/all.stopped" ] && ok "L16 停了引擎与 DNS" || no "L16 进程没停"
[ -f "$_TMPD/wd.stopped" ] && ok "L16b 守护也停了" \
                           || no "L16b **守护没被停**（面板 watchdog 仍是 up）"

echo "== L17 life_restart_engine：不得拿"还没死的旧引擎"冒充重启成功 =="
# 真机取证：委托守护重启是**异步**的，守护收到 USR1 才去停旧引擎；而本地立刻
# `wait_for healthy` —— 那一刻旧引擎还在，第一次检查就通过 → 0 秒回 engine=up。
# 界面下一刷新正好落在"旧已停、新未起"的空窗 → 重启被显示成红色，且不刷新就永不恢复。
_ORD17="$_TMPD/restart-order.txt"; : > "$_ORD17"
_old_pid=4242
life_wd_ensure_started() { echo running; }
life_wd_alive() { return 0; }
life_pid_of() { echo "$_old_pid"; }
life_wd_request() { printf 'req ' >> "$_ORD17"; }
wait_gone() { printf 'gone:%s ' "${3:-}" >> "$_ORD17"; return 0; }
life_engine_healthy() { return 0; }
life_dns_healthy() { return 0; }   # 同上：restart 也会等 DNS 到终态
life_restart_engine >/dev/null 2>&1
if grep -q "gone:$_old_pid" "$_ORD17" 2>/dev/null; then
  ok "L17 先等旧引擎真的退出，再判新引擎（不再 0 秒谎报 engine=up）"
else
  no "L17 没等旧引擎退出就判成功（真机表现：耗时 0s 的 engine=up 之后界面转红）"
fi

echo "== L19 life_restart_engine：DNS 必须与引擎**同级别等到终态**（2026-10-01）=="
# 真机：restart 返回那一刻（13s）engine=up 而 dns=down，再过 8s dns 才 up（≈21s）。
# 界面在返回瞬间刷新 → 引擎绿、DNS 红并排，用户以为 DNS 挂了（其实它还在起）。
# 修复：引擎起来后继续等 life_dns_healthy，两项都到终态才返回给界面。
_DNS19="$_TMPD/dns19.calls"; : > "$_DNS19"
life_wd_ensure_started() { echo running; }
life_wd_alive() { return 0; }
life_pid_of() { echo 4242; }
life_wd_request() { :; }
wait_gone() { return 0; }
life_engine_healthy() { return 0; }
life_dns_healthy() {   # 前两次假、第三次真 = 守护还没把 dnsfwd 拉起来的形状
  echo x >> "$_DNS19"
  [ "$(wc -l < "$_DNS19" | tr -d ' ')" -ge 3 ]
}
_out19="$(life_restart_engine)"
[ "$_out19" = "engine=up" ] && ok "L19 两项都到位 → engine=up" \
                            || no "L19 期望 engine=up，实际 [$_out19]"
_c19="$(wc -l < "$_DNS19" | tr -d ' ')"
[ "$_c19" -ge 3 ] && ok "L19b 真的等了 DNS（问了 $_c19 次）" \
                  || no "L19b 一次都没问 DNS（$_c19 次）＝ 返回即刷新会撞上「还在起」的空窗"

echo "== L20 DNS 迟迟不到位：如实回 dns-pending，不许谎报 engine=up =="
# 与 A5/A5b 同一条教训：把"只起来一半"报成成功，用户会照着错的结论去排查。
LIFE_DNS_SETTLE_LIMIT=2          # 下调上限使超时分支可离线重放（生产默认 30s）
life_dns_healthy() { return 1; }
_out20="$(life_restart_engine)"
[ "$_out20" = "dns-pending" ] && ok "L20 DNS 未到终态 → dns-pending（引擎起来了但没谎报全好）" \
                              || no "L20 期望 dns-pending，实际 [$_out20]"
LIFE_DNS_SETTLE_LIMIT=30

echo "== L21 settle 编排只许有一个家（2026-10-01 架构走查候选 1）=="
# 20 行同构原在 start-user / restart-engine 各抄一份（L17/L19 两起事故的温床）。
# 断言形状：等引擎健康的编排只许出现在 life_settle_report（=1），动词只许调用它。
_dup="$(grep '2 life_engine_healthy' module/lib/lifecycle.sh | grep -cv '^[[:space:]]*#')"
[ "$_dup" = "1" ] && ok "L21a 等引擎健康的原语与时限只剩一处（life_wait_engine_ready）" \
                  || no "L21a 又有人手抄等待编排了（$_dup 处，只许 1）"
_call="$(grep -c 'life_settle_report ' module/lib/lifecycle.sh)"
[ "$_call" -ge 2 ] && ok "L21b 两个动词都在调 life_settle_report（$_call 处调用）" \
                   || no "L21b settle 调用点不足（$_call 处，需 ≥2）"
_ens="$(grep -c 'life_ensure_stack' module/lib/lifecycle.sh; grep -c 'life_ensure_stack' module/lib/ops.sh)"
[ "$(echo "$_ens" | awk '{s+=$1} END{print s}')" -ge 3 ] \
  && ok "L21c 拉回清单收进 life_ensure_stack（lifecycle + ops 共用）" \
  || no "L21c life_ensure_stack 收口不完整（lifecycle/ops 各自的计数：$_ens）"

echo "== L22 父子关系：守护必须**直调** ensure，CHLD 才可达（2026-10-02 真机实锤）=="
# bug 形状（T5 红 / T15 实测 60s 的根源）：wd_bring_up 原来写 `x="$(life_ensure_engine)"`
# —— $() 把整个 ensure 放进子 shell，引擎成了那个短命子 shell 的孩子，子 shell 一退出
# 即被 init 收养（真机实测引擎 PPID=1）。后果三连：① 引擎死亡永远没有 SIGCHLD（事件
# 驱动对引擎死亡失效）；② wait 取不到退出码 → 死因取证恒「退出码 127」（假的）；
# ③ eng_ours 非空让守护睡 60s 长周期 —— 自愈从 ~1s 退化成最长 60s。
# (a) 先用 $() 差分锁住 bug 形状（保证本组断言真的能红），(b) 锁 wd_ensure 直调路径。
. module/lib/lifecycle.sh            # 还原 L15–L17 桩掉的真实函数体（重新 source 即还原）
_WD_LIB_ONLY=1 . module/lib/watchdog.sh >/dev/null 2>&1 \
  || no "L22 前置失败：无法 source watchdog.sh（判定核 + wd_ensure）"
printf '#!/bin/sh\nsleep 30\n' > "$_TMPD/fake-engine"; chmod 0755 "$_TMPD/fake-engine"
LIFE_BIN="$_TMPD/fake-engine"
_ppid_of() { awk '{print $4}' "/proc/$1/stat" 2>/dev/null; }
_ve22="$(life_ensure_engine)"        # 旧形态：经 $() 调用
_a22="$(cat "$LIFE_ST_ENGINE" 2>/dev/null)"
if [ -n "$_a22" ] && [ "$(_ppid_of "$_a22")" != "$$" ]; then
  ok "L22a 差分：经 \$() 调用引擎被 init 收养（PPID=$(_ppid_of "$_a22")，bug 形状可观测）"
else
  no "L22a 差分失败：\$() 路径观察不到收养形状（pid=$_a22 ppid=$(_ppid_of "$_a22")）—— 断言失去拦截能力"
fi
kill "$_a22" 2>/dev/null; rm -f "$LIFE_ST_ENGINE"
wd_ensure life_ensure_engine         # 新形态：直调（当前 shell）
_b22="$(cat "$LIFE_ST_ENGINE" 2>/dev/null)"
if [ -n "$_b22" ] && [ "$(_ppid_of "$_b22")" = "$$" ]; then
  ok "L22b wd_ensure 直调 → 引擎 PPID==守护（$$），CHLD 从此可达"
else
  no "L22b wd_ensure 没有产生真子进程（pid=$_b22 ppid=$(_ppid_of "$_b22")，期望 $$）—— 自愈退化回 60s"
fi
if [ "$wd_verdict" = "started" ]; then
  ok "L22c 判定词经 LIFE_ENSURE_VERDICT 文件如实带回（started）"
else
  no "L22c 判定词没带回（wd_verdict=[$wd_verdict]，期望 started）"
fi
kill "$_b22" 2>/dev/null
# (d) 回潮扫描：守护里 ensure **只许直调** —— 任何一处 `$(life_ensure` 都是子 shell 收养回归
# （排除注释行：反例文本写在 wd_ensure 的说明注释里，第一次跑就被它误红了）
_n22="$(grep '\$(life_ensure' module/lib/watchdog.sh | grep -cv '^[[:space:]]*#')"
[ "$_n22" = "0" ] && ok "L22d 守护里 \$() 调 ensure 残留 = 0（直调纪律）" \
                 || no "L22d 出现 $_n22 处 \$(life_ensure…（CHLD 会再次不可达）"
# (e) 行为断言：wd_bring_up 必须**消费**判定词记账（S3：只有亲手拉起的才 eng_ours）
# 变异自证：把 wd_bring_up 改回 \$() 后，_bu_ee 恒空 → eng_ours 不记账 → 此条精确变红
_p22() { echo 5551; }
life_ensure_engine() { life_verdict_emit "started"; }
life_engine_pid()     { echo 5551; }
life_engine_healthy() { return 0; }
wait_engine()         { return 0; }
mark_engine_alive()   { :; }
log()                 { :; }
eng_ours=""
wd_bring_up 测试成功 测试失败
[ "$eng_ours" = "5551" ] && ok "L22e wd_bring_up 消费判定词 → eng_ours 记账（S3 纪律生效）" \
                         || no "L22e 判定词没被消费（eng_ours=[$eng_ours]）—— 亲手拉起的引擎丢了归属"

echo "== 结果：通过 $PASS / 失败 $FAIL / 跳过 $SKIP =="
[ "$FAIL" = 0 ] || exit 1
exit 0
