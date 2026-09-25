#!/usr/bin/env python3
"""schema 漂移断言：上游 DATABASE.md 是唯一真相源，schema.sql 是手工拷贝。

背景：Go 引擎无迁移系统，模块靠 schema.sql 幂等建表。上游加表/加列时
手工拷贝会静默漂移（老用户升级永不建新表）。本工具把对照变成构建期断言。

用法：
  python3 tools/gen-schema.py --check           # 不一致 → exit 1（构建失败）
  python3 tools/gen-schema.py --diff            # 打印差异（人工核对用）

规则：语句归一化（剥注释 / IF NOT EXISTS / 折叠空白）后按 (类型, 名字) 集合比对。
_meta 表是 Node.js 专用（Go 不用），在 SKIP 集合中豁免。
"""
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DATABASE_MD = ROOT / "DATABASE.md"
SCHEMA_SQL = ROOT / "module" / "etc" / "schema.sql"
SKIP_TABLES = {"_meta"}  # Node.js 专用 schema 版本表，Go 侧不建


def normalize(sql: str) -> str:
    sql = re.sub(r"--[^\n]*", "", sql)                     # 剥行注释
    sql = re.sub(r"\bIF\s+NOT\s+EXISTS\b", "", sql, flags=re.I)
    sql = re.sub(r"\s+", " ", sql).strip().rstrip(";").strip()
    return sql


def extract(path: Path) -> dict:
    """返回 {(kind, name): 归一化语句}。kind ∈ TABLE / INDEX / UNIQUE INDEX。
    用 finditer 精确定位语句（按 ';' 切块会被 markdown 散文打断）。"""
    text = path.read_text(encoding="utf-8")
    text = re.sub(r"--[^\n]*", "", text)                   # 先剥注释
    out = {}
    for m in re.finditer(
            r"CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)\s*\((.*?)\)\s*;",
            text, re.S | re.I):
        name = m.group(1)
        if name in SKIP_TABLES:
            continue
        out[("TABLE", name)] = normalize(f"CREATE TABLE {name} ({m.group(2)})")
    for m in re.finditer(
            r"CREATE\s+(UNIQUE\s+)?INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)\s+ON\s+(.+?);",
            text, re.S | re.I):
        uniq = "UNIQUE " if m.group(1) else ""
        out[("INDEX", m.group(2))] = normalize(f"CREATE {uniq}INDEX {m.group(2)} ON {m.group(3)}")
    return out


def main() -> int:
    argv = sys.argv[1:]
    mode = "--diff" if "--diff" in argv else "--check"
    upstream = extract(DATABASE_MD)
    ours = extract(SCHEMA_SQL)
    missing = sorted(set(upstream) - set(ours))
    extra = sorted(set(ours) - set(upstream))
    drifted = sorted(k for k in set(upstream) & set(ours) if upstream[k] != ours[k])

    if mode == "--diff":
        for k in missing:
            print(f"[缺失] {k[0]} {k[1]}\n  上游: {upstream[k]}\n")
        for k in extra:
            print(f"[多余] {k[0]} {k[1]}\n  本地: {ours[k]}\n")
        for k in drifted:
            print(f"[漂移] {k[0]} {k[1]}\n  上游: {upstream[k]}\n  本地: {ours[k]}\n")
        if not (missing or extra or drifted):
            print("schema 与上游一致 ✅")
        return 0

    if missing or extra or drifted:
        print("schema 漂移：module/etc/schema.sql 与上游 DATABASE.md 不一致！", file=sys.stderr)
        for k in missing:
            print(f"  [缺失] {k[0]} {k[1]}", file=sys.stderr)
        for k in extra:
            print(f"  [多余] {k[0]} {k[1]}", file=sys.stderr)
        for k in drifted:
            print(f"  [漂移] {k[0]} {k[1]}", file=sys.stderr)
        print("同步方法：python3 tools/gen-schema.py --diff 查看详情，更新 schema.sql 后重试。",
              file=sys.stderr)
        print("（SKIP_SCHEMA_CHECK=1 可跳过本检查——不建议）", file=sys.stderr)
        return 1
    print("schema check: 与上游 DATABASE.md 一致 ✅")
    return 0


if __name__ == "__main__":
    sys.exit(main())
