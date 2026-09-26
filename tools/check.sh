#!/usr/bin/env bash
# tools/check.sh — 门禁唯一入口（清单与规则见 docs/TESTING.md、AGENT-CONVENTIONS.md §5）
#
# 档位：
#   --offline   离线：不需要真机 / 外网 / 上游参照树（默认档）
#   --device    真机：adb + root，跑 T*（生命周期与安装）/ A*（仪表盘 API）
#   --parity    对照：需要上游参照树（默认 ../9router，UPSTREAM= 可覆盖）
#   --all       三档全跑
# 严格模式（给将来的 CI）：
#   --require-device / --require-parity   缺前置时判失败，而不是 SKIP
#
# 约定（AGENT-CONVENTIONS §5）：缺前置 → 打印 SKIP 摘要并退出 0；断言失败 → 退出 1。
# 环境变量：
#   DEVICE=<serial>   指定真机（缺省取 `adb devices` 里第一台非 emulator）
#   UPSTREAM=<path>   上游参照树（用于 --parity）
#   CHECK_FAST=1      快速模式（build.sh 复用离线档时用）：跳过 GO-TEST 与 TSC，并如实标 SKIP
set -uo pipefail
cd "$(dirname "$0")/.."

OFFLINE=0; DEVICE_TIER=0; PARITY_TIER=0; REQ_DEVICE=0; REQ_PARITY=0; PICKED=0

usage() {
  sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
}

for a in "$@"; do
  case "$a" in
    --offline)       OFFLINE=1; PICKED=1 ;;
    --device)        DEVICE_TIER=1; PICKED=1 ;;
    --parity)        PARITY_TIER=1; PICKED=1 ;;
    --all)           OFFLINE=1; DEVICE_TIER=1; PARITY_TIER=1; PICKED=1 ;;
    --require-device) REQ_DEVICE=1 ;;
    --require-parity) REQ_PARITY=1 ;;
    -h|--help)       usage; exit 0 ;;
    *) echo "未知参数：$a" >&2; usage >&2; exit 2 ;;
  esac
done
[ "$PICKED" = 1 ] || OFFLINE=1

PASS=0; FAIL=0; SKIP=0
FAILED=(); SKIPPED=()
C_OK=$'\033[32m'; C_BAD=$'\033[31m'; C_SKIP=$'\033[33m'; C_H=$'\033[1m'; C_0=$'\033[0m'

step() { printf '\n%s== %s ==%s\n' "$C_H" "$*" "$C_0"; }
ok()   { printf '  %s✅%s %s\n' "$C_OK" "$C_0" "$*"; PASS=$((PASS + 1)); }
bad()  { printf '  %s❌%s %s\n' "$C_BAD" "$C_0" "$*"; FAIL=$((FAIL + 1)); FAILED+=("$*"); }
skip() { printf '  %s⏭ %s%s —— %s\n' "$C_SKIP" "$1" "$C_0" "$2"; SKIP=$((SKIP + 1)); SKIPPED+=("$1（$2）"); }
have() { command -v "$1" >/dev/null 2>&1; }
run()  { local name="$1"; shift; if "$@"; then ok "$name"; else bad "$name"; fi; }

device_serial() {
  if [ -n "${DEVICE:-}" ]; then echo "$DEVICE"; return; fi
  adb devices 2>/dev/null | awk '$2 == "device" && $1 !~ /^emulator/ {print $1; exit}'
}

# GO-TEST 需要排除的用例（本地必红的网络/凭据依赖；名单与证据见 docs/TESTING.md §4）。
# 逐条具名，不用宽泛前缀 —— 其余 e2e/live 用例自带 t.Skip，能自己跳过，别扩大排除范围。
go_skip_pattern() {
  printf '%s' 'TestHandleAudioVoices_elevenlabs|TestLiveE2E_Cline_SmartCombo|TestIntegration_OpenCode_MuseSpark13_ChatCompletions'
}

need_bin() {  # need_bin <命令> <档位名> <严格标志>
  if have "$1"; then return 0; fi
  if [ "$3" = 1 ]; then bad "$2：缺少 $1"; else skip "$2" "缺少 $1"; fi
  return 1
}

echo "门禁入口：档位 $([ "$OFFLINE" = 1 ] && echo -n 'offline ')$([ "$DEVICE_TIER" = 1 ] && echo -n 'device ')$([ "$PARITY_TIER" = 1 ] && echo -n 'parity')  仓库 $(pwd)"

