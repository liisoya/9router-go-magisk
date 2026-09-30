#!/system/bin/sh
# lifecycle.sh — 服务生命周期的唯一所有者（可 source 的库；source 时零副作用）
#
# 为什么有这个 module（ADR-0005）：「服务该不该在跑、谁把它拉起来、用户是不是要它停」
# 这个事实原先被编码成 6 个状态文件，由 ops.sh / watchdog.sh / service.sh / uninstall.sh
# 四方各自读写，优先级只靠 if 的书写顺序表达 —— 已经因此出过 bug（restart-engine 删掉
# 用户的 watchdog-off，用户关不掉守护；watchdog-start 与 pidfile 落盘的竞态把重启退化成
# "在调用者 cgroup 里启动"）。现在状态文件是本文件的**实现细节**，调用方只表达意图。
#
# 接口（调用方只需要知道这些动词；加粗项是给用户表达意图的，其余是给守护/运维用的）：
#   意图：life_boot            开机：清"用户关闭"、武装并启动守护（只能在 init/ksud 上下文）
#         life_stop_user       用户显式停服务：停进程 + 记住意图（守护不再复活）
#         life_start_user      用户显式启服务：清意图 + 拉起
#         life_restart_engine  停 →（有守护则委托它）→ 起，内置等待，echo engine=up|down
#         life_stop_all [--user]
#   幂等：life_ensure_engine / life_ensure_dns（在跑就返回 running，尊重用户意图）
#   谓词：life_engine_healthy / life_dns_healthy / life_wd_should_supervise / life_wd_take_request
#   只读：life_state（机器可读一行，供 ops.sh status 组合）
#   准备：life_prep（引擎启动前必须为真的东西：schema / autoUpdate 闸门 / 密码 / DNS 兜底）
#
# 前置约定：调用方必须先给出 MODDIR（本库不猜模块路径 —— 猜错过一次：
# uninstall.sh 硬编码 /modules/ninerouter-go）。DATA_DIR 缺省 /data/adb/9router-go。
[ -n "${MODDIR:-}" ] || { echo "lifecycle.sh: 必须先设置 MODDIR（调用方自己知道模块路径）" >&2; return 1 2>/dev/null || exit 1; }

DATA_DIR="${DATA_DIR:-/data/adb/9router-go}"
# 日志策略（路径/上限/轮转）在 lib/log.sh —— 唯一所有者；本库只经它写日志
. "$MODDIR/lib/log.sh" || { echo "lifecycle.sh: 缺 lib/log.sh" >&2; return 1 2>/dev/null || exit 1; }
# 「等就绪 / 等消失」的唯一实现在 lib/wait.sh（本库、守护、真机门禁共用；零依赖）
. "$MODDIR/lib/wait.sh" || { echo "lifecycle.sh: 缺 lib/wait.sh" >&2; return 1 2>/dev/null || exit 1; }
LIFE_DB="$DATA_DIR/db/data.sqlite"
LIFE_SQLITE3="$MODDIR/bin/sqlite3"
LIFE_SCHEMA="$MODDIR/etc/schema.sql"
LIFE_BIN="$MODDIR/bin/9router-go"
LIFE_DNSFWD="$MODDIR/bin/dnsfwd"
LIFE_UPSTREAMS="$DATA_DIR/dns-upstreams.conf"
LIFE_DNS_BIND_FILE="$DATA_DIR/dns-bind"
LIFE_WATCHDOG="$MODDIR/lib/watchdog.sh"
LIFE_RUNTIME_ENV="$DATA_DIR/runtime.env"
# ── 状态文件（实现细节：只有本文件读写；别的文件不该知道这些名字）──
LIFE_ST_ENGINE="$DATA_DIR/9router.pid"
LIFE_ST_DNS="$DATA_DIR/dnsfwd.pid"
LIFE_ST_DNS_OFF="$DATA_DIR/dns-disabled"
LIFE_ST_USER_OFF="$DATA_DIR/service-off"
LIFE_ST_WD_PID="$DATA_DIR/watchdog.pid"
LIFE_ST_WD_ARMED="$DATA_DIR/watchdog-armed"
LIFE_ST_WD_HOLD="$DATA_DIR/watchdog-hold"
LIFE_ST_WD_REQ="$DATA_DIR/watchdog.req"
LIFE_ST_WD_OFF="$DATA_DIR/watchdog-off"
LIFE_WD_LOG="$LOG_WATCHDOG_PATH"
LIFE_DNS_LOG="$LOG_DNS_PATH"

