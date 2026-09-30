#!/system/bin/sh
# ops.sh — 配置与运维编排（service.sh / action.sh / WebUI(ksu.exec) 的 seam）
#
# 职责边界（ADR-0005，「一切以模块实际源码为准」的分工声明）：
#   本文件   = 配置与数据（端口读写、库计数、出厂 key、引擎/模块安装编排）+ 状态聚合 + 对外子命令；
#   生命周期 = lib/lifecycle.sh（进程启停、用户意图、状态文件、cgroup 脱组）。本文件只 source 它，
#              **绝不自己读写任何 lifecycle 状态文件**（否则"状态无主"的老毛病会立刻回来）。
# 输出约定：机器可读的 key=value 行（WebUI 解析）；部分子命令输出状态词。
USAGE="ops.sh <status|panel|get <key> [key...]|prep-db|start-engine|stop-engine|stop-all|stop-user|start-user|restart-engine|reload-dns|watchdog-start|hold [sec]|install-engine <file> [ver]|install-module <zip>|cleanup [--dry-run]|start-dns|stop-dns|enable-dns|port53-busy|seed-key [--force]|get-port>"
# 环境变量: DATA_DIR（默认 /data/adb/9router-go）、PORT（显式覆盖端口）

# MODDIR 可覆盖（与 DATA_DIR 同形）：让 install-engine / install-module 的**成功与回滚路径**
# 能在临时 MODDIR 上离线端到端重放（2026-09-30 架构扫描 C2）。此前它写死成"脚本所在目录的上级"
# → 安装只能真写真实模块目录 → 那两条最安全敏感的分支**只在真机上赌**，唯一的顺序保障是
# tools/test-install-gate.sh 的 grep 行号（文本形状，不是结构）。回归：tools/test-install-flow.sh。
# 默认值不变（真实调用方不设这个变量 → 行为与过去完全一致）。
MODDIR="${MODDIR:-$(cd "$(dirname "$0")/.." && pwd)}"
DATA_DIR="${DATA_DIR:-/data/adb/9router-go}"
DB_FILE="$DATA_DIR/db/data.sqlite"
SQLITE3="$MODDIR/bin/sqlite3"
FACTORY_KEY="sk-8b71f86e0a1f2fb5-nhz496-cfa1c800"
FACTORY_KEY_ID="seed-default-client-key"

LIFECYCLE="$MODDIR/lib/lifecycle.sh"
if [ ! -r "$LIFECYCLE" ]; then
  echo "ops.sh: 缺 lib/lifecycle.sh（生命周期唯一所有者）；模块不完整，拒绝猜测" >&2
  exit 1
fi
. "$LIFECYCLE"

# ── 本文件私有的原语（配置/元数据，与进程生命周期无关）────────────
module_version() { grep -E '^version=' "$MODDIR/module.prop" 2>/dev/null | cut -d= -f2; }
module_versioncode() { grep -E '^versionCode=' "$MODDIR/module.prop" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]'; }
engine_version() {
  # 真实引擎版本的唯一来源（绝不拿模块版本冒充）：
  #   1) $DATA_DIR/engine-version —— install-engine 运行期更新时写入
  #   2) 包内 etc/engine-version —— 构建期写入（首次安装/模块更新携带）
  if [ -s "$DATA_DIR/engine-version" ]; then cat "$DATA_DIR/engine-version"; return; fi
  [ -s "$MODDIR/etc/engine-version" ] && cat "$MODDIR/etc/engine-version"
}

