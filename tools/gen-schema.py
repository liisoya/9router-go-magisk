#!/usr/bin/env python3
"""schema 漂移断言：上游 DATABASE.md 是表结构的真相源，schema.sql 是手工拷贝。

背景：Go 引擎无迁移系统，模块靠 schema.sql 幂等建表。上游加表/加列时
手工拷贝会静默漂移（老用户升级永不建新表）。本工具把对照变成构建期断言。

v1.9.2 起上游 DATABASE.md 只文档化"核心表"（索引与部分列改为指向
`src/lib/db/schema.js`），因此校验范围收敛为：
  1. 表必须一一对应（缺失/多余都报）
  2. 每个表的**列**必须对齐（列名与类型）
  3. 本地可以有 SUPERSET_COLUMNS 里登记的有意超集列 —— 这些列由引擎代码真实使用
     （见下方注释），删掉会直接破坏功能或首装性能
  4. 索引不再与 DATABASE.md 比对（上游已不文档化）；--diff 仍会列出本地索引供人工核对

用法：
  python3 tools/gen-schema.py --check           # 不一致 → exit 1（构建失败）
  python3 tools/gen-schema.py --diff            # 打印差异（人工核对用）

规则：语句归一化（剥注释 / IF NOT EXISTS / 折叠空白）后比对；_meta 表是 Node.js 专用。
"""
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
DATABASE_MD = ROOT / "DATABASE.md"
SCHEMA_SQL = ROOT / "module" / "etc" / "schema.sql"
SKIP_TABLES = {"_meta"}  # Node.js 专用 schema 版本表，Go 侧不建

# 本地相对上游 DATABASE.md 的**有意超集列**（上游 v1.9.2 起不再逐列文档化，见文件头）。
# 每一条都必须有代码依据，否则就是漂移：
#   lastUsedAt / consecutiveUseCount —— internal/db/usage.go:67 的轮换计数 UPDATE 与
#   internal/handlers/dashboard/connections.go:257 的字段白名单在用；缺列即功能坏。
SUPERSET_COLUMNS = {
    "providerConnections": {"lastUsedAt", "consecutiveUseCount"},
}


def normalize(sql: str) -> str:
    sql = re.sub(r"--[^\n]*", "", sql)                     # 剥行注释
    sql = re.sub(r"\bIF\s+NOT\s+EXISTS\b", "", sql, flags=re.I)
    sql = re.sub(r"\s+", " ", sql).strip().rstrip(";").strip()
    return sql


def split_top_level(body: str):
    """按顶层逗号切分（括号内的逗号不算），用于拆列定义。"""
    depth, cur, parts = 0, "", []
    for ch in body:
        if ch == "(":
            depth += 1
        elif ch == ")":
            depth -= 1
        if ch == "," and depth == 0:
            parts.append(cur)
            cur = ""
        else:
            cur += ch
    parts.append(cur)
    return [p.strip() for p in parts if p.strip()]


def col_defs(body: str) -> dict:
    """{列名: 归一化类型串}；表级约束（PRIMARY KEY(...)/UNIQUE(...) 等）跳过。"""
    out = {}
    for part in split_top_level(body):
        if re.match(r"(?i)^(PRIMARY\s+KEY|UNIQUE|FOREIGN\s+KEY|CHECK|CONSTRAINT)\b", part):
            continue
        m = re.match(r'"?([A-Za-z_]\w*)"?\s+(.*)$', part, re.S)
        if not m:
            continue
        out[m.group(1)] = re.sub(r"\s+", " ", m.group(2)).strip()
    return out


def extract(path: Path) -> dict:
    """返回 {(kind, name): 语句}；kind ∈ TABLE / INDEX。finditer 精确定位语句。"""
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


def body_of(stmt: str, name: str) -> str:
    return stmt[stmt.index("(") + 1:stmt.rindex(")")]


def main() -> int:
    argv = sys.argv[1:]
    mode = "--diff" if "--diff" in argv else "--check"
    upstream, ours = extract(DATABASE_MD), extract(SCHEMA_SQL)
    up_t = {k: v for k, v in upstream.items() if k[0] == "TABLE"}
    our_t = {k: v for k, v in ours.items() if k[0] == "TABLE"}
    up_i = {k: v for k, v in upstream.items() if k[0] != "TABLE"}
    our_i = {k: v for k, v in ours.items() if k[0] != "TABLE"}

    missing = sorted(set(up_t) - set(our_t))
    extra = sorted(set(our_t) - set(up_t))

    col_issues = []  # (表名, 说明, 上游, 本地)
    for k in sorted(set(up_t) & set(our_t)):
        table = k[1]
        up_cols, our_cols = col_defs(body_of(up_t[k], table)), col_defs(body_of(our_t[k], table))
        allowed = SUPERSET_COLUMNS.get(table, set())
        for c in sorted(set(up_cols) - set(our_cols)):
            col_issues.append((table, f"缺列 {c}", up_cols[c], ""))
        for c in sorted(set(our_cols) - set(up_cols) - allowed):
            col_issues.append((table, f"多余列 {c}", "", our_cols[c]))
        for c in sorted(set(up_cols) & set(our_cols)):
            if up_cols[c] != our_cols[c]:
                col_issues.append((table, f"类型漂移 {c}", up_cols[c], our_cols[c]))

    if mode == "--diff":
        for k in missing:
            print(f"[缺失] TABLE {k[1]}\n  上游: {up_t[k]}\n")
        for k in extra:
            print(f"[多余] TABLE {k[1]}\n  本地: {our_t[k]}\n")
        for table, what, up, local in col_issues:
            print(f"[漂移] TABLE {table} {what}\n  上游: {up}\n  本地: {local}\n")
        print("本地有而上游 DATABASE.md 未文档化的索引（v1.9.2 起不再比对，仅供核对）："
              f"{len(set(our_i) - set(up_i))} 条")
        for k in sorted(set(our_i) - set(up_i)):
            print(f"  {our_i[k]}")
        if not (missing or extra or col_issues):
            print("schema check: 表结构与上游一致 ✅（索引按上述规则不比对）")
        return 0

    if missing or extra or col_issues:
        print("schema 漂移：module/etc/schema.sql 与上游 DATABASE.md 不一致！", file=sys.stderr)
        for k in missing:
            print(f"  [缺失] TABLE {k[1]}", file=sys.stderr)
        for k in extra:
            print(f"  [多余] TABLE {k[1]}", file=sys.stderr)
        for table, what, up, local in col_issues:
            print(f"  [漂移] TABLE {table} {what}（上游 {up} / 本地 {local}）", file=sys.stderr)
        print("同步方法：python3 tools/gen-schema.py --diff 查看详情，更新 schema.sql 后重试；"
              "确属有意超集请在 SUPERSET_COLUMNS 登记并写明代码依据。", file=sys.stderr)
        print("（SKIP_SCHEMA_CHECK=1 可跳过本检查——不建议）", file=sys.stderr)
        return 1
    print(f"schema check: 表结构与上游 DATABASE.md 一致 ✅（本地索引 {len(our_i)} 条不比对）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