# ── 原语 ────────────────────────────────────────
life_pid_alive() {
  # 用 shell **内建 read** 读 pid 文件，不用 `$(cat ...)`（那是一次 fork + 子 shell）。
  # 为什么在意：守护每 5s 一轮、每轮要问两次"引擎/dnsfwd 还在不在" —— 实测优化前
  # 9.17ms/轮 ≈ 158s CPU/天（0.18% 单核）、17280 次唤醒/天，而这些 CPU 全花在
  # "为读几个字节而 fork"上。真机 A/B：见 docs/FIXPLAN.md Phase 33.11。
  # 语义与原先一致（空文件/缺文件/非数字 → 不在），额外顺带更稳：多行 pid 文件只取首行。
  # 变量名刻意用 _alive_* 前缀：本库靠全局变量传值，不得与调用方的 _p/_f 冲突。
  _alive_f="$1"
  [ -f "$_alive_f" ] || return 1
  _alive_p=""
  # **重定向顺序要紧**：`2>/dev/null` 必须在 `< file` 之前 —— shell 从左到右处理重定向，
  # 写成 `read v < file 2>/dev/null` 时重定向失败的错误会先报在终端上（真机实测：
  # 刚死的进程会在 `ops.sh panel` 的输出里混进 "can't open /proc/<pid>/stat"，
  # 而那份输出是按键值解析的）。
  read -r _alive_p 2>/dev/null < "$_alive_f" || return 1
  case "$_alive_p" in ''|*[!0-9]*) return 1 ;; esac
  # 僵尸态不算"活着"（见 life_stat_is_zombie）：读 /proc/<pid>/stat 仍是内建 read，0 次 fork
  _alive_s=""
  read -r _alive_s 2>/dev/null < "/proc/$_alive_p/stat" || return 1
  life_stat_is_zombie "$_alive_s" && return 1
  kill -0 "$_alive_p" 2>/dev/null
}
life_stat_is_zombie() {
  # /proc/<pid>/stat 的 state 字段（第 3 段）为 Z = 僵尸（已退出、待回收）。
  # 为什么单独成函数：**为了让它能被离线断言** —— 僵尸窗口在测试机上转瞬即逝
  # （shell 会立刻回收子进程），抽成纯字符串判定后就能用真实样本把解析锁住。
  # 为什么不用"按空格切第 3 段"：comm 字段可能含空格/括号（如 `(a b)`），切字段会错位；
  # 所以匹配形状 —— **右括号 + 空格 + Z + 空格**。
  case "${1:-}" in *") Z "*) return 0 ;; esac
  return 1
}
life_pid_of() { cat "$1" 2>/dev/null; }
life_pid_gone() {
  # "已经退出"判定（含僵尸态）—— 实现唯一所有者在 lib/wait.sh，这里只保留历史名字给既有调用方
  wait_pid_gone "${1:-}"
}
life_cgroup_of() { sed -n 's/^0:://p' "/proc/$1/cgroup" 2>/dev/null; }
life_pid_is_exe() {
  # 进程身份校验：pidfile 里的号可能被无关进程复用（Android pid_max 不大，长跑会绕回）。
  # 杀之前必须确认"这就是我们的二进制"。模块更新后二进制被 mv 掉，exe 链接会带 " (deleted)"。
  _pid="$1"; _want="$2"
  case "$_pid" in ''|*[!0-9]*) return 1 ;; esac
  _exe="$(readlink "/proc/$_pid/exe" 2>/dev/null)"
  case "$_exe" in "$_want"|"$_want (deleted)") return 0 ;; esac
  return 1
}
life_pid_is_watchdog() {
  # 守护身份校验：它是个 **shell 脚本**，/proc/<pid>/exe 指向 sh 而不是脚本本身，
  # 所以 life_pid_is_exe 对它无效，只能按 cmdline 认（真机实测：cmdline 为
  # "/system/bin/sh" + "<MODDIR>/lib/watchdog.sh"）。
  # 为什么需要：pidfile 里的号会被无关进程复用（长跑 pid_max 会绕回），"活着"不等于
  # "我们的守护还活着" —— 少了这层，面板会误报 up、且永不重新拉起。
  # 匹配用"完整路径 or 任意路径下的同名脚本"：模块目录经符号链接/相对路径到达时，
  # 严格全路径匹配会假死（宁可宽松一点，也不能把活着的守护判成不在）。
  _pid="${1:-}"
  case "$_pid" in ''|*[!0-9]*) return 1 ;; esac
  [ "$_pid" -ge 1 ] || return 1
  _cl="$(tr '\0' '\n' 2>/dev/null < "/proc/$_pid/cmdline")"
  case "$_cl" in
    *"$LIFE_WATCHDOG"*) return 0 ;;
    *"/watchdog.sh"*)   return 0 ;;
  esac
  return 1
}
life_pid_file_is_bin() {
  # pidfile → 进程身份校验，供"活着"类谓词使用（与 life_wd_alive → life_pid_is_watchdog
  # 同一条纪律）。**为什么必须有（2026-09-30 真机事故）**：用户设备上引擎早已死掉、pid 被无关
  # 进程占用（82 分钟推进了 12943 个号），而面板照报 engine=up、守护 39 分钟不判死不拉起 ——
  # 自愈形同不存在；连面板显示的"引擎内存 571.7MB"都是**那个无关进程**的内存。守护侧早有这层
  # （注释里写明"少了这层，面板会误报 up、且永不重新拉起"），引擎/dnsfwd 这半边一直缺。
  # **安全偏向（与守护一致，宁松勿严）**：
  #   ① 按 basename 松匹配 —— 模块目录可能经符号链接/相对路径到达，严格全路径匹配会假死
  #      （把活着的引擎判成不在 → 守护反复重启，杀掉用户正在用的代理）；
  #   ② 两条身份信息**都读不到**时判"在" —— 读不到（SELinux/权限/tr 不可用）≠ 不是它。
  # 读 pid 用内建 read（热路径 0 次额外 fork，与 life_pid_alive 同款）；唯一的一次 fork 是
  # readlink，且只在进程确实活着时发生。
  _b_f="${1:-}"; _b_want="${2:-}"
  [ -n "$_b_f" ] && [ -n "$_b_want" ] || return 1
  [ -f "$_b_f" ] || return 1
  _b_p=""
  read -r _b_p 2>/dev/null < "$_b_f" || return 1
  case "$_b_p" in ''|*[!0-9]*) return 1 ;; esac
  [ "$_b_p" -ge 1 ] || return 1
  _b_bn="${_b_want##*/}"
  # ① exe 链接：真二进制走这条；模块更新后二进制被 mv 掉 → 链接带 " (deleted)"，也算它
  _b_exe="$(readlink "/proc/$_b_p/exe" 2>/dev/null)"
  case "$_b_exe" in
    */"$_b_bn"|*/"$_b_bn (deleted)") return 0 ;;
  esac
  # ② cmdline 指纹：按「完整参数段」匹配，**不能裸子串** —— WebUI 高频跑
  #   `sqlite3 …/9router-go/db/…`、`sh …/lib/ops.sh status`，cmdline 都含 "9router-go"
  #   子串；引擎刚死、pid 恰被这类短命进程复用时，裸子串会把它们认成引擎 →
  #   Phase 37 要修的误报换个方向复发（架构审查 S5）。
  _b_cl="$(tr '\0' '\n' 2>/dev/null < "/proc/$_b_p/cmdline")"
  _b_hit=0
  while IFS= read -r _b_arg; do
    case "$_b_arg" in "$_b_want"|*/"$_b_bn"|*/"$_b_bn (deleted)") _b_hit=1 ;; esac
  done <<EOF
