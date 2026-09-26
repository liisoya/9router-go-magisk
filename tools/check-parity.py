#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""check-parity.py — 上游端点 parity 巡检（棘轮 / ratchet）

为什么要这个脚本
    本仓的仪表盘/控制台 API 是从上游 9router（Next.js App Router）**手写重写**成
    Go + chi 的：两份代码没有可 merge 的血缘，重新 clone 上游修不了任何东西。
    端点靠人对照移植 → 漏一个端点、少一个 HTTP 方法，既不会编译报错、也不会运行报错，
    只会在用户点到那个按钮时炸。2026-09-26 的 "Download Backup → 401 Invalid password"
    就是这一类"移植缺失"的样本。
    本脚本把"上游有哪些端点、各支持哪些方法"变成可执行断言。

棘轮（ratchet）语义
    存量缺口一次性补齐不现实（首次巡检出 130 条），所以用基线文件把"当前已知缺口"
    冻结下来：**只对新增缺口报警**。这既让"以后悄悄又少一个端点"不可能发生，
    又让存量缺口保持可见、可逐条烧掉（烧掉后跑 --write-baseline 收紧基线）。

    三类来源
      ① 本仓已注册（internal/handlers/**/*.go 的字面量注册）→ 通过
      ② tools/parity-baseline.txt 里的已知缺口            → 记录，不报警
      ③ tools/parity-ignore.txt 里有理由的豁免            → 通过（等价实现/产品决策）
    不在 ①②③ 里的缺口 = 新增缺口 → 退出码 1。

清单来源
    上游：<上游树>/src/app/api/**/route.js  →  路径 = /api/<目录段>，
          方法 = 该文件导出的 GET/POST/PUT/PATCH/DELETE（`export async function GET`
          与 `export const GET` 两种写法都认）。
    本仓：internal/handlers/**/*.go，按 `.Route("/前缀", func...)` 的嵌套层级推导前缀。

动态段归一化（两侧统一，避免参数名不同造成假差异）
    [id] / [slug]           → {}      （单段占位）
    [...slug] / [[...slug]] → {**}    （尾部通配）
    {id} / {id:[0-9]+}      → {}      （chi 路径参数，含正则约束）
    *（chi 尾部通配）        → {**}

用法
    python3 tools/check-parity.py                 # 巡检（CI / 提交前）
    python3 tools/check-parity.py --list-gaps     # 列出全部存量缺口（烧 backlog 用）
    python3 tools/check-parity.py --list-extra    # 列出本仓多出的端点（引擎/代理路径）
    python3 tools/check-parity.py --list-ignored  # 列出豁免项
    python3 tools/check-parity.py --write-baseline  # 把当前缺口写进基线（收紧棘轮）
    UPSTREAM=/path/to/9router python3 tools/check-parity.py

退出码
    0 = 无新增缺口；1 = 有新增缺口（移植缺失回归）；2 = 环境错误（上游参照树缺失等）