engine_version_sync() {
  # 运行期版本文件的**自愈**（2026-09-26 用户实测「假更新」）：
  #   整包更新只换 $MODDIR 里的文件，**不跑我们的代码** —— WebUI 那个按钮是由"当时装在设备上
  #   的那份旧 ops.sh"执行的（所以修好的代码要等下一次 install 才生效），管理器在线更新
  #   （updateJson）更是一个字节的模块代码都不跑。于是 $DATA_DIR/engine-version 停在上一个版本：
  #   包已是 r2、引擎实际在跑 1.9.2，面板却写 1.9.1 —— 用户看到的就是"更新没成功"。
  #
  # 收敛规则（**不依赖 mtime 精度**：mksh 的 -nt 只到秒，同一秒内的先写后 touch 判不出来）：
  #   主判据 = `engine-version-code` 记录着"这份引擎版本是在哪个 module versionCode 下写的"。
  #     ① 记录缺失、或与当前 versionCode 不同 → 包被换过（而换包必然换 bin/）→ 以包内为准；
  #     ② 记录与当前一致 → 这份引擎是本模块版本下由 install-engine 装的 → 别动它；
  #     ③ 运行期文件缺失 → 从包内补齐。
  #   补充判据 = 包比运行期文件新（同 versionCode 的重装场景，见 T12a2）。
  # 同时记下"这次的值是从哪来的、是不是刚自愈"，供面板做来源自检提示（谎报一眼可见）
  #
  # --from-package（install-module 专用）：包**刚被整体换过**，运行期文件无条件以包内为准
  #（跳过全部判据）；包里没有 etc/engine-version 时删除运行期文件——宁可显示"未知"，
  # 也不留旧版本的谎报。此前这段逻辑内联在 cmd_install_module 里（第二写者，判据漂移）。
  ENGINE_VER_HEALED=0
  ENGINE_VER_SRC=runtime
  _force=0; [ "${1:-}" = "--from-package" ] && _force=1
  if [ ! -s "$MODDIR/etc/engine-version" ]; then
    if [ "$_force" = 1 ]; then
      rm -f "$DATA_DIR/engine-version" "$DATA_DIR/engine-version-code" 2>/dev/null
      ENGINE_VER_SRC=none
      ENGINE_VER_HEALED=1
    else
      [ -s "$DATA_DIR/engine-version" ] || ENGINE_VER_SRC=none
    fi
    return 0
  fi
  _code="$(module_versioncode)"
  _seen="$(cat "$DATA_DIR/engine-version-code" 2>/dev/null)"
  if [ "$_force" = 1 ] \
     || [ ! -s "$DATA_DIR/engine-version" ] \
     || [ "$_seen" != "$_code" ] \
     || [ "$MODDIR/module.prop" -nt "$DATA_DIR/engine-version" ]; then
    cp "$MODDIR/etc/engine-version" "$DATA_DIR/engine-version" 2>/dev/null
    printf '%s\n' "$_code" > "$DATA_DIR/engine-version-code" 2>/dev/null
    ENGINE_VER_HEALED=1
    ENGINE_VER_SRC=package
  fi
}
lan_ips() {
  # 全局作用域 IPv4（排除 127.*），'|' 连接（panel 值不含空格），最多 3 个
  ip -4 addr show scope global 2>/dev/null \
    | sed -n 's/.*inet \([0-9.]*\).*/\1/p' | head -n 3 | tr '\n' '|' | sed 's/|$//'
}

