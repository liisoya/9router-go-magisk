#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""check-dead-handlers.py — HTTP handler 是否真的被挂载（棘轮）

为什么有它
    2026-09-26 一天内抓到两次同一类问题：**handler 写好了、测试也覆盖了，但从来没人把它挂到路由上**
    —— `media.HandleWebFetch`（Dashboard 的网页抓取按钮点了必 404）与 `dashboard.RegisterRoutes`
    （整个函数没被调用）。这类问题编译通过、单测通过（测试直接调 handler），只有用户点到才暴露。

判定方式
    1. 收集 `internal/**/*.go`（非测试）里所有 `func [(recv)] HandleXxx(...)` 定义；
    2. 统计该名字在**非测试** Go 文件里出现的次数：只出现 1 次（就是定义处）= **未挂载**；
    3. 若同时被 `*_test.go` 引用 → 标记 `test-only`（"有测试但没路由"，比纯死代码更可疑）。

棘轮（ratchet）语义
    上游引擎本来就有一批未被挂载/仅供内部调用的 handler（历史包袱）。存量冻结在
    tools/dead-handlers-baseline.txt，**只对新增报警**；确认无害的写进
    tools/dead-handlers-ignore.txt（理由必填）。

已知边界（不假装覆盖）
    - 名字级判定：通过变量/循环/接口间接注册的会被算成"已挂载"（宁可漏报，不误报）
    - 同名 handler 分布在不同包时会合并计数（罕见，出现时人工核对）

用法
    python3 tools/check-dead-handlers.py                # 巡检（新增即失败）
    python3 tools/check-dead-handlers.py --list         # 列出全部存量
    python3 tools/check-dead-handlers.py --write-baseline
退出码：0 = 无新增；1 = 有新增；2 = 环境错误
"""

import argparse
import re
import sys
from pathlib import Path

# 棘轮语义收在 tools/ratchet.py（唯一实现）；载入时不要写 __pycache__（上游 .gitignore 不该为它改）
sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
from ratchet import Ratchet  # noqa: E402

ROOT = Path(__file__).resolve().parent.parent
BASELINE_FILE = Path("tools") / "dead-handlers-baseline.txt"
IGNORE_FILE = Path("tools") / "dead-handlers-ignore.txt"
# 两类"本该被挂载/被调用"的名字：Handle*（HTTP handler）与 Register*/Setup*/Routes*（注册函数）
DEF_RE = re.compile(r"^func\s+(?:\([^)]*\)\s+)?(Handle[A-Z]\w*|Register\w*|Setup\w*|Routes?[A-Z]\w*)\s*\(", re.M)


def go_files():
    files = sorted((ROOT / "internal").rglob("*.go"))
    return [f for f in files if not f.name.endswith("_test.go")], [f for f in files if f.name.endswith("_test.go")]


def main() -> int:
    ap = argparse.ArgumentParser(description="handler 挂载巡检（棘轮）")
    ap.add_argument("--list", action="store_true", help="列出全部未挂载项（--list-gaps 的别名）")
    Ratchet.add_flags(ap)
    args = ap.parse_args()

    non_test, tests = go_files()
    if not non_test:
        print("环境错误：internal/ 下没有 Go 文件", file=sys.stderr)
        return 2

    definitions = {}  # name -> file
    for f in non_test:
        for m in DEF_RE.finditer(f.read_text(encoding="utf-8", errors="replace")):
            definitions.setdefault(m.group(1), f.relative_to(ROOT))

    # 非测试文件里的总出现次数（含定义），以及测试文件里的出现次数。
    # **必须先剥注释**：否则函数名出现在自己的文档注释里就会被当成"被引用"（假阴性 ——
    # 实测 dashboard.RegisterRoutes 正是这样漏过的）。
    def strip_comments(text: str) -> str:
        text = re.sub(r"/\*.*?\*/", "", text, flags=re.S)
        return re.sub(r"//[^\n]*", "", text)

    non_test_text = "\n".join(strip_comments(f.read_text(encoding="utf-8", errors="replace")) for f in non_test)
    test_text = "\n".join(strip_comments(f.read_text(encoding="utf-8", errors="replace")) for f in tests)

    gaps: dict[str, str] = {}
    for name, where in definitions.items():
        if len(re.findall(rf"\b{name}\b", non_test_text)) > 1:
            continue  # 定义之外还有引用 → 视为已挂载
        kind = "test-only（有测试、无路由）" if re.search(rf"\b{name}\b", test_text) else "no-reference"
        gaps[name] = f"{kind}   ← {where}"

    ratchet = Ratchet(
        label="handler 挂载巡检（棘轮）",
        baseline=str(BASELINE_FILE), ignore=str(IGNORE_FILE),
        entry_hint="<HandleName|RegisterName>  # 理由",
        gap_noun="未挂载 handler",
        fixed_note="已被挂载",
        list_title="未挂载清单",
        header=[
            "# 未挂载 handler 基线（棘轮）—— 由 tools/check-dead-handlers.py --write-baseline 生成，勿手改",
            "# 语义：这些 handler 当前没有路由引用（多为上游历史包袱），巡检不报警；",
            "#       不在这里的同类 handler = 新增 → 必红。",
        ],
        next_step=f"要么挂上路由，要么写进 {IGNORE_FILE} 并给理由；确认是历史包袱才可 --write-baseline。",
        validate=lambda s: None if s.isidentifier() else "需要标识符（HandleXxx / RegisterXxx）",
    )
    return ratchet.report(
        gaps, args=args,
        summary=[f"定义 {len(definitions)} 个 Handle* ｜ 当前未挂载 {len(gaps)} 条"],
        list_flag="--list",
    )


if __name__ == "__main__":
    sys.exit(main())
