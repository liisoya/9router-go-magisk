/* DOM id 契约门禁（Step 4）
 *
 * 为什么需要这条：`test/lib/app-harness.js` 的桩 DOM 里 `getElementById` **永远返回元素**
 * —— 于是"JS 里拼错 id / HTML 里删了按钮"在离线**完全看不见**，真机上表现为初始化链
 * 同步抛错、整页死（与 Phase 27.6 的 `window.KUpstream undefined` 是同一类"整页失效"）。
 *
 * 三个方向缺一不可：
 *   ① **JS → HTML**：脚本请求的每个 id 都必须在 index.html 里存在（拼错、或按钮被删）
 *   ② **HTML → JS**：index.html 里每个 `btn-*` 都必须被脚本绑定（加了按钮却没人接 = 死按钮）
 *      —— 这是 app-wiring 那条"每个 btn-* 都绑上了处理函数"的**另一半**：桩会为拼错的 id
 *      凭空造元素并绑上，所以只有这条能发现"HTML 里没有那个按钮"。
 *   ③ **nav 契约**：`data-page` 必须指向存在的 id（点页签时 `getElementById(null)` → 整页死）
 *
 * 清单来源：脚本清单来自 index.html（唯一来源）；id 从两侧源码现取，不手写清单。
 * 运行：node --test module/webroot/test/
 */
'use strict';
const { test } = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');
const { scriptFiles, WEBROOT } = require('./lib/app-harness.js');

const HTML = fs.readFileSync(path.join(WEBROOT, 'index.html'), 'utf8');
const SCRIPTS = scriptFiles()
  .filter(f => f !== 'upstream.js')   // 纯地址契约，不碰 DOM
  .map(f => ({ f, src: fs.readFileSync(path.join(WEBROOT, f), 'utf8') }));

// ── index.html 声明的 id / data-page ──
function declaredIds() {
  const ids = new Set();
  for (const m of HTML.matchAll(/\bid="([^"]+)"/g)) ids.add(m[1]);
  return ids;
}
function declaredPages() {
  const pages = [];
  for (const m of HTML.matchAll(/\bdata-page="([^"]+)"/g)) pages.push(m[1]);
  return pages;
}
// ── 脚本请求的 id（只认字面量；`getElementById(b.dataset.page)` 这类动态调用不在此列，
//    它们由 ③ 的 nav 契约覆盖）──
function requestedIds() {
  const out = new Map();   // id → 出现位置
  const re = /(?:\$id|getElementById)\(\s*['"]([^'"]+)['"]\s*\)/g;
  for (const { f, src } of SCRIPTS) {
    for (const m of src.matchAll(re)) {
      if (!out.has(m[1])) out.set(m[1], new Set());
      out.get(m[1]).add(f);
    }
  }
  return out;
}
function boundButtonIds() {
  const ids = new Set();
  const re = /(?:\$id|getElementById)\(\s*['"](btn-[^'"]+)['"]\s*\)/g;
  for (const { src } of SCRIPTS) for (const m of src.matchAll(re)) ids.add(m[1]);
  return ids;
}

const DECLARED = declaredIds();
const REQUESTED = requestedIds();
const BOUND = boundButtonIds();

test('① 脚本请求的每个 id 都必须在 index.html 里存在（拼错 id = 真机整页死）', () => {
  const missing = [...REQUESTED.keys()].filter(id => !DECLARED.has(id)).sort();
  const where = missing.map(id => `${id}（${[...REQUESTED.get(id)].join(', ')}）`);
  assert.deepStrictEqual(missing, [],
    `这些 id 被脚本请求但 index.html 里没有：\n  ${where.join('\n  ')}`);
});

test('② index.html 里每个 btn-* 都必须被脚本绑定（死按钮）', () => {
  const htmlBtns = [...DECLARED].filter(id => id.startsWith('btn-'));
  const dead = htmlBtns.filter(id => !BOUND.has(id)).sort();
  assert.deepStrictEqual(dead, [],
    `这些按钮在 index.html 里存在但从没被绑定（点了没反应）：${dead.join(', ')}`);
});

test('③ nav 的每个 data-page 都必须指向存在的 id', () => {
  const pages = declaredPages();
  assert.ok(pages.length >= 4, `只解析出 ${pages.length} 个 data-page（nav 结构变了？本门禁需要同步）`);
  const broken = pages.filter(p => !DECLARED.has(p));
  assert.deepStrictEqual(broken, [],
    `这些 data-page 指向不存在的 id（点页签会 getElementById(null)）：${broken.join(', ')}`);
});

// 闸位自检：两个集合都不许是空转（否则上面的断言永远绿）
test('④ 门禁非空转：两侧解析结果都必须有量', () => {
  assert.ok(REQUESTED.size >= 20, `只从脚本里解析出 ${REQUESTED.size} 个 id，解析规则该更新了`);
  assert.ok(BOUND.size >= 15, `只解析出 ${BOUND.size} 个 btn-* 绑定，解析规则该更新了`);
  assert.ok(DECLARED.size >= 30, `只从 index.html 解析出 ${DECLARED.size} 个 id，解析规则该更新了`);
});