"""

import argparse
import datetime
import os
import re
import subprocess
import sys
from pathlib import Path

METHODS = ("GET", "POST", "PUT", "PATCH", "DELETE")
IGNORE_FILE = Path("tools") / "parity-ignore.txt"
BASELINE_FILE = Path("tools") / "parity-baseline.txt"
DEFAULT_UPSTREAM = Path("..") / "9router"


def canon(path: str) -> str:
    """归一化路径：动态段统一成 {} / {**}，去重斜杠与尾部斜杠。"""
    p = "/" + path.strip().lstrip("/")
    p = re.sub(r"\[\[?\.\.\.[^\]]*\]\]?", "{**}", p)  # [...slug] / [[...slug]]
    p = re.sub(r"\[[^\]]*\]", "{}", p)  # [id]
    p = re.sub(r"\{[^{}]*:[^{}]*\}", "{}", p)  # {id:[0-9]+}
    p = re.sub(r"\{[^{}]*\}", "{}", p)  # {id}
    p = re.sub(r"(?<=/)\*(?=$)", "{**}", p)  # chi 尾部通配 *
    p = re.sub(r"/{2,}", "/", p)
    if len(p) > 1:
        p = p.rstrip("/")
    return p or "/"


def scan_upstream(root: Path):
    """返回 (registry, unresolved)；registry = {METHOD: {canon_path, ...}}。"""
    api = root / "src" / "app" / "api"
    if not api.is_dir():
        return None, []
    registry, unresolved = {m: set() for m in METHODS}, []
    for f in sorted(api.rglob("route.js")):
        rel = f.parent.relative_to(api)
        path = canon("/api" + ("/" + str(rel).replace(os.sep, "/") if str(rel) != "." else ""))
        txt = f.read_text(encoding="utf-8", errors="replace")
        found = set(re.findall(r"export\s+(?:async\s+)?function\s+([A-Z]+)", txt))
        found |= set(re.findall(r"export\s+const\s+([A-Z]+)\s*[:=]", txt))
        found = {m for m in found if m in METHODS}
        if not found:
            unresolved.append(str(f.relative_to(root)))
            continue
        for m in found:
            registry[m].add(path)
    return registry, unresolved


def scan_ours(root: Path):
    """扫描本仓 chi 注册；返回 (registry, route_files)。"""
    registry = {m: set() for m in METHODS}
    files = []
    for f in sorted((root / "internal").rglob("*.go")):
        if f.name.endswith("_test.go"):
            continue
        src = f.read_text(encoding="utf-8", errors="replace")
        if not re.search(r"\.(Get|Post|Put|Patch|Delete)\(\s*\"", src):
            continue
        files.append(f)
        depth, stack = 0, []  # stack: [(开括号时的深度, 前缀), ...]
        for raw in src.splitlines():
            line = raw.split("//", 1)[0]
            m_route = re.search(r"\.Route\(\s*\"([^\"]+)\"", line)
            if m_route:
                stack.append((depth, m_route.group(1)))
            m_reg = re.search(r"\.(Get|Post|Put|Patch|Delete)\(\s*\"([^\"]+)\"", line)
            if m_reg:
                prefix = "".join(px for _, px in stack)
                registry[m_reg.group(1).upper()].add(canon(prefix + m_reg.group(2)))
            # 统计花括号深度前先抹掉字符串字面量（否则 {id} 会干扰配对）
            bare = re.sub(r"\"[^\"]*\"", '""', line)
            depth += bare.count("{") - bare.count("}")
            while stack and depth <= stack[-1][0]:
                stack.pop()
    return registry, files


def load_pairs(path: Path, require_reason: bool):
    """解析 `<METHOD> <path>  # 理由(可选/必填)` 形式的清单。"""
    entries, problems = set(), []
    if not path.is_file():
        return entries, problems
    for lineno, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), 1):
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        body, _, reason = line.partition("#")
        parts = body.split()
        if len(parts) != 2 or parts[0].upper() not in METHODS:
            problems.append(f"{path}:{lineno} 格式错误（需要：<METHOD> <path>  # 理由）")
            continue
        if require_reason and not reason.strip():
            problems.append(f"{path}:{lineno} 缺少理由（豁免必须说明为什么）")
            continue
        entries.add((parts[0].upper(), canon(parts[1])))
    return entries, problems


def upstream_version(root: Path) -> str:
    """尽力取上游版本号，用于基线文件溯源；失败不致命。"""
    def run(*args):
        try:
            out = subprocess.run(["git", "-C", str(root), *args], capture_output=True,
                                 text=True, timeout=10)
            return out.stdout.strip() if out.returncode == 0 else ""
        except Exception:
            return ""
    sha, tag = run("rev-parse", "--short", "HEAD"), run("describe", "--tags", "--always")
    if tag and tag != sha:
        return f"{tag} ({sha})" if sha else tag
    return tag or sha or "未知"


def write_baseline(root: Path, gaps, version: str) -> None:
    path = root / BASELINE_FILE
    header = [
        "# 上游端点 parity 基线（棘轮）—— 由 tools/check-parity.py --write-baseline 生成，勿手改",
        f"# 上游参照：{version}",
        f"# 生成时间：{datetime.date.today().isoformat()}",
        "# 语义：这些是**当前已知**的端点缺口，巡检不报警；不在本文件里的缺口 = 新增缺口 → 必红。",
        "# 烧掉一条就删一行（或直接重跑 --write-baseline 收紧）。",
        "",
    ]
    lines = [f"{m:6} {p}" for m, p in sorted(gaps)]
    path.write_text("\n".join(header + lines) + "\n", encoding="utf-8")


def main() -> int:
    ap = argparse.ArgumentParser(description="上游端点 parity 巡检（棘轮）")
    ap.add_argument("--upstream", default=os.environ.get("UPSTREAM", str(DEFAULT_UPSTREAM)),
                    help=f"上游参照树路径（默认 {DEFAULT_UPSTREAM}）")
    ap.add_argument("--write-baseline", action="store_true", help="把当前缺口写入基线文件")
    ap.add_argument("--list-gaps", action="store_true", help="列出全部存量缺口")
    ap.add_argument("--list-extra", action="store_true", help="列出本仓多出的端点")
    ap.add_argument("--list-ignored", action="store_true", help="列出豁免项")
    args = ap.parse_args()

    root = Path(__file__).resolve().parent.parent
    up_root = Path(args.upstream).expanduser().resolve()
    if not up_root.is_dir():
        print(f"环境错误：上游参照树不存在 {up_root}", file=sys.stderr)
        print("  → git clone https://github.com/decolua/9router.git ../9router", file=sys.stderr)
        return 2

    up, unresolved = scan_upstream(up_root)
    if up is None:
        print(f"环境错误：{up_root}/src/app/api 不存在（不是上游树？）", file=sys.stderr)
        return 2
    ours, our_files = scan_ours(root)
    version = upstream_version(up_root)

    ignored, ignore_problems = load_pairs(root / IGNORE_FILE, require_reason=True)
    baseline, baseline_problems = load_pairs(root / BASELINE_FILE, require_reason=False)
    for p in ignore_problems + baseline_problems:
        print(f"警告：清单 {p}", file=sys.stderr)

    gaps = {(m, p) for m in METHODS for p in up[m]
            if (m, p) not in ignored and p not in ours[m]}
    new_gaps, fixed = sorted(gaps - baseline), sorted(baseline - gaps)

    if args.write_baseline:
        write_baseline(root, gaps, version)
        print(f"已写入基线 {BASELINE_FILE}：{len(gaps)} 条已知缺口（上游 {version}）")
        return 0

    up_total = sum(len(v) for v in up.values())
    print("上游端点 parity 巡检（棘轮）")
    print(f"  上游 {up_root} @ {version}")
    print(f"  清单：上游 {up_total} 条 / 本仓 {sum(len(v) for v in ours.values())} 条注册"
          f"（扫描 {len(our_files)} 个 Go 文件）")
    print(f"  已知缺口（基线）{len(baseline - ignored)} 条 ｜ 豁免 {len(ignored)} 条")
    if unresolved:
        print(f"  注意：{len(unresolved)} 个上游 route.js 未识别到方法导出，已跳过："
              + ", ".join(unresolved[:5]) + ("…" if len(unresolved) > 5 else ""))

    if new_gaps:
        print(f"\n❌ 新增缺口 {len(new_gaps)} 条（不在基线内 —— 这就是『移植缺失』回归）：")
        for m, p in new_gaps:
            print(f"   {m:6} {p}")
        print(f"\n要么补实现，要么写进 {IGNORE_FILE} 并给理由；"
              f"确认是遗留问题才可 --write-baseline 收进基线。")
        status = 1
    else:
        print(f"\n✅ 无新增缺口（基线内 {len(baseline)} 条存量缺口保持不变）")
        status = 0

    if fixed:
        print(f"\n可收紧基线：{len(fixed)} 条缺口已消失（跑 --write-baseline 收紧）")
        for m, p in fixed[:10]:
            print(f"   {m:6} {p}")
        if len(fixed) > 10:
            print(f"   …另有 {len(fixed) - 10} 条")

    if args.list_gaps:
        print(f"\n存量缺口 {len(gaps)} 条：")
        for m, p in sorted(gaps):
            print(f"   {m:6} {p}")

    if args.list_ignored and ignored:
        print("\n豁免项：")
        for m, p in sorted(ignored):
            print(f"   {m:6} {p}")

    if args.list_extra:
        extra = sorted((m, p) for m in METHODS for p in ours[m] - up[m])
        print(f"\n本仓多出的端点（上游 src/app/api 里没有；引擎/代理路径属正常）：{len(extra)} 条")
        for m, p in extra:
            print(f"   {m:6} {p}")

    return status


if __name__ == "__main__":
    sys.exit(main())
