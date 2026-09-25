#!/usr/bin/env python3
"""构建时向 web/dist/index.html 注入 clipboard polyfill。

背景：Dashboard 所有复制按钮裸用 navigator.clipboard（需 HTTPS/localhost 的
secure context）。用户经 http://<LAN-IP>:20130 访问时该 API 不存在 → 全部
复制失效。此补丁在构建产物上注入降级实现（textarea + execCommand），
**不改上游任何源码文件**（ADR-0002），上游修复后：
    CLIPBOARD_PATCH=0 ./build.sh   # 跳过注入
或直接重构建即恢复干净产物。

注入块带 BEGIN/END 注释标记，可整段识别/删除。
"""
import re
import sys
from pathlib import Path

MARK_BEGIN = "<!-- 9router-go-magisk clipboard polyfill BEGIN -->"
MARK_END = "<!-- 9router-go-magisk clipboard polyfill END -->"

POLYFILL = """<script>
(function () {
  'use strict';
  if (typeof navigator === 'undefined') return;
  var nav = navigator;
  if (nav.clipboard && typeof nav.clipboard.writeText === 'function') return;
  function fallbackWrite(text) {
    return new Promise(function (resolve, reject) {
      try {
        var ta = document.createElement('textarea');
        ta.value = String(text == null ? '' : text);
        ta.setAttribute('readonly', '');
        ta.style.cssText = 'position:fixed;top:-999px;left:-999px;opacity:0';
        document.body.appendChild(ta);
        var sel = document.getSelection();
        var saved = sel && sel.rangeCount ? sel.getRangeAt(0) : null;
        ta.select();
        var ok = document.execCommand && document.execCommand('copy');
        document.body.removeChild(ta);
        if (saved && sel) { sel.removeAllRanges(); sel.addRange(saved); }
        if (ok) resolve();
        else reject(new Error('execCommand copy failed'));
      } catch (e) { reject(e); }
    });
  }
  Object.defineProperty(nav, 'clipboard', {
    configurable: true,
    value: {
      writeText: fallbackWrite,
      readText: function () {
        return Promise.reject(new Error('clipboard readText unavailable'));
      }
    }
  });
})();
</script>"""


def main() -> int:
    if len(sys.argv) < 2:
        print("usage: patch-clipboard.py <dist/index.html>", file=sys.stderr)
        return 2
    path = Path(sys.argv[1])
    if not path.is_file():
        print(f"patch-clipboard: {path} not found, skip", file=sys.stderr)
        return 0
    html = path.read_text(encoding="utf-8")
    if MARK_BEGIN in html:
        # 幂等：先移除旧注入块再重新注入（polyfill 更新时也走这条路）
        html = re.sub(
            re.escape(MARK_BEGIN) + r".*?" + re.escape(MARK_END) + r"\n?",
            "",
            html,
            flags=re.S,
        )
    m = re.search(r"<head[^>]*>", html)
    if not m:
        print("patch-clipboard: no <head> found, skip", file=sys.stderr)
        return 0
    injected = m.group(0) + "\n" + MARK_BEGIN + "\n" + POLYFILL + "\n" + MARK_END
    html = html[: m.start()] + injected + html[m.end():]
    path.write_text(html, encoding="utf-8")
    print("patch-clipboard: polyfill injected into", path)
    return 0


if __name__ == "__main__":
    sys.exit(main())
