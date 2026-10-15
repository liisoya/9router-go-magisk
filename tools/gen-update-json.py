#!/usr/bin/env python3
"""tools/gen-update-json.py — 模块更新清单（update.json）的生成与一致性闸门。

为什么有它（2026-10-15 架构评审 C1）：update.json 是设备端应用内更新的清单
（module/webroot/page-update.js 的 modCheck 按 versionCode 比较：j.versionCode >
当前 才提供更新）。它曾与 module.prop 漂移——清单停在 v1.9.7-r2/109071，而模块
已是 v1.9.13-r1/109130：任何 versionCode < 109130 的设备（含升级路径上的全部
前代）都会看到"已是最新"，这次发布对存量设备静默不可见；且发布流程之外没有任何
东西断言二者一致（docs/INVESTIGATION-20261015:91 记过同类漂移）。原先只有
release.yml 里一段内联 python 做这件事：本地无从复用、漂移无从发现。

本脚本把"清单长什么样"收敛为单一实现：
  --write  从 module.prop 重新生成 update.json（发布准备；CI 调用同一命令）
  --check  断言 update.json 与 module.prop 一致（离线闸门 UPDATEJSON，漂移即红）

URL 语义与 release.yml 既有行为逐字一致（CI 调用后必须零 diff）：
  version     = module.prop 的 version（形如 v1.9.13-r1）
  versionCode = module.prop 的 versionCode
  zipUrl      = releases/download/<version>/9router-go-<version 去掉 v>-magisk.zip
  changelog   = releases/download/<version>/changelog.md
    （KSU 系管理器把 changelog 当 URL 下载，必须是文件不是文本；用 tag 直链而非
     latest/download——避开重定向，部分网络栈/代理对 302 502）

用法（仓库根或任意目录均可，路径相对脚本位置解析）：
  python3 tools/gen-update-json.py --write [--repo owner/name]
  python3 tools/gen-update-json.py --check [--repo owner/name]
"""
import argparse
import json
import pathlib
import sys

REPO_ROOT = pathlib.Path(__file__).resolve().parent.parent
DEFAULT_REPO = "liisoya/9router-go-magisk"


def read_prop(prop_path: pathlib.Path) -> dict:
    """从 module.prop 读 version / versionCode（唯一版本源）。"""
    fields = {}
    for line in prop_path.read_text(encoding="utf-8").splitlines():
        if "=" not in line:
            continue
        key, _, value = line.partition("=")
        key = key.strip()
        if key in ("version", "versionCode"):
            fields[key] = value.strip()
    missing = [k for k in ("version", "versionCode") if k not in fields]
    if missing:
        sys.exit(f"gen-update-json: {prop_path} 缺少字段：{', '.join(missing)}")
    if not fields["versionCode"].isdigit():
        sys.exit(f"gen-update-json: versionCode 不是数字：{fields['versionCode']!r}")
    return fields


def expected_manifest(prop: dict, repo: str) -> dict:
    tag = prop["version"]  # module.prop 的 version 已带 v 前缀（v1.9.13-r1）
    rel = tag[1:] if tag.startswith("v") else tag
    return {
        "version": tag,
        "versionCode": int(prop["versionCode"]),
        "zipUrl": f"https://github.com/{repo}/releases/download/{tag}/9router-go-{rel}-magisk.zip",
        "changelog": f"https://github.com/{repo}/releases/download/{tag}/changelog.md",
    }


def cmd_write(prop_path: pathlib.Path, out_path: pathlib.Path, repo: str) -> int:
    manifest = expected_manifest(read_prop(prop_path), repo)
    out_path.write_text(json.dumps(manifest, indent=2, ensure_ascii=False) + "\n", encoding="utf-8")
    print(f"gen-update-json: 已生成 {out_path}（{manifest['version']} / {manifest['versionCode']}）")
    return 0


def cmd_check(prop_path: pathlib.Path, out_path: pathlib.Path, repo: str) -> int:
    expected = expected_manifest(read_prop(prop_path), repo)
    if not out_path.exists():
        print(f"gen-update-json: {out_path} 不存在——发布准备时运行 --write 生成", file=sys.stderr)
        return 1
    try:
        actual = json.loads(out_path.read_text(encoding="utf-8"))
    except (json.JSONDecodeError, UnicodeDecodeError) as exc:
        print(f"gen-update-json: {out_path} 不是合法 JSON：{exc}", file=sys.stderr)
        return 1
    if not isinstance(actual, dict):
        print(f"gen-update-json: {out_path} 顶层不是对象", file=sys.stderr)
        return 1
    drift = []
    for key, want in expected.items():
        got = actual.get(key)
        if got != want:
            drift.append(f"  {key}: update.json={got!r} ≠ module.prop 派生={want!r}")
    if drift:
        print(
            "gen-update-json: update.json 与 module.prop 漂移（低于 versionCode "
            f"{expected['versionCode']} 的设备将永远看不到本次更新）：\n" + "\n".join(drift),
            file=sys.stderr,
        )
        return 1
    print(f"gen-update-json: update.json 与 module.prop 一致（{expected['version']} / {expected['versionCode']}）")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description="生成/校验模块更新清单 update.json")
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument("--write", action="store_true", help="从 module.prop 重新生成 update.json")
    mode.add_argument("--check", action="store_true", help="断言 update.json 与 module.prop 一致（闸门用）")
    parser.add_argument("--prop", type=pathlib.Path, default=REPO_ROOT / "module" / "module.prop")
    parser.add_argument("--out", type=pathlib.Path, default=REPO_ROOT / "update.json")
    parser.add_argument("--repo", default=DEFAULT_REPO, help="GitHub owner/name（fork 用 github.repository）")
    args = parser.parse_args()
    if not args.prop.exists():
        sys.exit(f"gen-update-json: 找不到 {args.prop}")
    # 默认 --write（发布准备的人手滑忘参数时不应静默无操作）
    if args.check:
        return cmd_check(args.prop, args.out, args.repo)
    return cmd_write(args.prop, args.out, args.repo)


if __name__ == "__main__":
    sys.exit(main())