# ── 子命令 ────────────────────────────────────
cmd_status() {
  # 先让运行期版本文件自愈（整包更新/管理器安装不跑我们的代码，见 engine_version_sync）：
  # 放在这里是因为 status 是面板与 action.sh 的唯一数据源，读一次就收敛一次
  engine_version_sync
  # 单行输出（空格分隔 key=value）：兼容 WebUI 的 promise 降级形态
  # （该形态多行输出只剩末行）。值均不含空格。
  # 进程三态（dns/engine/watchdog）由 life_state 提供 —— 判定规则只有那一处。
  _cnts="$("$SQLITE3" "$DB_FILE" "SELECT (SELECT COUNT(*) FROM apiKeys WHERE key='$FACTORY_KEY') || '|' || (SELECT COUNT(*) FROM apiKeys);" 2>/dev/null | tr -d '[:space:]')"
  case "$_cnts" in
    # 读失败（引擎写事务锁住 / 表缺失）：显式 err，绝不冒充 0 —— 上层凭 0 会误判
    # 全新安装而自动写库（孤儿扫描同款护栏：读不到 = 不可信）
    ''|*[!0-9|]*) _fk=err; _kt=err ;;
    # mksh 的参数展开模式里 | 是"或"运算符（%%|* 会删空全部），必须转义 \| 才是字面量
    *) _fk="${_cnts%%\|*}"; _kt="${_cnts##*\|}" ;;
  esac
  # engine_version 来自真实来源（engine_version()），versioncode 来自 module.prop
  # 字段——此前从 "v1.9.1-r1" 正则提取 r1 当 versionCode 与远端 109010 比较，
  # 导致"没发新版却永远提示有更新"
  echo "port=$(life_get_port) bind=$(life_read_bind) module_version=$(module_version) versioncode=$(module_versioncode) engine_version=$(engine_version) engine_ver_src=${ENGINE_VER_SRC:-none} engine_ver_healed=${ENGINE_VER_HEALED:-0} lan_ip=$(lan_ips) $(life_state) factory_key=$_fk apikeys_total=$_kt"
}

cmd_panel() {
  # 概览页单命令数据源（WebUI 一次 ksu.exec 拿全，替代原先 9 次串行 shell）：
  # status 全量 + meminfo + 引擎/dnsfwd RSS + upstreams(base64 单行) + mod_url + accel_sel。
  # base64 保证多行 upstreams 不含空白，promise 降级形态（只剩末行）下也完整。
  _s="$(cmd_status)"
  _mem="$(awk '/^MemTotal:/{mt=$2}/^MemAvailable:/{ma=$2}END{print mt+0, ma+0}' /proc/meminfo 2>/dev/null)"
  _mem="${_mem:-0 0}"
  _ep="$(life_engine_pid)"
  _dp="$(life_dns_pid)"
  _er="$(awk '/^VmRSS:/{print $2+0; exit}' "/proc/$_ep/status" 2>/dev/null)"; _er="${_er:-0}"
  _dr="$(awk '/^VmRSS:/{print $2+0; exit}' "/proc/$_dp/status" 2>/dev/null)"; _dr="${_dr:-0}"
  _ub="$(base64 "$DATA_DIR/dns-upstreams.conf" 2>/dev/null | tr -d '\n')"
  _mu="$(cat "$DATA_DIR/module-update-url" 2>/dev/null)"
  _as="$(cat "$DATA_DIR/github-accel" 2>/dev/null)"
  echo "$_s mem_total=${_mem%% *} mem_avail=${_mem##* } engine_rss=$_er dns_rss=$_dr upstreams_b64=$_ub mod_url=$_mu accel_sel=$_as"
}
cmd_get() {
  # ops.sh get <key> [key...] —— 键访问器：**「status/panel 是一行空格分隔的 k=v」这个事实的
  # 唯一所有者**（解析只在这里实现一次）。
  #
  # 为什么要有它（2026-09-29 架构走查 A4）：module/action.sh 曾自己拿 `grep "^$1="` 去解析
  # 这一行 —— 行锚匹配**行中间的键永远不中**（engine= 在行中间 → 显示成空），而**行首的键
  # （port=）会命中整行**，`cut -d= -f2-` 于是把 "20130 bind=loopback module_version=… engine=up …"
  # 整串吐出来（连 `curl http://127.0.0.1:$(kv port)/health` 都必然失败）。
  #
  # 输出形态刻意与 status/panel **不同**：每个键一行（`key=value`），键不存在就跳过该行，
  # 全都没命中则退出 1（「读不到」不等于「空结果」）。多行在这里是安全的：
  # 本访问器的消费者是 shell 脚本（action.sh），多行让 `grep "^key="` 这类读法**按构造正确**；
  # WebUI 那条路仍用单行的 panel（promise 降级形态只保留末行）。
  if [ "$#" -eq 0 ]; then
    echo "usage: ops.sh get <key> [key...]" >&2
    return 1
  fi
  _g_line="$(cmd_status)"
  _g_hit=0
  for _g_k in "$@"; do
    # 键名白名单：既防用户输入当正则用，也保证下面的模式匹配不会自我破坏
    case "$_g_k" in ''|*[!a-zA-Z0-9_]*) continue ;; esac
    case " $_g_line" in
      *" ${_g_k}="*)
        _g_v="$(printf '%s' "$_g_line" | tr ' ' '\n' | sed -n "s/^${_g_k}=//p" | head -1)"
        printf '%s=%s\n' "$_g_k" "$_g_v"
        _g_hit=1
        ;;
    esac
  done
  [ "$_g_hit" = 1 ]
}