$_b_cl
EOF
  [ "$_b_hit" = "1" ] && return 0
  # ③ 两条身份信息都读到了、但都不是它 → pid 已被复用（上面那个误报的来源）
  if [ -n "$_b_exe$_b_cl" ]; then return 1; fi
  # ④ 什么都读不到 → 宽松：不推翻 pidfile（宁可漏判一次复用，不可错杀活着的引擎）
  return 0
}
life_cgroup_memory_events() {
  # cgroup 路径（life_cgroup_of 的输出："0::" 之后的部分，如 "/" 或 "/apps/uid_1/pid_2"）
  # → 该组的 memory.events 路径。为什么单独成纯函数：**为了能离线断言** —— 路径拼错或越权
  # 拼接这类错误，真机上只会表现为"读不到"（然后被当成内核不支持而漏过），离线可以精确锁住。
  # 防御：必须以 / 开头、不得含 ".."（值虽来自 /proc 的内核输出，拼接仍按最坏情况设防）。
  _cme_c="${1:-}"
  case "$_cme_c" in
    /|/*) ;;
    *) return 1 ;;
  esac
  case "$_cme_c" in *..*) return 1 ;; esac
  echo "/sys/fs/cgroup${_cme_c%/}/memory.events"
}
life_oom_kill_count() {
  # memory.events 里的 oom_kill 计数。读不到（文件缺失/旧内核/没有这一行）→ 如实失败，
  # **不冒充 0** —— "没读到"和"确实是 0"是两回事（同 lib/ops.sh 那条读数纪律）。
  _ok_f="${1:-}"
  [ -n "$_ok_f" ] || return 1
  _ok_v="$(sed -n 's/^oom_kill //p' "$_ok_f" 2>/dev/null | tail -n 1)"
  case "$_ok_v" in ''|*[!0-9]*) return 1 ;; esac
  echo "$_ok_v"
}
life_cgroup_escape() {
  # 把进程从"启动者的 cgroup"里挪出去（连坐免疫）。真机实测：WebUI 经 ksu.exec 启动的
  # 进程落在管理器应用的 cgroup（0::/uid_10235/pid_X），系统清理该应用时整组 SIGKILL；
  # setsid/nohup 只换会话不换 cgroup，救不了。
  _pid="${1:-}"
  case "$_pid" in ''|*[!0-9]*) return 1 ;; esac
  [ -r "/proc/$_pid/cgroup" ] || return 1
  _cur="$(life_cgroup_of "$_pid")"
  case "$_cur" in
    '') return 1 ;;                 # 非 cgroup v2（拿不到统一层级）→ 如实返回失败
    '/') return 0 ;;                # 已在根 cgroup（init/ksud 上下文，天然免疫）
  esac
  for _r in /sys/fs/cgroup /dev/cgroup; do
    [ -w "$_r/cgroup.procs" ] || continue
    echo "$_pid" > "$_r/cgroup.procs" 2>/dev/null && return 0
  done
  return 1
}
life_oom_protect() {
  # 显式设定 OOM 优先级（第二个参数，-1000 = 内存压力下不可被杀）。
  #
  # 为什么必须**显式**：真实值是**继承**来的，取决于谁启动了它。2026-09-29 真机实测：
  # 引擎/dnsfwd/守护三者都是 -1000（从 init/adbd 继承），而这不是任何人的选择 ——
  # 后果是内存压力下内核杀不动模块进程，只能去杀系统里其他可杀进程（用户报告：
  # "模块内存涨到 100+MB，然后系统的进程都挂掉了"）。策略必须是声明出来的：
  #   · 守护 = -1000（唯一自愈者，它被杀 = 整机失去自愈能力）
  #   · 其余进程的取值由调用方决定，不在这里替它做主
  _pid="${1:-}"; _adj="${2:-}"
  case "$_pid" in ''|*[!0-9]*) return 1 ;; esac
  [ "$_pid" -ge 1 ] || return 1
  _n="${_adj#-}"
  case "$_n" in ''|*[!0-9]*) return 1 ;; esac
  [ "$_n" -le 1000 ] || return 1
  [ -w "/proc/$_pid/oom_score_adj" ] || return 1
  echo "$_adj" > "/proc/$_pid/oom_score_adj" 2>/dev/null || return 1
  return 0
}
life_rss_kb() {
  # /proc/<pid>/status 的 VmRSS（kB）；读不到就失败且不输出 —— 不冒充 0
  # （与"读失败不冒充 0"同一条纪律，见 Phase 0.2）
  _pid="${1:-}"
  case "$_pid" in ''|*[!0-9]*) return 1 ;; esac
  [ "$_pid" -ge 1 ] || return 1
  _r="$(awk '/^VmRSS:/{print $2}' "/proc/$_pid/status" 2>/dev/null)"
  case "$_r" in ''|*[!0-9]*) return 1 ;; esac
  echo "$_r"
}
# 内存证据策略（唯一所有者）：阈值与两条节奏都只在这里声明，守护只调用 life_rss_log_due
# **单位是秒，不是轮次**：轮次会随 poll 间隔变（5s→60s 时"每 12 轮"会从 60 秒悄悄变成
# 12 分钟），把意图静默改掉 —— 这正是本仓库最忌讳的那类 bug，所以口径钉死在秒。
LIFE_RSS_WARN_KB=102400      # 100MB —— 用户报障的量级（面板告警线是 200/300MB）
LIFE_RSS_WARN_GAP=720        # 超阈值时，两行之间至少隔 12 分钟
LIFE_RSS_BASE_GAP=3600       # 基线：每 1 小时无条件记一行
life_rss_log_due() {
  # "这一轮该不该记内存" —— 纯判定，方便离线断言（守护的循环本身没法离线跑）。
  #   $1 = 当前 RSS(kB，可空)  $2 = 已运行秒数  $3 = 上次记录的秒数
  #   输出 1 = 该记 / 0 = 不必
  # 为什么除了阈值还要有基线：只记"超 100MB"会漏掉"缓慢爬到 90MB"这种**最需要证据**的形态
  # （真机那份报障正是"跑一段时间涨到 100+MB"）。基线每小时一行，趋势在任何量级都连得上。
  _due_r="${1:-}"; _due_t="${2:-}"; _due_last="${3:-0}"
  case "$_due_r" in ''|*[!0-9]*) echo 0; return 0 ;; esac
  case "$_due_t" in ''|*[!0-9]*) _due_t=0 ;; esac
  case "$_due_last" in ''|*[!0-9]*) _due_last=0 ;; esac
  _due_gap=$((_due_t - _due_last))
  if [ "$_due_r" -ge "$LIFE_RSS_WARN_KB" ] && [ "$_due_gap" -ge "$LIFE_RSS_WARN_GAP" ]; then echo 1; return 0; fi
  [ "$_due_gap" -ge "$LIFE_RSS_BASE_GAP" ] && { echo 1; return 0; }
  echo 0
}
life_port53_busy() {
  # 优先 ss（Android netstat 对 UDP 监听展示不可靠），netstat 兜底
  if command -v ss >/dev/null 2>&1; then
    ss -tuln 2>/dev/null | grep -qE '[:.]53[[:space:]]'
  else
    netstat -tuln 2>/dev/null | grep -qE '[:.]53[[:space:]]'
  fi
}
life_read_bind() {
  _b="$(cat "$LIFE_DNS_BIND_FILE" 2>/dev/null | tr -d ' \n')"
  [ "$_b" = "any" ] && echo any || echo loopback
}
life_get_port() {
  p=""
  if [ -z "${PORT:-}" ] && [ -f "$DATA_DIR/port" ]; then
    p="$(cat "$DATA_DIR/port" 2>/dev/null | tr -d ' \n')"
    case "$p" in ''|*[!0-9]*) p= ;; esac
    if [ -n "$p" ] && [ "$p" -ge 1 ] && [ "$p" -le 65535 ]; then PORT="$p"; fi
  fi
  echo "${PORT:-20130}"
}
life_log() { log_write engine "[$(date '+%F %T')] $*"; }

# ── 引擎运行环境（承载性 env 的唯一声明）──────────────
# 「引擎需要哪些 env 才能正常工作」原先只活在 export 语句与散文注释里，其中两个是**承载性**的：
# 没有 SSL_CERT_DIR 则所有 HTTPS 与更新检查全废；没有 AUTO_UPDATE=false 则引擎会按 DB 开启
# 自更新、绕过模块管理。现在清单是唯一声明，产出物是 $DATA_DIR/runtime.env（0600，含密码），
# 启动前 source 它；门禁 T7 逐条断言"键在文件里 + 真的进了引擎进程"。
life_env_q() {
  # 单引号安全包裹（值里带空格/引号也不会破坏 runtime.env 的语法）
  printf "'%s'" "$(printf '%s' "${1:-}" | sed "s/'/'\\\\''/g")"
}
life_carrier_env_keys() {
  # 唯一声明：这些键缺失 = 引擎不可用或不受管
  cat <<'EOF'
SSL_CERT_DIR
AUTO_UPDATE
PORT
DATA_DIR
MODDIR
INITIAL_PASSWORD
EOF
}
life_carrier_env_value() {
  # 承载性 env 的**取值唯一来源**（键 → 值）。
  # 分工（2026-09-30 架构扫描 C6 重整）：`life_carrier_env_keys` 决定**有哪些键**，
  # 本函数决定**每个键的值是什么**，`life_write_runtime_env` 只按清单取值写出。
  # 此前是"清单 + 六行 echo + 内联 export"三份并行实现：改一处忘另一处，离线看不见，
  # 只有真机 T7 能兜（而 T7 要有设备）。现在离线档 L13 断言"写出物键集合 == 清单"。
  case "${1:-}" in
    SSL_CERT_DIR)     printf '%s' /system/etc/security/cacerts ;;
    AUTO_UPDATE)      printf '%s' false ;;
    PORT)             life_get_port ;;
    DATA_DIR)         printf '%s' "$DATA_DIR" ;;
    MODDIR)           printf '%s' "$MODDIR" ;;
    INITIAL_PASSWORD) cat "$DATA_DIR/initial-password" 2>/dev/null ;;
    *) return 1 ;;
  esac
}
life_carrier_env_export() {
  # 内联兜底用：把清单里的每个键按取值函数导出给子进程（不再手写 export 列表 —— 那正是第三份实现）。
  # **值可以为空，但键必须齐**：键集合恒等于清单（离线 L13 断言）—— 取不到值时"少一个键"
  # 会让引擎少一份承载性 env（例如没有 SSL_CERT_DIR 则所有 HTTPS 全废），比空值危险得多。
  for _k in $(life_carrier_env_keys); do
    _v="$(life_carrier_env_value "$_k" 2>/dev/null)"
    export "$_k=$_v"
  done
}
life_write_runtime_env() {
  {
    echo "# 由 lib/lifecycle.sh 生成（勿手改）：引擎运行环境（承载性 env 的单一来源）"
    for _k in $(life_carrier_env_keys); do
      _v="$(life_carrier_env_value "$_k" 2>/dev/null)"
      echo "$_k=$(life_env_q "$_v")"
    done
  } > "$LIFE_RUNTIME_ENV.tmp" 2>/dev/null || { rm -f "$LIFE_RUNTIME_ENV.tmp"; return 1; }
  # 原子落盘（与 webroot/bridge.js 的 writeFile 同一条纪律，架构审查 S8）：写一半被杀只会
  # 留下 .tmp，不会留下半份 runtime.env 等着下次被 source —— 轻则语法错、重则 PORT 缺失
  # 导致引擎端口漂移。
  mv "$LIFE_RUNTIME_ENV.tmp" "$LIFE_RUNTIME_ENV" 2>/dev/null || { rm -f "$LIFE_RUNTIME_ENV.tmp"; return 1; }
  chmod 600 "$LIFE_RUNTIME_ENV" 2>/dev/null
  echo "$LIFE_RUNTIME_ENV"
}
life_load_runtime_env() {
  # set -a 让文件里的赋值全部导出给子进程（引擎）
  if [ -r "$LIFE_RUNTIME_ENV" ]; then
    set -a
    . "$LIFE_RUNTIME_ENV"
    set +a
    return 0
  fi
  return 1
}

# ── 用户意图 ────────────────────────────────────
life_user_stopped() { [ -f "$LIFE_ST_USER_OFF" ]; }
life_wd_disabled() { [ -f "$LIFE_ST_WD_OFF" ]; }
life_wd_armed() { [ -f "$LIFE_ST_WD_ARMED" ]; }
life_wd_alive() {
  # "活着"必须同时"是我们的守护"：pidfile 里的号会被无关进程复用（见 life_pid_is_watchdog）。
  # 少了这层，"系统里恰好有个同号进程"会让面板永久误报 up，且永远不再拉起守护。
  life_pid_alive "$LIFE_ST_WD_PID" && life_pid_is_watchdog "$(life_pid_of "$LIFE_ST_WD_PID")"
}
life_wd_hold_active() {
  # 维护窗口用**绝对到期时间**：调用方崩了也不会把守护永久卡死
  [ -f "$LIFE_ST_WD_HOLD" ] || return 1
  _exp="$(cat "$LIFE_ST_WD_HOLD" 2>/dev/null | tr -d ' \n')"
  case "$_exp" in ''|*[!0-9]*) rm -f "$LIFE_ST_WD_HOLD"; return 1 ;; esac
  [ "$(date +%s)" -lt "$_exp" ] && return 0
  rm -f "$LIFE_ST_WD_HOLD"
  return 1
}
life_wd_should_supervise() {
  # 守护每轮只问这一个问题：现在该不该插手？
  life_wd_armed || return 1
  life_wd_disabled && return 1
  life_user_stopped && return 1
  life_wd_hold_active && return 1
  return 0
}
life_wd_hold() {
  _sec="${1:-180}"
  case "$_sec" in ''|*[!0-9]*) _sec=180 ;; esac
  echo "$(( $(date +%s) + _sec ))" > "$LIFE_ST_WD_HOLD" 2>/dev/null
}
life_wd_hold_release() { rm -f "$LIFE_ST_WD_HOLD"; }
life_wd_request() {
  printf '%s\n' "${1:-start}" > "$LIFE_ST_WD_REQ" 2>/dev/null
  life_wd_notify
}
life_engine_pid()   { life_pid_of "$LIFE_ST_ENGINE"; }
life_dns_pid()      { life_pid_of "$LIFE_ST_DNS"; }
life_watchdog_pid() { life_pid_of "$LIFE_ST_WD_PID"; }
life_wd_notify() {
  # 写完请求后**立刻叫醒**守护，不等下一个轮询周期（FIXPLAN Phase 33.12）。
  # 为什么这不是"可选优化"而是必需：守护间隔放宽到 60s 后，life_restart_engine 的
  # `wait_for 20 2 life_engine_healthy` 会先超时（守护最多 60s 才看到请求文件）
  # → 面板/CLI 报"重启失败"，而真相只是"没被叫醒"。
  # 机制：USR1 —— 真机验证过它能打断被 sleep 阻塞的循环（3 轮实验见 Phase 33.12）。
  # **安全前提**：pidfile 可能陈旧（pid 被复用），而 USR1 的默认动作是**终止**进程 ——
  #  所以必须先确认那确实是我们自己的守护（cmdline 指纹），否则会杀掉无辜进程。
  _nt_p="$(life_pid_of "$LIFE_ST_WD_PID" 2>/dev/null)"
  case "$_nt_p" in ''|*[!0-9]*) return 1 ;; esac
  life_pid_is_watchdog "$_nt_p" || return 1
  kill -USR1 "$_nt_p" 2>/dev/null
}
life_exit_reason() {
  # wait 的返回值 → 人话。**128+N = 被信号 N 杀死**，其余是退出码。
  # 为什么要有它：这是"引擎为什么又死了"的唯一客观证据来源，而它只有守护在
  # "引擎是自己的子进程"时拿得到（wait 只能取自己子进程的状态）。纯函数，可离线断言。
  _er_c="${1:-}"
  case "$_er_c" in
    ''|*[!0-9]*) echo "原因未知（不是本守护的子进程）" ;;
    0)   echo "正常退出" ;;
    129) echo "被 SIGHUP 杀" ;;
    130) echo "被 SIGINT 杀" ;;
    134) echo "SIGABRT（自身中止）" ;;
    137) echo "被 SIGKILL 杀（kill -9 / 内存回收 / 连坐清理）" ;;
    139) echo "SIGSEGV（自身段错误）" ;;
    143) echo "被 SIGTERM 停（优雅停止）" ;;
    *) if [ "$_er_c" -gt 128 ]; then echo "被信号 $((_er_c - 128)) 杀"; else echo "退出码 $_er_c"; fi ;;
  esac
}
life_wd_take_request() {
  # 取走即删（消费语义）；请求**优先于** hold —— 运维重启必须能立刻生效
  [ -s "$LIFE_ST_WD_REQ" ] || return 0
  _c="$(tr -d ' \n' < "$LIFE_ST_WD_REQ" 2>/dev/null)"
  rm -f "$LIFE_ST_WD_REQ"
  echo "$_c"
}
life_wd_announce() {
  # 守护进程登记自己的 pid（它不该知道状态文件名 —— 由本库代劳）
  echo "$$" > "$LIFE_ST_WD_PID" 2>/dev/null
}
life_wd_retire() {
  # 守护体面退场：清掉自己的 pidfile 与"关闭"标志（标志不该永久留在数据目录）
  rm -f "$LIFE_ST_WD_PID" "$LIFE_ST_WD_OFF"
}
life_shutdown() {
  # 卸载语义：记住"用户要它停" + 关守护 + 停进程（保留数据目录）
  printf 'stop\n' > "$LIFE_ST_USER_OFF"
  printf 'off\n' > "$LIFE_ST_WD_OFF"
  life_stop_all >/dev/null
  rm -f "$LIFE_ST_WD_PID" "$LIFE_ST_WD_HOLD" "$LIFE_ST_WD_REQ"
  echo "shutdown"
}
life_wd_start() {
  # 守护只允许武装后启动：非开机上下文起的守护会随启动者一起被系统清掉，等于没守护
  life_wd_armed || { echo "not-armed"; return 0; }
  life_wd_alive && { echo "running"; return 0; }
  if command -v setsid >/dev/null 2>&1; then
    setsid "$LIFE_WATCHDOG" >>"$LIFE_WD_LOG" 2>&1 &
  else
    "$LIFE_WATCHDOG" >>"$LIFE_WD_LOG" 2>&1 &
  fi
  _wpid=$!
  life_cgroup_escape "$_wpid"
  # 守护 OOM 免疫必须**显式**设：真实值是从启动者继承的（2026-09-29 真机实测 adbd/init
  # 都恰好是 -1000），"碰巧"不能当保障 —— 换个启动路径就可能变成可杀。
  # 失败不阻塞启动（守护仍在跑），但如实写日志，绝不静默。
  life_oom_protect "$_wpid" -1000 || life_log "watchdog: oom_score_adj 设置失败（内存压力下守护可能被杀）"
  # setsid 是异步的，pidfile 要下一拍才落盘：等它就位再返回，否则调用方会误判"守护不在"
  # 而退回本地启动 —— 那正好把引擎放回调用者的 cgroup。
  # 轮询语义唯一实现在 lib/wait.sh（30×0.1s ≈ 3s）
  wait_for 30 0.1 life_wd_alive && { echo "started"; return 0; }
  echo "start-failed"
}

# ── 引擎 ────────────────────────────────────────
life_engine_healthy() {
  # "活着"必须同时"是我们的引擎"（与 life_wd_alive 同一条纪律，见 life_pid_file_is_bin）。
  # 少了这层的代价（2026-09-30 真机实测）：引擎死掉后 pid 被无关进程复用 → 面板永久误报
  # engine=up、守护永不重新拉起（自愈失效），面板上的"引擎内存"其实是别人进程的内存。
  life_pid_alive "$LIFE_ST_ENGINE" && life_pid_file_is_bin "$LIFE_ST_ENGINE" "$LIFE_BIN"
}

life_prep() {
  # 引擎启动前必须为真的一切（幂等）：schema 引导、autoUpdate 压回 false、DNS 上游兜底、
  # 初始密码。输出 ok / degraded —— **绝不谎报成功**（schema 没建 = 引擎报 no such table；
  # 自更新闸门开着 = 更新绕过模块管理）。
  _rc=ok
  mkdir -p "$DATA_DIR/db" 2>/dev/null
  if [ -x "$LIFE_SQLITE3" ] && [ -f "$LIFE_SCHEMA" ]; then
    if ! "$LIFE_SQLITE3" "$LIFE_DB" "SELECT 1 FROM settings LIMIT 1;" >/dev/null 2>&1; then
      if "$LIFE_SQLITE3" "$LIFE_DB" < "$LIFE_SCHEMA" 2>>"$LOG_ENGINE_PATH"; then
        life_log "prep: schema bootstrap applied to $LIFE_DB"
      else
        _rc=degraded
        life_log "prep: schema 引导失败（引擎可能报 no such table）"
      fi
    fi
  fi
  if [ -x "$LIFE_SQLITE3" ] && [ -s "$LIFE_DB" ]; then
    _SQL='UPDATE settings SET data = json_set(data, '\''$.autoUpdate'\'', json('\''false'\'')) WHERE json_type(data, '\''$.autoUpdate'\'') IS NOT NULL;'
    if ! "$LIFE_SQLITE3" "$LIFE_DB" "$_SQL" 2>>"$LOG_ENGINE_PATH"; then
      _rc=degraded
      life_log "prep: autoUpdate 压回 false 失败（引擎会按 DB 开启自更新，绕过模块管理）"
    fi
  fi
  if [ ! -s "$LIFE_UPSTREAMS" ]; then
    {
      echo "# 9router-go 生成：公共 DNS 兜底（无优选）"
      echo "nameserver 223.5.5.5"
      echo "nameserver 119.29.29.29"
      echo "nameserver 1.1.1.1"
    } > "$LIFE_UPSTREAMS.tmp" 2>/dev/null || rm -f "$LIFE_UPSTREAMS.tmp"
    # 原子落盘（架构审查 S8）：半份 upstreams 会让 dnsfwd 起不来或解析到坏上游
    [ -s "$LIFE_UPSTREAMS.tmp" ] && { mv "$LIFE_UPSTREAMS.tmp" "$LIFE_UPSTREAMS" 2>/dev/null || rm -f "$LIFE_UPSTREAMS.tmp"; }
  fi
  if [ ! -f "$DATA_DIR/initial-password" ]; then
    printf '123456\n' > "$DATA_DIR/initial-password"
    chmod 600 "$DATA_DIR/initial-password"
  fi
  echo "$_rc"
}

life_ensure_engine() {
  # 引擎启动的唯一实现（service.sh / 守护 / WebUI 重启 / 更新后重启都走这里）
  life_user_stopped && { echo "off-by-user"; return 0; }
  if life_engine_healthy; then
    life_cgroup_escape "$(life_pid_of "$LIFE_ST_ENGINE")"   # 已在跑的那个也补一次逃逸
    echo "running"; return 0
  fi
  _prep="$(life_prep)"
  [ "$_prep" = "ok" ] || life_log "ensure-engine: prep=$_prep（后果见上方日志）"

  # 承载性 env 从唯一来源加载（C3）：runtime.env 由 life_write_runtime_env 生成，
  # 清单是 life_carrier_env_keys。生成失败才退回内联（不静默降级）。
  if life_write_runtime_env >/dev/null && life_load_runtime_env; then
    : 
  else
    life_log "ensure-engine: runtime.env 生成/加载失败，退回内联 env"
    # 内联兜底也**按清单导出**（此前手写 5 条 export —— 那是承载性 env 的第三份实现）
    life_carrier_env_export
  fi

  life_log "ensure-engine: port=$PORT caller=${LIFE_CALLER:-shell} cgroup=$(life_cgroup_of self)"
  if command -v setsid >/dev/null 2>&1; then
    setsid "$LIFE_BIN" >>"$LOG_ENGINE_PATH" 2>&1 &
  else
    "$LIFE_BIN" >>"$LOG_ENGINE_PATH" 2>&1 &
  fi
  _p=$!
  echo "$_p" > "$LIFE_ST_ENGINE"
  if life_cgroup_escape "$_p"; then
    life_log "ensure-engine: pid=$_p 已迁出启动者 cgroup"
  else
    life_log "ensure-engine: pid=$_p cgroup 迁移未成功（依赖守护兜底）"
  fi
  echo "started"
}

life_stop_engine() {
  # 先按 pidfile 精确杀（**并校验身份**：pid 可能已被别的进程复用），再按"整条命令行就是
  # 我们的二进制"兜底（pidfile 失联时的孤儿）。用 ^...$ 锚定：裸子串匹配会误杀任何命令行
  # 里含该路径的进程。
  _p="$(life_pid_of "$LIFE_ST_ENGINE")"
  if [ -n "$_p" ] && kill -0 "$_p" 2>/dev/null; then
    if life_pid_is_exe "$_p" "$LIFE_BIN"; then kill "$_p" 2>/dev/null
    else life_log "stop-engine: pidfile 里的 $_p 不是本模块引擎，跳过（防误杀）"; fi
  fi
  for p in $(pgrep -f "^$LIFE_BIN\$" 2>/dev/null); do
    [ "$p" != "$$" ] && [ "$p" != "${PPID:-}" ] && kill "$p" 2>/dev/null
  done
  rm -f "$LIFE_ST_ENGINE"
}

# ── dnsfwd ──────────────────────────────────────
life_dns_disabled() { [ -f "$LIFE_ST_DNS_OFF" ]; }
life_dns_running() {
  # 同 life_engine_healthy：pidfile 里的号会被无关进程复用，必须认身份（见 life_pid_file_is_bin）
  life_pid_alive "$LIFE_ST_DNS" && life_pid_file_is_bin "$LIFE_ST_DNS" "$LIFE_DNSFWD"
}
life_dns_healthy() {
  # 谓词的唯一实现：用户关闭 / 在跑 / 已让路（:53 被第三方占用，是正常稳态而非"死了"）
  life_dns_disabled && return 0
  life_dns_running && return 0
  life_port53_busy && return 0
  return 1
}
life_ensure_dns() {
  life_user_stopped && { echo "off-by-user"; return 0; }
  life_dns_disabled && { echo "disabled"; return 0; }
  if life_dns_running; then
    life_cgroup_escape "$(life_pid_of "$LIFE_ST_DNS")"
    echo "running"; return 0
  fi
  life_port53_busy && { echo "yielded"; return 0; }
  # 让位宽限窗口：Magisk 服务早于普通 App 启动，第三方 DNS 服务可能还没起。
  sleep 5
  life_port53_busy && { echo "yielded"; return 0; }
  _BIND="$(life_read_bind)"
  if command -v setsid >/dev/null 2>&1; then
    setsid "$LIFE_DNSFWD" -f "$LIFE_UPSTREAMS" -b "$_BIND" >>"$LIFE_DNS_LOG" 2>&1 &
  else
    "$LIFE_DNSFWD" -f "$LIFE_UPSTREAMS" -b "$_BIND" >>"$LIFE_DNS_LOG" 2>&1 &
  fi
  echo $! > "$LIFE_ST_DNS"
  life_cgroup_escape "$(life_pid_of "$LIFE_ST_DNS")"
  echo "started"
}
life_stop_dns() {
  # 先按 pidfile 精确杀（含身份校验）；再按完整二进制路径兜底（pidfile 失联会留下占
  # 127.0.0.1:53 的孤儿，"关闭"就成了假关闭 —— 真机实测发生过）。
  # 模式必须带 `-b `（只有守护形态带它）：`dnsfwd -P -j 8` 是面板探测进程，共享同一路径。
  _p="$(life_pid_of "$LIFE_ST_DNS")"
  if [ -n "$_p" ] && kill -0 "$_p" 2>/dev/null; then
    if life_pid_is_exe "$_p" "$LIFE_DNSFWD"; then kill "$_p" 2>/dev/null
    else life_log "stop-dns: pidfile 里的 $_p 不是本模块 dnsfwd，跳过（防误杀）"; fi
  fi
  for p in $(pgrep -f "$LIFE_DNSFWD .*-b " 2>/dev/null); do
    [ "$p" != "$$" ] && [ "$p" != "${PPID:-}" ] && kill "$p" 2>/dev/null
  done
  rm -f "$LIFE_ST_DNS"
}
life_disable_dns() {
  # 同 life_stop_user：**意图先落盘再停进程**（否则守护的 CHLD 快路径会把刚关掉的 dnsfwd
  # 又拉起来 —— 用户看到的是"关了又自己开了"）。
  printf 'off\n' > "$LIFE_ST_DNS_OFF"
  life_stop_dns
}
life_enable_dns() { rm -f "$LIFE_ST_DNS_OFF"; life_ensure_dns; }
life_reload_dns() {
  # 热重载上游（SIGHUP）：只对"确实在跑的 dnsfwd"发信号（身份校验避免误伤复用 pid）
  _p="$(life_pid_of "$LIFE_ST_DNS")"
  if [ -n "$_p" ] && kill -0 "$_p" 2>/dev/null && life_pid_is_exe "$_p" "$LIFE_DNSFWD" \
     && kill -HUP "$_p" 2>/dev/null; then
    echo "reloaded"
  else
    echo "fail"
  fi
}

# ── 意图编排（调用方的主入口）─────────────────────
life_boot() {
  # 开机 = 新的用户意图：清除"用户停止服务"与"关闭守护"两个标志，武装并启动守护。
  # 只能在 init/ksud 上下文调用（见 ADR-0004）。
  rm -f "$LIFE_ST_USER_OFF" "$LIFE_ST_WD_OFF"
  : > "$LIFE_ST_WD_ARMED"
  # **判据绝不丢弃**：2026-09-29 真机事故 —— 开机那次守护没起来，而 `life_wd_start
  # >/dev/null` 把 start-failed 静默了，日志里一个字都没有，用户只能从面板看到
  # "未运行"，无从排查（引擎随后任何死因都不会再自愈）。
  # 失败立刻重试一次：开机期系统繁忙，首次启动可能在"等 pidfile 就位"的 3s 窗口里
  # 超时（life_wd_start 幂等：已在跑则返回 running，不会起第二个）。
  _w="$(life_wd_start)"
  [ "$_w" = "start-failed" ] && { sleep 1; _w="start-failed→$(life_wd_start)"; }
  life_log "boot: watchdog=$_w"
  echo "booted"
}
life_stop_all() {
  # 只停进程并清 pidfile；不触碰 dns-disabled（用户显式开关）。
  # 必须**等到它们真的退出**再返回：引擎收到 SIGTERM 后要 drain SSE、关监听才走
  # （真机见过"sleep 1 不够 → 新实例 bind 失败 → 守护日志写'拉起失败'，下一轮才成"）。
  _ep="$(life_pid_of "$LIFE_ST_ENGINE")"
  _dp="$(life_pid_of "$LIFE_ST_DNS")"
  life_stop_engine
  life_stop_dns
  # 等两个进程真的退出（含僵尸态）：语义唯一实现在 lib/wait.sh
  wait_gone 50 0.2 "$_ep" "$_dp"
  echo "stopped"
}
life_stop_user() {
  # 用户显式停服务：记住意图，守护不再复活（这正是"意图有主"的价值）
  # **顺序不能倒**：意图必须先落盘，再动进程。
  # 为什么（Phase 33.12 真机 T4 抓到）：守护改成事件驱动后，引擎一死 CHLD 会**毫秒级**
  # 唤醒它。若先停后写，守护醒来时 service-off 还不存在 → 它会立刻把用户刚停掉的服务复活。
  # 旧代码（5s 轮询 + 连续两次判死 ≈ 10s 窗口）刚好掩盖了这个竞态 —— 是"变快"把它暴露的。
  printf 'stop\n' > "$LIFE_ST_USER_OFF"
  life_stop_all >/dev/null
  echo "stopped"
}
life_start_user() {
  rm -f "$LIFE_ST_USER_OFF"
  life_ensure_engine
  life_ensure_dns >/dev/null
  # 如实自报（2026-09-29 诊断）：过去无论引擎有没有起来都 echo started，面板于是无条件报
  # 「✅ 服务已启动」—— 而同文件的 restart-engine 一直会回 engine=up/down。端口被占/二进制损坏时
  # 用户看到的是"成功"与状态卡"未运行"互相打脸。改成与 restart 同一口径（等一会儿再判）。
  if wait_for 20 2 life_engine_healthy; then echo "engine=up"; else echo "engine=down"; fi
}
life_restart_all() {
  # 「停了它，就由同一处负责把它起回来」—— stop_all 会**连同 DNS 一起停**，所以这里必须
  # 也把 DNS 拉回来。为什么要有这个 module（2026-09-29 架构走查 A3）：
  # 无守护分支过去只 ensure_engine，DNS 静默停摆 —— 而 Android 无 /etc/resolv.conf、
  # 引擎只认 127.0.0.1:53，DNS 不在就等于**域名解析全挂**；按钮文案写的却是「重启引擎 + DNS」。
  # 收进一个函数后，两条分支（有守护/无守护）不会再各自漏掉一项。
  life_stop_all >/dev/null
  # LIFE_CALLER 只影响日志归属，透传调用方的名字
  ( LIFE_CALLER="${1:-restart-all}"; export LIFE_CALLER; life_ensure_engine ) >/dev/null
  ( LIFE_CALLER="${1:-restart-all}"; export LIFE_CALLER; life_ensure_dns ) >/dev/null 2>&1
}
life_restart_engine() {
  # 有守护时**委托守护**执行停止+启动：重启后的进程天然落在免疫上下文里；
  # 没守护时才退回本上下文（不理想，但至少能用，并把守护顺手拉回来）。
  # 注意：守护那条分支自己有 eng_ours/dns_ours 记账与逐条日志，故不套用 life_restart_all
  # （它在本文件里，不读那些守护态变量）；**但两侧"停就负责起"的语义必须一致** ——
  # 无守护这一侧走 life_restart_all，DNS 不再被漏掉。
  rm -f "$LIFE_ST_USER_OFF"
  if ! life_wd_alive && life_wd_armed; then life_wd_start >/dev/null; fi
  life_wd_hold 180
  if life_wd_alive; then
    life_wd_request restart
  else
    life_restart_all restart-engine
  fi
  if wait_for 20 2 life_engine_healthy; then
    life_wd_hold_release
    echo "engine=up"
    return 0
  fi
  life_wd_hold_release
  echo "engine=down"
  return 0
}
life_state() {
  # 只读：机器可读一行（值都不含空格），供 ops.sh status 组合。dnsfwd/引擎/守护三个状态
  # 的判定规则只在这里写一次（此前散在 status / panel / 守护三处，各写一遍）。
  _dns=down; _dns_pid=""
  if life_dns_disabled; then _dns=disabled
  elif life_dns_running; then _dns=up; _dns_pid="$(life_pid_of "$LIFE_ST_DNS")"
  elif life_port53_busy; then _dns=yielded; fi
  _eng=down; _eng_pid=""
  if life_engine_healthy; then _eng=up; _eng_pid="$(life_pid_of "$LIFE_ST_ENGINE")"
  elif life_user_stopped; then _eng=stopped; fi
  _wd=down; _wd_pid=""
  if life_wd_alive; then _wd=up; _wd_pid="$(life_pid_of "$LIFE_ST_WD_PID")"
  elif life_wd_armed; then _wd=stale; fi
  echo "dns=$_dns dns_pid=$_dns_pid engine=$_eng engine_pid=$_eng_pid watchdog=$_wd watchdog_pid=$_wd_pid"
}