# ── 离线档 ────────────────────────────────────────────────────────────────
if [ "$OFFLINE" = 1 ]; then
  step "离线档：语法 / 单元测试 / 构建 / 类型 / schema"

  # JS-SYNTAX：全部 shell 脚本语法（按 shebang 选 sh / bash —— bash 脚本用 sh -n 会假红）
  if have sh; then
    _bad=""
    while IFS= read -r f; do
      if head -n 1 "$f" 2>/dev/null | grep -q 'bash'; then
        bash -n "$f" 2>/dev/null || _bad="$_bad $f"
      else
        sh -n "$f" 2>/dev/null || _bad="$_bad $f"
      fi
    done < <(find module tools -name '*.sh' -type f 2>/dev/null | sort)
    if [ -z "$_bad" ]; then ok "JS-SYNTAX 全部 shell 脚本语法正确"; else bad "JS-SYNTAX 语法错误：$_bad"; fi
  else
    skip "JS-SYNTAX" "缺少 sh"
  fi

  # JS-UNIT：模块 WebUI 纯函数（node）
  if need_bin node "JS-UNIT" 0; then
    run "JS-UNIT 模块 WebUI 纯函数（node --test）" node --test module/webroot/test/parsers.test.js \
        module/webroot/test/bridge-commands.test.js module/webroot/test/contract-keys.test.js
  fi

  # BUN-UNIT：引擎 Dashboard 纯函数（bun；无 bun 但有 npx 时用 `npx --yes bun` 兜底）
  if have bun; then
    run "BUN-UNIT Dashboard 纯函数（bun test）" bash -c 'cd web && bun test'
  elif have npx; then
    run "BUN-UNIT Dashboard 纯函数（npx bun test）" bash -c 'cd web && npx --yes bun test'
  else
    skip "BUN-UNIT" "缺少 bun 与 npx"
  fi

  # GO-BUILD：引擎可编译
  if need_bin go "GO-BUILD" 0; then
    run "GO-BUILD 引擎编译（go build ./...）" go build ./...
    # GO-TEST：排除外网/真机依赖用例（名单见 docs/TESTING.md §4）
    if [ "${CHECK_FAST:-0}" = 1 ]; then
      skip "GO-TEST" "CHECK_FAST=1（build.sh 复用离线档；发布前请单独跑 tools/check.sh --offline）"
    else
      run "GO-TEST Go 单元测试（跳过外网依赖用例）" go test ./... -skip "$(go_skip_pattern)"
    fi
  fi

  # TSC：Dashboard 类型检查（build.sh 的 bun run build 内含 tsc -b，故快速模式跳过）
  if [ "${CHECK_FAST:-0}" = 1 ]; then
    skip "TSC" "CHECK_FAST=1（构建流程内的 tsc -b 已覆盖）"
  elif ! have npx; then
    skip "TSC" "缺少 npx"
  elif [ ! -d web/node_modules ]; then
    skip "TSC" "web/node_modules 不存在（先 bun/npm install）"
  else
    run "TSC Dashboard 类型检查（tsc -b）" bash -c 'cd web && npx tsc -b'
  fi

  # SCHEMA：schema 漂移
  if need_bin python3 "SCHEMA" 0; then
    run "SCHEMA schema 与上游 DATABASE.md 对齐" python3 tools/gen-schema.py --check
    # DEADH：handler / 注册函数是否真的被挂载（棘轮；已核实的历史包袱在 ignore 里带理由）
    if [ -f tools/check-dead-handlers.py ]; then
      run "DEADH handler 挂载巡检（新增未挂载即红）" python3 tools/check-dead-handlers.py
    fi
    # PY-UNIT：棘轮 module 的接口级单测（三个门禁的机械结构只此一处，故测这里=测三个）
    # -B：不写 __pycache__（.gitignore 是上游文件，不该为它改动；discovery 导入测试模块时最容易漏）
    if [ -f tools/test_ratchet.py ]; then
      run "PY-UNIT 棘轮 module 单测（unittest discover tools）" \
        python3 -B -m unittest discover -s tools -p 'test_*.py'
    fi
    # INJECT：__MOD_ID__ 注入器（打包 / 直推两条路径共用一份实现，出错的表现是"上线后才炸"）
    if [ -f tools/test-inject-mod-id.sh ]; then
      run "INJECT 占位符注入器自证（全树注入 + 0 残留）" sh tools/test-inject-mod-id.sh
    fi
  fi
fi

