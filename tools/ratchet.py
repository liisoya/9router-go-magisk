#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""ratchet.py — 棘轮（ratchet）机制的唯一实现：基线 + 豁免 + 只拦新增。

为什么有这个 module
    2026-09-26 一天里加了三个门禁（端点 parity、UI 调用 parity、handler 挂载），三份脚本各自
    抄了一遍同一套机械结构：清单解析（格式校验 + 豁免理由必填）、基线写盘、`new = gaps - baseline`
    与 `fixed = baseline - gaps`、`--write-baseline` / `--list-*`、退出码 0/1/2。
    加第四个门禁还得再抄一遍 —— 于是把它收成一个 module。

interface（刻意保持小）
    构造时说明"我是谁、基线/豁免文件在哪、清单行长什么样、文案用什么"；
    scanner 只提供 `gaps: dict[key, 说明]`，其余（比较/打印/收紧/退出码）都不用管。

    ratchet = Ratchet(label=..., baseline=..., ignore=..., entry_hint=..., header=[...], ...)
    baseline, ignored = ratchet.load()          # 解析两个清单（含理由必填校验）
    gaps = {...}                                # scanner 的产物：key → 说明文字
    return ratchet.report(gaps, args=args, summary=[...], notes=[...])   # 打印 + 退出码

    标志位统一由 `Ratchet.add_flags(parser)` 添加（三个脚本的 CLI 因此完全一致）。

语义（三个门禁一致）
    缺口 key 已在 ignore 里      → 豁免，不报警
    缺口 key 在 baseline 里      → 存量，不报警（但会提示"可收紧"）
    baseline 里的 key 不再是缺口 → 提示收紧（--write-baseline）
    其余缺口                     → **新增** → ❌ + 退出码 1
    环境错误                     → 退出码 2（由各脚本自己返回）

边界（不假装覆盖）
    - key 一律是**字符串**：`GET /api/x`、`/v1/web/fetch`、`RegisterRoutes` 都行；比较是字符串相等，
      归一化（大小写/空白/路径形状）由 scanner 通过 `normalize=` 提供，不由这里猜。
    - 这里只管"集合语义"，不管"怎么扫出来"（那是 scanner 的事，也是 seam 所在）。
