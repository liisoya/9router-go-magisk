#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""check-ui-parity.py — UI 调用 ⊆ 已注册端点（棘轮）

为什么有它
    端点 parity 巡检（check-parity.py）回答"上游有的端点我们缺不缺"；本脚本回答另一半：
    **我们自己的 UI 有没有调用一个后端不存在的端点**。这类问题不会编译报错，只会在用户
    点到那个按钮时 404 —— 是"将来 UI 加新调用"时最可能踩的坑。

判定方式
    只认**调用点**，不认任意字符串（避免把注释/文档里的路径算成调用）：
      匹配 fetch(...) / request(...) / KB.fetch(...) / KB.download(...) 的第一个字符串参数
      （含 request<泛型>(...) 形态），从字符串里取出 `/api/...` 或引擎根路径（/health、
      /version、/callback、/v1/...）的片段；模板串里的 `${...}` 归一成 `{}`。
    已注册端点来自 `check-parity.py` 的 `scan_ours()`（**同一份扫描实现**，避免"什么算已注册"
    出现两种口径）。

棘轮（ratchet）语义（与端点巡检一致）
    存量"UI 调了但没注册"冻结在 tools/ui-parity-baseline.txt；新增的才报警（退出码 1）。
    确认等价/有意的写进 tools/ui-parity-ignore.txt（理由必填）。

已知边界（不假装覆盖）
    - 只做**路径级**判定：方法（GET/POST…）不解析（前端写法太多样，静态判方法会假红）
    - 间接引用（`const P = '/api/x'; fetch(P)`）与运行期拼接不覆盖
    - 覆盖范围：web/src/**、module/webroot/**（排除 *.test.*）

用法
    python3 tools/check-ui-parity.py               # 巡检（新增缺口即失败）
    python3 tools/check-ui-parity.py --list-gaps    # 列出全部存量缺口
    python3 tools/check-ui-parity.py --write-baseline
    python3 tools/check-ui-parity.py --list-ignored
退出码：0 = 无新增；1 = 有新增；2 = 环境错误
"""

import argparse
import importlib.util
import re
import sys
from pathlib import Path

# 载入 check-parity.py 时会写 __pycache__（污染仓库，而 .gitignore 是上游文件不该改）
sys.dont_write_bytecode = True

ROOT = Path(__file__).resolve().parent.parent
BASELINE_FILE = Path("tools") / "ui-parity-baseline.txt"
IGNORE_FILE = Path("tools") / "ui-parity-ignore.txt"
SCAN_GLOBS = ("web/src/**/*.ts", "web/src/**/*.svelte", "module/webroot/**/*.js", "module/webroot/**/*.html")
ENGINE_ROOTS = re.compile(r"^/(?:health|version|api(?:/|$)|v1(?:/|$)|callback$)")


def load_scan_ours():
    """从同目录的 check-parity.py 载入 scan_ours（单一实现；文件名带连字符故用 importlib）。"""
    spec = importlib.util.spec_from_file_location("check_parity", ROOT / "tools" / "check-parity.py")
    if spec is None or spec.loader is None:
        raise RuntimeError("无法载入 tools/check-parity.py")
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod.scan_ours


# 调用点：① fetch/request/KB.fetch/KB.download 的第一个字符串参数
#        ② 导航式调用（location.href = '...' / window.open('...')）—— SSO 起点就是这种
CALL_RE = re.compile(
    r"(?:fetch|request|KB\.fetch|KB\.download)[^(\n]{0,140}\(\s*(?:`([^`]*)`|'([^']*)'|\"([^\"]*)\")"
    r"|(?:location\.href|window\.open|location\.assign)\s*[=(]\s*(?:`([^`]*)`|'([^']*)'|\"([^\"]*)\")",
    re.S,
)
# 字符串里的路径片段：占位必须整体匹配（`${...}` 放在字符类之前，且字符类不含 $ { }），
# 否则 `/api/headroom/extras${log ? ... }` 会在占位中间被截断 → 假缺口。
PATH_RE = re.compile(r"/(?:[A-Za-z0-9_./\-]|\$\{[^{}]*\}|\{\})*")

# 别名前缀：引擎对已注册端点统一提供 /v1 形态（`/embeddings` 与 `/v1/embeddings`、
# `/systemone` 与 `/v1/systemone` …）。真机验证（2026-09-26）：7 对 `/x` 与 `/v1/x`
# 返回码完全一致 → /v1 是统一别名，不是"另有一套端点"。因此匹配时同时尝试去前缀的形态。
ALIAS_PREFIXES = ("/v1",)


def candidates(path: str) -> set:
    out = {path}
    for pre in ALIAS_PREFIXES:
        if path.startswith(pre + "/"):
            out.add(path[len(pre):])
    return out


def norm_ui_path(p: str) -> str:
    """UI 路径归一：模板占位与具体值都成 {}，去掉查询串与尾部斜杠。"""
    # ① 先收敛占位，再切查询串 —— 顺序不能反：占位内部常有 `?`（`${log ? '?log=1' : ''}`），
    #    先 split('?') 会把路径截断在占位中间（2026-09-26 自测发现的假缺口根因）。
    p = re.sub(r"\$\{[^}]*\}", "{}", p)
    p = re.sub(r"\{[^{}]*\}", "{}", p)
    p = p.split("?", 1)[0].split("#", 1)[0]
    # ② 紧贴段落的占位（`authorize${q}` / `proxy-pools${flag}`）通常是查询串模板，不是路径段 → 去掉
    p = re.sub(r"(?<=[^/]){}", "", p)
    p = re.sub(r"/{2,}", "/", p)
    if len(p) > 1:
        p = p.rstrip("/")
    return p


