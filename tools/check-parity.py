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

# 棘轮语义收在 tools/ratchet.py（唯一实现）；载入时不要写 __pycache__
sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
from ratchet import Ratchet  # noqa: E402

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


def _norm_entry(s: str) -> str:
    """清单行归一：压缩空白 + 方法大写（路径大小写敏感，不动）——棘轮按字符串比较用。"""
    flat = " ".join(s.split())
    head, _, rest = flat.partition(" ")
    if rest and head.upper() in METHODS:
        return f"{head.upper()} {rest}"
    return flat


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


def main() -> int:
    ap = argparse.ArgumentParser(description="上游端点 parity 巡检（棘轮）")
    ap.add_argument("--upstream", default=os.environ.get("UPSTREAM", str(DEFAULT_UPSTREAM)),
                    help=f"上游参照树路径（默认 {DEFAULT_UPSTREAM}）")
    ap.add_argument("--list-extra", action="store_true", help="列出本仓多出的端点")
    Ratchet.add_flags(ap)
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

    ratchet = Ratchet(
        label="上游端点 parity 巡检（棘轮）",
        baseline=str(BASELINE_FILE), ignore=str(IGNORE_FILE),
        entry_hint="<METHOD> <path>  # 理由",
        gap_noun="缺口",
        header=[
            "# 上游端点 parity 基线（棘轮）—— 由 tools/check-parity.py --write-baseline 生成，勿手改",
            f"# 上游参照：{version}",
            f"# 生成时间：{datetime.date.today().isoformat()}",
            "# 语义：这些是**当前已知**的端点缺口，巡检不报警；不在本文件里的缺口 = 新增缺口 → 必红。",
            "# 烧掉一条就删一行（或直接重跑 --write-baseline 收紧）。",
        ],
        normalize=_norm_entry,
        validate=lambda s: None if (len(s.split()) == 2 and s.split()[0].upper() in METHODS)
                           else "需要 <METHOD> <path> 两个字段",
        next_step=f"要么补实现，要么写进 {IGNORE_FILE} 并给理由；"
                  f"确认是遗留问题才可 --write-baseline 收进基线。",
    )

    up_total = sum(len(v) for v in up.values())
    our_total = sum(len(v) for v in ours.values())
    gaps = {f"{m} {p}": "" for m in METHODS for p in up[m] if p not in ours[m]}

    status = ratchet.report(
        gaps, args=args,
        summary=[f"上游 {up_root} @ {version}",
                 f"清单：上游 {up_total} 条 / 本仓 {our_total} 条注册（扫描 {len(our_files)} 个 Go 文件）"],
        notes=[f"{len(unresolved)} 个上游 route.js 未识别到方法导出，已跳过："
               + ", ".join(unresolved[:5]) + ("…" if len(unresolved) > 5 else "")] if unresolved else [],
    )

    if args.list_extra:
        extra = sorted(f"{m} {p}" for m in METHODS for p in ours[m] - up[m])
        print(f"\n本仓多出的端点（上游 src/app/api 里没有；引擎/代理路径属正常）：{len(extra)} 条")
        for key in extra:
            print(f"   {key}")

    return status


if __name__ == "__main__":
    sys.exit(main())