"""

from __future__ import annotations

import argparse
from pathlib import Path
from typing import Callable, Iterable, Sequence

ROOT = Path(__file__).resolve().parent.parent


def _identity(s: str) -> str:
    return s


class Ratchet:
    def __init__(
        self,
        *,
        label: str,
        baseline: str,
        ignore: str,
        entry_hint: str,
        header: Sequence[str],
        gap_noun: str = "缺口",
        fixed_note: str = "已消失",
        list_title: str | None = None,
        next_step: str | None = None,
        normalize: Callable[[str], str] | None = None,
        validate: Callable[[str], str | None] | None = None,
    ) -> None:
        self.label = label
        self.baseline_path = Path(baseline)
        self.ignore_path = Path(ignore)
        self.entry_hint = entry_hint
        self.header = list(header)
        self.gap_noun = gap_noun
        self.fixed_note = fixed_note
        self.list_title = list_title or f"存量{gap_noun}"
        self.next_step = next_step or (
            f"要么补实现，要么写进 {ignore} 并给理由；确认是遗留问题才可 --write-baseline。"
        )
        self.normalize = normalize or _identity
        # 可选：清单行的形状校验（返回错误说明 = 不合格）。不传则任何非注释行都当 key ——
        # 那会让"手抖写错一行"静默变成一条永远匹配不上的基线项（只出现在"可收紧"里）。
        self.validate = validate

    # ── 清单 I/O（格式校验与理由校验在唯一一处）──────────────────────────────
    def _load_list(self, path: Path, *, require_reason: bool) -> tuple[set[str], list[str]]:
        entries: set[str] = set()
        problems: list[str] = []
        full = ROOT / path
        if not full.is_file():
            return entries, problems
        for lineno, raw in enumerate(full.read_text(encoding="utf-8").splitlines(), 1):
            line = raw.strip()
            if not line or line.startswith("#"):
                continue
            body, _, reason = line.partition("#")
            key = body.strip()
            if not key:
                problems.append(f"{path}:{lineno} 格式错误（需要：{self.entry_hint}）")
                continue
            if self.validate is not None:
                problem = self.validate(key)
                if problem:
                    problems.append(f"{path}:{lineno} 格式错误（{problem}；需要：{self.entry_hint}）")
                    continue
            if require_reason and not reason.strip():
                problems.append(f"{path}:{lineno} 缺少理由（豁免必须说明为什么）")
                continue
            entries.add(self.normalize(key))
        return entries, problems

    def load(self) -> tuple[set[str], set[str]]:
        """返回 (baseline, ignored)；格式问题打印成警告而不是静默吞掉。"""
        ignored, ignore_problems = self._load_list(self.ignore_path, require_reason=True)
        baseline, baseline_problems = self._load_list(self.baseline_path, require_reason=False)
        for p in ignore_problems + baseline_problems:
            print(f"警告：清单 {p}", file=__import__("sys").stderr)
        return baseline, ignored

    # ── 收紧基线 ────────────────────────────────────────────────────────────
    def write_baseline(self, gaps) -> None:
        lines = list(self.header) + [""]
        for key in sorted(self.normalize(k) for k in gaps):
            lines.append(key)
        (ROOT / self.baseline_path).write_text("\n".join(lines) + "\n", encoding="utf-8")

    # ── CLI 标志位（三个脚本一致）───────────────────────────────────────────
    @staticmethod
    def add_flags(ap: argparse.ArgumentParser, *, list_flag: str = "--list-gaps") -> None:
        ap.add_argument("--write-baseline", action="store_true", help="把当前缺口写入基线文件（收紧棘轮）")
        ap.add_argument(list_flag, action="store_true", help="列出全部存量缺口")
        ap.add_argument("--list-ignored", action="store_true", help="列出豁免项")

    # ── 比较 + 打印 + 退出码（唯一的"棘轮语义"实现处）────────────────────────
    def report(
        self,
        gaps: dict[str, str],
        *,
        args: argparse.Namespace,
        summary: Iterable[str] = (),
        notes: Iterable[str] = (),
        list_flag: str = "--list-gaps",
    ) -> int:
        baseline, ignored = self.load()
        gaps = {self.normalize(k): v for k, v in gaps.items()}
        gaps = {k: v for k, v in gaps.items() if k not in ignored}
        new_gaps = {k: v for k, v in gaps.items() if k not in baseline}
        fixed = sorted(baseline - set(gaps))

        if getattr(args, "write_baseline", False):
            self.write_baseline(gaps)
            print(f"已写入基线 {self.baseline_path}：{len(gaps)} 条已知{self.gap_noun}")
            return 0

        print(self.label)
        for line in summary:
            print(f"  {line}")
        print(f"  已知{self.gap_noun}（基线）{len(baseline - ignored)} 条 ｜ 豁免 {len(ignored)} 条")
        for line in notes:
            print(f"  注意：{line}")

        if new_gaps:
            print(f"\n❌ 新增{self.gap_noun} {len(new_gaps)} 条：")
            for k in sorted(new_gaps):
                extra = f"   {new_gaps[k]}" if new_gaps[k] else ""
                print(f"   {k}{extra}")
            print(f"\n{self.next_step}")
            status = 1
        else:
            print(f"\n✅ 无新增{self.gap_noun}（基线内 {len(baseline)} 条保持不变）")
            status = 0

        if fixed:
            print(f"\n可收紧基线：{len(fixed)} 条{self.gap_noun}{self.fixed_note}（跑 --write-baseline 收紧）")
            for k in fixed[:10]:
                print(f"   {k}")
            if len(fixed) > 10:
                print(f"   …另有 {len(fixed) - 10} 条")

        if getattr(args, list_flag.lstrip("-").replace("-", "_"), False):
            print(f"\n{self.list_title} {len(gaps)} 条：")
            for k in sorted(gaps):
                extra = f"   {gaps[k]}" if gaps[k] else ""
                print(f"   {k}{extra}")

        if getattr(args, "list_ignored", False) and ignored:
            print("\n豁免项：")
            for k in sorted(ignored):
                print(f"   {k}")

        return status