ENGINE_MIN_BYTES=5242880  # 与 parsers.js ENGINE_MIN_BYTES 对齐（真实产物约 25MB）

engine_src_ok() {
  # 一个文件"像不像一个引擎"：体积下限 + ELF 魔数。
  # 下载器（curl -f）打的是 HTTP 层，这里打的是文件本体 —— 2026-09-26 的事故正是
  # 从这一层漏进去的：加速节点 404 正文（9 字节 "Not Found"）既没被前端拦，也没被这里拦，
  # 于是装成了"引擎"、版本号还写成了 1.9.2，设备上再没有可用引擎。
  [ -f "$1" ] || return 1
  _sz="$(wc -c < "$1" 2>/dev/null | tr -d '[:space:]')"
  case "$_sz" in ''|*[!0-9]*) return 1 ;; esac
  [ "$_sz" -ge "$ENGINE_MIN_BYTES" ] || return 1
  [ "$(head -c 4 "$1" 2>/dev/null | od -An -tx1 | tr -d '[:space:]')" = "7f454c46" ] || return 1
  return 0
}

cmd_install_engine() {
  # 引擎二进制安装唯一入口：门禁 → 回滚点 → 替换 → 重启 → （起来后才）写版本与 .bak。
  # src 为已下载到本地的临时文件；第二参数为引擎版本（可选），写 $DATA_DIR/engine-version。
  # 全程 hold 住守护：换文件的窗口里它去拉起旧/半份二进制会造成"text file busy"或假启动。
  [ -f "${1:-}" ] || { echo "no-src"; return 0; }
  # 门禁必须在**动任何东西之前**：不合格的源绝不能碰现有二进制
  if ! engine_src_ok "$1"; then
    echo "install-rejected-src"   # 前端据此提示"下载物不是引擎"；设备保持原样
    return 0
  fi
  life_wd_hold 300
  life_stop_all >/dev/null
  # 回滚点只在**当前二进制本身合格**时才留：否则会把垃圾当回滚点，把好二进制挤掉
  # （2026-09-26 就是这样丢的：第二次尝试用 9 字节的当前文件覆盖了 .bak）
  _prev="$DATA_DIR/engine.prev"
  rm -f "$_prev"
  engine_src_ok "$MODDIR/bin/9router-go" && cp "$MODDIR/bin/9router-go" "$_prev" 2>/dev/null
  if mv "$1" "$MODDIR/bin/9router-go" && chmod 0755 "$MODDIR/bin/9router-go"; then
    if [ "$(life_restart_engine)" = "engine=up" ]; then
      # 版本号只在新引擎**真的起来**之后才写（旧实现在替换后立即写 → 谎报）
      if [ -n "${2:-}" ]; then
        printf '%s\n' "$2" > "$DATA_DIR/engine-version"
        # 记录"这份引擎是在哪个模块版本下装的"：engine_version_sync 据此区分
        # 「整包更新换掉了引擎」与「运行期更新了引擎」，从而自愈而不误覆盖
        printf '%s\n' "$(module_versioncode)" > "$DATA_DIR/engine-version-code"
      fi
      # .bak 的语义从"替换前备份"改为"最后一次已验证可用"（只在这里更新）
      cp "$MODDIR/bin/9router-go" "$MODDIR/bin/9router-go.bak" 2>/dev/null
      echo "engine=up"
    elif [ -f "$_prev" ]; then
      # 起不来就回滚到启动前的二进制，并把版本号留在旧值（绝不谎报）
      cp "$_prev" "$MODDIR/bin/9router-go" && chmod 0755 "$MODDIR/bin/9router-go"
      life_restart_engine >/dev/null
      echo "install-failed-rolled-back"
    else
      life_wd_hold_release
      echo "install-failed"
    fi
    rm -f "$_prev"
  else
    life_wd_hold_release
    echo "install-failed"
  fi
}