# ── 真机档 ────────────────────────────────────────────────────────────────
if [ "$DEVICE_TIER" = 1 ]; then
  step "真机档：T*（生命周期与安装）/ A*（仪表盘 API）"
  if ! have adb; then
    if [ "$REQ_DEVICE" = 1 ]; then bad "真机档：缺少 adb"; else skip "真机档" "缺少 adb"; fi
  else
    SERIAL="$(device_serial)"
    if [ -z "$SERIAL" ]; then
      if [ "$REQ_DEVICE" = 1 ]; then bad "真机档：没有可用设备（adb devices 为空）"
      else skip "真机档" "没有可用设备（DEVICE= 可指定）"; fi
    else
      echo "  · 目标设备：$SERIAL"
      adb -s "$SERIAL" push tools/device/test-lifecycle.sh /data/local/tmp/ >/dev/null 2>&1 || true
      adb -s "$SERIAL" push tools/device/test-dashboard-api.sh /data/local/tmp/ >/dev/null 2>&1 || true
      run "T* 生命周期与安装（test-lifecycle.sh）" adb -s "$SERIAL" shell 'su -c "sh /data/local/tmp/test-lifecycle.sh"'
      run "A* 仪表盘 API（test-dashboard-api.sh）" adb -s "$SERIAL" shell 'su -c "sh /data/local/tmp/test-dashboard-api.sh"'
    fi
  fi
fi

# ── 对照档 ────────────────────────────────────────────────────────────────
if [ "$PARITY_TIER" = 1 ]; then
  step "对照档：上游端点 parity / UI 调用 parity"
  if ! have python3; then
    if [ "$REQ_PARITY" = 1 ]; then bad "对照档：缺少 python3"; else skip "对照档" "缺少 python3"; fi
  else
    UP="${UPSTREAM:-../9router}"
    if [ ! -d "$UP" ]; then
      if [ "$REQ_PARITY" = 1 ]; then bad "对照档：上游参照树不存在（$UP）"
      else skip "对照档" "上游参照树不存在（$UP；UPSTREAM= 可指定）"; fi
    else
      echo "  · 上游参照树：$UP"
      run "PARITY 端点 parity 棘轮" python3 tools/check-parity.py
      if [ -f tools/check-ui-parity.py ]; then
        run "UIPARITY UI 调用 ⊆ 已注册端点" python3 tools/check-ui-parity.py
      else
        skip "UIPARITY" "tools/check-ui-parity.py 尚未落地"
      fi
    fi
  fi
fi

# ── 变更映射自检（只提醒，不判失败；规则见 AGENT-CONVENTIONS §4）────────────
step "变更映射自检（提醒，不拦）"
if ! have git || ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "  · 非 git 工作区，跳过"
else
  CHANGED="$(git status --porcelain | awk '{print $NF}')"
  if [ -z "$CHANGED" ]; then
    echo "  · 工作区无改动"
  else
    WARN=0
    if echo "$CHANGED" | grep -qE '^module/lib/.*\.sh$' \
       && ! echo "$CHANGED" | grep -qE '^(docs/TESTING\.md|tools/device/|docs/adr/)'; then
      echo "  ⚠️  改了 module/lib/*.sh，但没有同步文档/门禁（§4：运维动作 → T* 断言 + 台账）"; WARN=1
    fi
    if echo "$CHANGED" | grep -qE '^(web/src/.*\.(ts|svelte)|module/webroot/(app|bridge|parsers)\.js)$' \
       && ! echo "$CHANGED" | grep -qE '(\.test\.(ts|js)$|docs/TESTING\.md)'; then
      echo "  ⚠️  改了前端请求形状相关文件，但没有测试改动（§4：请求形状 → 纯函数用例）"; WARN=1
    fi
    if echo "$CHANGED" | grep -qE '^internal/.*router\.go$' \
       && ! echo "$CHANGED" | grep -qE '^(tools/parity|docs/|internal/.*_test\.go)'; then
      echo "  ⚠️  改了引擎路由，但没有 parity/文档/测试改动（§4）"; WARN=1
    fi
    [ "$WARN" = 0 ] && echo "  · 无提醒"
  fi
fi

# ── 汇总 ──────────────────────────────────────────────────────────────────
step "结果"
echo "  通过 $PASS ／ 失败 $FAIL ／ 跳过 $SKIP"
if [ "$FAIL" != 0 ]; then
  printf '  %s失败项：%s\n' "$C_BAD" "$C_0"
  for f in "${FAILED[@]}"; do echo "    - $f"; done
fi
if [ "$SKIP" != 0 ]; then
  printf '  %s跳过项（缺前置或快速模式）：%s\n' "$C_SKIP" "$C_0"
  for s in "${SKIPPED[@]}"; do echo "    - $s"; done
fi
[ "$FAIL" = 0 ] || exit 1
exit 0