def ui_calls() -> dict:
    """{归一化路径: {出现位置, ...}}"""
    out = {}
    for pattern in SCAN_GLOBS:
        for f in sorted(ROOT.glob(pattern)):
            if ".test." in f.name or f.name.endswith(".min.js"):
                continue
            text = f.read_text(encoding="utf-8", errors="replace")
            for m in CALL_RE.finditer(text):
                raw = next((g for g in m.groups() if g), "")
                for cand in PATH_RE.findall(raw):
                    if not ENGINE_ROOTS.match(cand):
                        continue
                    path = norm_ui_path(cand)
                    if path in ("/", ""):
                        continue
                    out.setdefault(path, set()).add(f"{f.relative_to(ROOT)}")
    return out


def match_registered(path: str, registered: set) -> bool:
    """路径级匹配：字面相等，或与 `{}`/`{**}` 模式同段数匹配。"""
    if path in registered:
        return True
    seg = path.strip("/").split("/")
    for r in registered:
        rs = r.strip("/").split("/")
        if len(rs) != len(seg):
            continue
        ok = True
        for a, b in zip(seg, rs):
            if b in ("{}", "{**}") or a == "{}":
                continue
            if a != b:
                ok = False
                break
        if ok:
            return True
    return False


def load_list(path: Path, require_reason: bool):
    entries, problems = set(), []
    if not path.is_file():
        return entries, problems
    for lineno, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        body, _, reason = line.partition("#")
        parts = body.split()
        if len(parts) != 1:
            problems.append(f"{path}:{lineno} 格式错误（需要：<path>  # 理由）")
            continue
        if require_reason and not reason.strip():
            problems.append(f"{path}:{lineno} 缺少理由（豁免必须说明为什么）")
            continue
        entries.add(norm_ui_path(parts[0]))
    return entries, problems


def write_baseline(gaps) -> None:
    lines = [
        "# UI 调用 parity 基线（棘轮）—— 由 tools/check-ui-parity.py --write-baseline 生成，勿手改",
        "# 语义：这些是当前**已知**的\"UI 调了但后端没注册\"的路径，巡检不报警；",
        "#       不在这里的同类路径 = 新增缺口 → 必红。烧掉一条就删一行。",
        "",
    ]
    for p in sorted(gaps):
        lines.append(f"{p}")
    (ROOT / BASELINE_FILE).write_text("\n".join(lines) + "\n", encoding="utf-8")


def main() -> int:
    ap = argparse.ArgumentParser(description="UI 调用 ⊆ 已注册端点（棘轮）")
    ap.add_argument("--write-baseline", action="store_true")
    ap.add_argument("--list-gaps", action="store_true")
    ap.add_argument("--list-ignored", action="store_true")
    args = ap.parse_args()

    try:
        scan_ours = load_scan_ours()
    except Exception as e:  # 环境错误
        print(f"环境错误：{e}", file=sys.stderr)
        return 2
    registry, files = scan_ours(ROOT)
    registered = {p for v in registry.values() for p in v}

    calls = ui_calls()
    ignored, ignore_problems = load_list(ROOT / IGNORE_FILE, require_reason=True)
    baseline, baseline_problems = load_list(ROOT / BASELINE_FILE, require_reason=False)
    for p in ignore_problems + baseline_problems:
        print(f"警告：清单 {p}", file=sys.stderr)

    gaps = {}
    for path, where in calls.items():
        if path in ignored or any(match_registered(c, registered) for c in candidates(path)):
            continue
        gaps[path] = where
    new_gaps = {p: w for p, w in gaps.items() if p not in baseline}
    fixed = sorted(baseline - set(gaps))

    if args.write_baseline:
        write_baseline(gaps.keys())
        print(f"已写入基线 {BASELINE_FILE}：{len(gaps)} 条（UI 调用 {len(calls)} 条 / 已注册 {len(registered)} 条）")
        return 0

    print("UI 调用 parity 巡检（棘轮）")
    print(f"  UI 调用路径 {len(calls)} 条（扫描 {', '.join(SCAN_GLOBS)}）｜ 已注册端点 {len(registered)} 条（{len(files)} 个 Go 文件）")
    print(f"  已知缺口（基线）{len(baseline - ignored)} 条 ｜ 豁免 {len(ignored)} 条")

    if new_gaps:
        print(f"\n❌ 新增缺口 {len(new_gaps)} 条（UI 调了但后端没注册）：")
        for p in sorted(new_gaps):
            print(f"   {p}   ← {', '.join(sorted(new_gaps[p]))}")
        print(f"\n要么补端点/改用已注册路径，要么写进 {IGNORE_FILE} 并给理由；确认是遗留问题才可 --write-baseline。")
        status = 1
    else:
        print(f"\n✅ 无新增缺口（基线内 {len(baseline)} 条保持不变）")
        status = 0

    if fixed:
        print(f"\n可收紧基线：{len(fixed)} 条缺口已消失（跑 --write-baseline 收紧）")
        for p in fixed[:10]:
            print(f"   {p}")
    if args.list_gaps:
        print(f"\n存量缺口 {len(gaps)} 条：")
        for p in sorted(gaps):
            print(f"   {p}   ← {', '.join(sorted(gaps[p]))}")
    if args.list_ignored and ignored:
        print("\n豁免项：")
        for p in sorted(ignored):
            print(f"   {p}")
    return status


if __name__ == "__main__":
    sys.exit(main())