cmd_install_module() {
  # 模块 zip 安装唯一入口：解压到暂存区 → **用 mv 落位（换 inode）** → 权限兜底 → 同步引擎版本 → 重启。
  # 为什么不能直接 `unzip -oq` 到 $MODDIR：本文件（lib/ops.sh）正被当前 shell 逐行读取，unzip
  # 原地覆写（同 inode + 截断重写）会让 shell 从被改写的位置继续读 → 真机实测报
  # "ops.sh[193]: syntax error: unexpected ';'"（行号落在 case 块内），安装中途夭折、引擎可能被
  # 留在停住的状态。mv 换 inode 后执行中的实例读的还是旧文件，安全。
  [ -f "${1:-}" ] || { echo "no-src"; return 0; }
  # **门禁先于动作**（2026-09-29 诊断）：与 cmd_install_engine 的"不合格源绝不碰现有二进制"对称。
  # 过去这行之后立刻 stop_all：zip 坏 / 非 zip / unzip 缺失时只能 echo install-failed 走人，
  # 而引擎与 dnsfwd **已经被停掉** —— 守护若未武装（watchdog-armed 不存在），服务不会自己回来。
  # 装包是"模块唯一安装入口"，必须先把包验成可用，再动服务。
  _lst="$(unzip -l "$1" 2>/dev/null)" || _lst=""
  case "$_lst" in
    *module.prop*) ;;
    *) echo "install-failed"; return 0 ;;
  esac
  life_wd_hold 300
  life_stop_all >/dev/null
  cp "$1" "$DATA_DIR/last-module.zip" 2>/dev/null
  _stage="$DATA_DIR/module-stage"
  rm -rf "$_stage"
  if ! mkdir -p "$_stage" || ! (cd "$_stage" && unzip -oq "$1"); then
    rm -rf "$_stage"
    life_wd_hold_release
    echo "install-failed"
    return 0
  fi
  # 目录整体换 inode 会连带丢掉"不在包内"的文件 —— bin/9router-go.bak（回滚点）就是唯一一个，
  # 先把它放进暂存的 bin/，让它跟着一起换过去，回滚能力不因整包更新而消失。
  if [ -f "$MODDIR/bin/9router-go.bak" ] && [ -d "$_stage/bin" ]; then
    cp "$MODDIR/bin/9router-go.bak" "$_stage/bin/9router-go.bak" 2>/dev/null
  fi
  # 目录整体换 inode：先把新目录搬成 .new，再把旧目录挪开，最后就位
  # （直接 `mv 新目录 $MODDIR/` 且旧目录同名时，会把新目录塞进旧目录里 —— 必须绕开）
  for _d in lib bin webroot etc; do
    [ -d "$_stage/$_d" ] || continue
    rm -rf "$MODDIR/$_d.new" "$MODDIR/$_d.old"
    mv "$_stage/$_d" "$MODDIR/$_d.new" || continue
    [ -d "$MODDIR/$_d" ] && mv "$MODDIR/$_d" "$MODDIR/$_d.old"
    mv "$MODDIR/$_d.new" "$MODDIR/$_d"
    rm -rf "$MODDIR/$_d.old"
  done
  # 顶层文件（module.prop / service.sh / uninstall.sh / …）同样用 mv 换 inode
  for _f in "$_stage"/*; do
    [ -f "$_f" ] || continue
    mv -f "$_f" "$MODDIR/" 2>/dev/null
  done
  rm -rf "$_stage"
  chmod 0755 "$MODDIR"/*.sh "$MODDIR"/lib/*.sh "$MODDIR"/bin/* 2>/dev/null
  # 整包更新同样换了 bin/9router-go：运行期版本文件无条件以包内为准。
  # 写入逻辑单一所有者 = engine_version_sync（此前内联第二实现，判据已与自愈版漂移）。
  engine_version_sync --from-package
  rm -f "$1"
  # 与 install-engine 同一语义：只有新引擎**真的起来**才把恢复点刷成这一份（已验证可用）
  if [ "$(life_restart_engine)" = "engine=up" ]; then
    cp "$MODDIR/bin/9router-go" "$MODDIR/bin/9router-go.bak" 2>/dev/null
    echo "engine=up"
  else
    echo "engine=down"
  fi
}

CLEAN_KEEP_BACKUPS="${CLEAN_KEEP_BACKUPS:-5}"   # 备份快照保留份数（新的在前）

cmd_cleanup() {
  # 清理"更新/安装/运行"留下的可安全丢弃的残留（`--dry-run` 只报告，不动手）。
  # 绝不碰：当前引擎二进制、`.bak` 回滚点、`last-module.zip`（整包回滚用）、DB、凭据、
  #         端口/加速节点等配置、watchdog 状态文件、最近 CLEAN_KEEP_BACKUPS 份备份快照。
  _dry="${1:-}"
  _freed=0
  _del() {
    [ -e "$1" ] || return 0
    _sz="$(du -sk "$1" 2>/dev/null | awk '{print $1}')"; _sz="${_sz:-0}"
    if [ "$_dry" = "--dry-run" ]; then
      echo "  [dry] $1 （${_sz}KB）"
    else
      rm -rf "$1" 2>/dev/null && { _freed=$((_freed + _sz)); echo "  已删 $1 （${_sz}KB）"; }
    fi
  }
  echo "== 清理残留（保留：当前二进制 / .bak 回滚点 / last-module.zip / DB / 凭据 / 最近 ${CLEAN_KEEP_BACKUPS} 份备份）=="
  # 1) 安装/更新过程中可能遗留的半份目录与回滚中间件
  for _d in lib bin webroot etc; do _del "$MODDIR/$_d.old"; _del "$MODDIR/$_d.new"; done
  _del "$DATA_DIR/module-stage"
  _del "$DATA_DIR/engine.prev"
  # 2) 旧的安装包与临时下载（/data/local/tmp 是共享目录，只删我们自己的命名）
  _del /data/local/tmp/9r-eng.new
  _del /data/local/tmp/9r-mod.zip
  _del /data/local/tmp/9r-gate.zip
  for _z in /data/local/tmp/9router-go-*.zip; do
    case "$_z" in *'*'*) continue ;; esac
    _del "$_z"
  done
  # 3) 日志轮转产物（上限由 lib/log.sh 管；这里只收走已经轮转出去的那份）
  for _l in "$DATA_DIR"/9router.log.1 "$DATA_DIR"/dnsfwd.log.1 "$DATA_DIR"/watchdog.log.1; do _del "$_l"; done
  # 4) 备份快照只留最近 N 份（旧的误删回滚点没必要长期堆着）
  if [ -d "$DATA_DIR/backups" ]; then
    _i=0
    for _f in $(ls -1t "$DATA_DIR/backups" 2>/dev/null); do
      _i=$((_i + 1))
      [ "$_i" -le "$CLEAN_KEEP_BACKUPS" ] && continue
      _del "$DATA_DIR/backups/$_f"
    done
  fi
  if [ "$_dry" = "--dry-run" ]; then
    echo "（dry-run：未删除任何文件；数据目录当前 $(du -sk "$DATA_DIR" 2>/dev/null | awk '{print $1}')KB）"
  else
    echo "释放约 $((_freed / 1024))MB；数据目录现在 $(du -sk "$DATA_DIR" 2>/dev/null | awk '{print $1}')KB"
  fi
}

cmd_seed_key() {
  [ -x "$SQLITE3" ] && [ -s "$DB_FILE" ] || { echo "no-db"; exit 0; }
  _has="$("$SQLITE3" "$DB_FILE" "SELECT COUNT(*) FROM apiKeys WHERE key='$FACTORY_KEY';" 2>/dev/null | tr -d '[:space:]')"
  # POSIX 否定必须是 [!...]：mksh/dash 里 [^0-9] 的 ^ 是字面量（类= ^+数字），
  # 会把 "0"（key 不存在）误判为读取失败 → 永远 error（真机实锤，勿改回 [^...]）
  case "$_has" in ''|*[!0-9]*) echo "error"; exit 0 ;; esac  # 读失败不得谎报 present
  if [ "$_has" != "0" ] && [ "${1:-}" != "--force" ]; then echo "present"; exit 0; fi
  _cnt="$("$SQLITE3" "$DB_FILE" "SELECT COUNT(*) FROM apiKeys;" 2>/dev/null | tr -d '[:space:]')"
  case "$_cnt" in ''|*[!0-9]*) echo "error"; exit 0 ;; esac  # 读失败不得谎报 user-deleted
  if [ "$_cnt" = "0" ] || [ "${1:-}" = "--force" ]; then
    # INSERT 成功与否要如实上报，不得无条件谎报 seeded
    if "$SQLITE3" "$DB_FILE" "INSERT OR IGNORE INTO apiKeys (id, key, name, isActive, createdAt) VALUES ('$FACTORY_KEY_ID', '$FACTORY_KEY', 'Default client key (dashboard)', 1, datetime('now'));" 2>>"$LOG_ENGINE_PATH"; then
      echo "seeded"
    else
      echo "error"
    fi
  else
    # 表非空但 key 缺失 = 用户有意删除，不自动加回
    echo "user-deleted"
  fi
}

# ── 只加载模式（测试专用）────────────────────────────────────────────
# `OPS_LIB_ONLY=1` 时只取本文件的函数定义、不跑下面的 dispatch。存在的理由：本文件的编排逻辑
# （install-engine / install-module）是全仓最安全敏感的动作，而它过去**无法被 source**
# （裸 dispatch 会直接 usage+exit）→ 成功/回滚分支没有任何测试。回归工具用它把定义加载进
# 临时 shell、覆盖掉"进程那一层"的 lifecycle 函数，然后在临时 MODDIR 上重放整个安装流程
# （tools/test-install-flow.sh）。真机上没人设这个变量 → 行为完全不变。
if [ "${OPS_LIB_ONLY:-0}" = "1" ]; then
  return 0 2>/dev/null || exit 0
fi

case "${1:-}" in
  status)          cmd_status ;;
  panel)           cmd_panel ;;
  prep-db)         life_prep ;;
  start-engine)    life_ensure_engine ;;
  stop-engine)     life_stop_engine; echo "stopped" ;;
  stop-all)        life_stop_all ;;
  stop-user)       life_stop_user ;;
  start-user)      life_start_user ;;
  restart-engine)  life_restart_engine ;;
  reload-dns)      life_reload_dns ;;
  watchdog-start)  life_wd_start ;;
  hold)            shift; life_wd_hold "${1:-180}"; echo "held" ;;
  install-engine)  shift; cmd_install_engine "$@" ;;
  install-module)  shift; cmd_install_module "$@" ;;
  cleanup)         shift; cmd_cleanup "$@" ;;
  start-dns)       life_ensure_dns ;;
  stop-dns)        life_disable_dns; echo "stopped" ;;
  enable-dns)      life_enable_dns ;;
  port53-busy)     if life_port53_busy; then echo 1; else echo 0; fi ;;
  seed-key)        shift; cmd_seed_key "$@" ;;
  get-port)        life_get_port ;;
  get)             shift; cmd_get "$@" ;;
  *)               echo "usage: $USAGE"; exit 1 ;;
esac
