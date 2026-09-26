#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""test_ratchet.py — 棘轮 module 的接口级单测（`python3 -m unittest discover -s tools -p 'test_*.py'`）

为什么测这里：三个门禁脚本的"基线 / 豁免 / 只拦新增 / 收紧"语义**只此一处实现**，
所以这里绿 = 三个门禁的机械结构都绿（interface 即测试面）。scanner 各自的扫描逻辑
仍由各自的巡检在真仓上验证。
"""

import argparse
import io
import sys
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
from ratchet import Ratchet  # noqa: E402


def write(path: Path, text: str) -> Path:
    path.write_text(text, encoding="utf-8")
    return path


def build(**kw):
    """构造一个指向临时清单的 Ratchet（绝对路径会覆盖 ROOT 前缀）。"""
    tmp = Path(tempfile.mkdtemp())
    base = write(tmp / "baseline.txt", kw.pop("baseline_text", ""))
    ign = write(tmp / "ignore.txt", kw.pop("ignore_text", ""))
    return Ratchet(label="测试棘轮", baseline=str(base), ignore=str(ign),
                   entry_hint="<key>  # 理由", header=["# 测试基线"], **kw), base, ign


class TestRatchet(unittest.TestCase):
    def test_load_skips_comments_and_blank_lines(self):
        # 注释与空行被跳过，其余按 key 收进集合
        r, _, _ = build(baseline_text="# 头注释\n\nGET /api/a\n\n# 尾注释\nGET /api/b\n")
        baseline, ignored = r.load()
        self.assertEqual(baseline, {"GET /api/a", "GET /api/b"})
        self.assertEqual(ignored, set())

    def test_load_rejects_ignore_without_reason(self):
        # 豁免缺理由必须被拒，而且必须有人喊（否则"随便写一行就绕过门禁"）
        r, _, _ = build(ignore_text="/api/x  # 有理由\n/api/y\n")
        buf = io.StringIO()
        with redirect_stderr(buf):
            _, ignored = r.load()
        self.assertEqual(ignored, {"/api/x"})
        self.assertNotIn("/api/y", ignored)
        self.assertIn("缺少理由", buf.getvalue())

    def test_load_reports_shape_problems_when_validator_given(self):
        # 这条单测第一次跑就抓到迁移漏了东西：抽掉旧脚本的"清单行形状校验"后，
        # "格式错误"分支变成死代码（注释/空行被跳过，其余一律当 key 接受） →
        # 手抖写错的一行会静默变成"永远匹配不上的基线项"。于是补了可选 validate。
        def must_be_method_path(s: str):
            parts = s.split()
            return None if len(parts) == 2 else "需要 <METHOD> <path> 两个字段"

        r, _, _ = build(baseline_text="GET /api/a\nGET /api/b 多了一列\n", validate=must_be_method_path)
        buf = io.StringIO()
        with redirect_stderr(buf):
            baseline, _ = r.load()
        self.assertEqual(baseline, {"GET /api/a"})
        self.assertIn("格式错误", buf.getvalue())

    def test_load_without_validator_accepts_any_key(self):
        # 不传 validate 时不做形状判断（key 形状交给 scanner 自己的归一化）
        r, _, _ = build(baseline_text="/v1/web/fetch\nRegisterRoutes\n")
        baseline, _ = r.load()
        self.assertEqual(baseline, {"/v1/web/fetch", "RegisterRoutes"})

    def test_report_new_gap_fails_with_1(self):
        r, _, _ = build()
        out = io.StringIO()
        with redirect_stdout(out):
            code = r.report({"GET /api/new": "← somefile.ts"}, args=argparse.Namespace())
        self.assertEqual(code, 1)
        self.assertIn("❌ 新增缺口 1 条", out.getvalue())
        self.assertIn("GET /api/new", out.getvalue())

    def test_report_baseline_gap_is_quiet_and_hints_tightening(self):
        r, _, _ = build(baseline_text="GET /api/old\nGET /api/gone\n")
        out = io.StringIO()
        with redirect_stdout(out):
            code = r.report({"GET /api/old": ""}, args=argparse.Namespace())
        self.assertEqual(code, 0)
        self.assertIn("✅ 无新增缺口", out.getvalue())
        self.assertIn("可收紧基线：1 条", out.getvalue())
        self.assertIn("GET /api/gone", out.getvalue())

    def test_report_ignored_entry_is_not_a_gap(self):
        r, _, _ = build(ignore_text="GET /api/ignored  # 等价实现\n")
        out = io.StringIO()
        with redirect_stdout(out):
            code = r.report({"GET /api/ignored": ""}, args=argparse.Namespace())
        self.assertEqual(code, 0)
        self.assertIn("豁免 1 条", out.getvalue())

    def test_write_baseline_then_report_is_green(self):
        r, base, _ = build()
        with redirect_stdout(io.StringIO()):
            code = r.report({"GET /api/a": ""}, args=argparse.Namespace(write_baseline=True))
        self.assertEqual(code, 0)
        self.assertIn("GET /api/a", base.read_text(encoding="utf-8"))
        out = io.StringIO()
        with redirect_stdout(out):
            code = r.report({"GET /api/a": ""}, args=argparse.Namespace())
        self.assertEqual(code, 0)
        self.assertIn("✅ 无新增缺口", out.getvalue())

    def test_normalize_applies_to_both_sides(self):
        # 清单里写成多空格、缺口里也写成多空格 —— 归一化后必须相等（否则假红）
        norm = lambda s: " ".join(s.split())  # noqa: E731
        r, _, _ = build(baseline_text="GET    /api/a\n", normalize=norm)
        out = io.StringIO()
        with redirect_stdout(out):
            code = r.report({"GET    /api/a": ""}, args=argparse.Namespace(), )
        self.assertEqual(code, 0, out.getvalue())

    def test_report_list_gaps_prints_entries_with_details(self):
        r, _, _ = build(baseline_text="/v1/x\n")
        out = io.StringIO()
        with redirect_stdout(out):
            r.report({"/v1/x": "← Detail.svelte"}, args=argparse.Namespace(list_gaps=True))
        self.assertIn("存量缺口 1 条", out.getvalue())
        self.assertIn("← Detail.svelte", out.getvalue())

    def test_add_flags_gives_identical_cli_to_all_scripts(self):
        ap = argparse.ArgumentParser()
        Ratchet.add_flags(ap)
        ns = ap.parse_args(["--write-baseline", "--list-gaps", "--list-ignored"])
        self.assertTrue(ns.write_baseline and ns.list_gaps and ns.list_ignored)


if __name__ == "__main__":
    unittest.main()
