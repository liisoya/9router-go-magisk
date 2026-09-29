/* 面板装配层冒烟测试 —— 在桩 DOM + 桩 ksu.exec 里跑**真实的**初始化与 refresh()。
 *
 * 为什么需要它（2026-09-27 真机事故，一路发到 r1 才被发现）：
 *   把 URL 常量收编进 upstream.js 时，漏改了 app.js 里一处裸引用 ——
 *   `renderPanel` 的 `state.modUrl = st.mod_url || DEFAULT_MOD_UPDATE_URL`。
 *   纯函数用例（parsers / bridge / upstream）全绿、回潮扫描也只查字符串字面量，
 *   于是这个 ReferenceError 发到设备上：面板显示 localStorage 里的旧快照
 *   （引擎 1.9.2 / 旧 PID），"服务地址"卡在"加载中"、资源占用全是 "-"，
 *   点重启弹 "DEFAULT_MOD_UPDATE_URL is not defined"。
 *
 * 判据是"**跑一遍真实的装配路径**"，不是再抄一遍名字：
 *   · 有异常 → unhandledRejection 捕获（refresh() 的异常正是这样冒出去的）
 *   · 渲染有没有跑到底 → 断言 renderPanel **靠后**那两块（资源占用 / 服务地址）
 *     和链尾的 renderAccelCur 都留下了痕迹。任一没渲染 = 中途抛错。
 *
 * 桩在 test/lib/app-harness.js（唯一一份）；脚本清单来自 index.html（唯一来源）。
 * 运行：node --test module/webroot/test/
 */
'use strict';
const { test } = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');
const { createHarness, scriptFiles, WEBROOT } = require('./lib/app-harness.js');

// 真机形态的 panel 单行输出（值不含空格 —— 见 ops.sh cmd_panel 的约定）
const PANEL_LINE = [
  'port=20130 bind=loopback module_version=v1.9.3-r1 versioncode=109030',
  'engine_version=1.9.4 engine_ver_src=runtime engine_ver_healed=0',
  'lan_ip=192.168.1.2 dns=up dns_pid=101 engine=up engine_pid=202',
  'watchdog=up watchdog_pid=303 factory_key=1 apikeys_total=1',
  'mem_total=5809472 mem_avail=1814288 engine_rss=24316 dns_rss=1128',
  'upstreams_b64=bmFtZXNlcnZlciAxMTkuMjkuMjkuMjk=',
  'mod_url= accel_sel='
].join(' ');

const h = createHarness({ execHandler: cmd => {
  // ops() 逐 token shq 引号包裹：`'…ops.sh' 'panel'`
  if (/ops\.sh' 'panel'|ops\.sh panel/.test(cmd) || /ops\.sh' 'status'/.test(cmd)) return PANEL_LINE;
  if (cmd.includes('github-accel')) return '';   // 未选加速节点 = 直连
  return '';
}});

test('面板初始化 + refresh() 必须跑到底（装配层没有裸引用 / 中途抛错）', async () => {
  const rec = h.recordRejections();
  const els = h.loadApp();
  await new Promise(r => setTimeout(r, 80));   // 等 refresh() 与它的 .then 链跑完
  rec.stop();

  const rejected = rec.list.map(r => (r && r.stack) || String(r)).join('\n');
  assert.strictEqual(rec.list.length, 0,
    `初始化链上抛出了异常（面板会显示旧快照、后面几块渲染不出来）：\n${rejected}`);

  // 状态行（renderPanel 前半段）
  assert.strictEqual(els.get('st-ver').textContent, '1.9.4', '引擎版本行没渲染');
  // 下面三处都在 renderPanel 的**后半段 / 链尾** —— 有值才说明整条路跑到底了
  assert.notStrictEqual(els.get('mem-eng').textContent, '',
    '资源占用没渲染（renderPanel 在它之前抛错了？）');
  assert.notStrictEqual(els.get('mem-dns').textContent, '', 'dnsfwd 内存没渲染');
  assert.ok(els.get('addr-list').innerHTML.includes('20130'),
    '服务地址没渲染（卡在"加载中"的形态）');
  assert.ok(els.get('accel-cur').textContent, '加速节点行没渲染（链尾 renderAccelCur 没跑到）');
});

test('每个按钮绑定都真的执行到了（id 写错会静默失联）', () => {
  const els = h.loadApp();
  const ids = [...els.keys()].filter(id => id.startsWith('btn-'));
  assert.ok(ids.length >= 20, `只绑定到 ${ids.length} 个按钮，装配层似乎没跑完`);
  const unbound = ids.filter(id => typeof els.get(id).onclick !== 'function');
  assert.deepStrictEqual(unbound, [],
    `这些按钮没绑上处理函数（id 写错了？）：${unbound.join(', ')}`);
});

// ── 清单双向闭合：index.html 声明的必须存在（漏推送），存在的必须被声明（死文件）──
test('index.html 声明的每个脚本都必须在 webroot 下存在', () => {
  const missing = scriptFiles().filter(f => !fs.existsSync(path.join(WEBROOT, f)));
  assert.deepStrictEqual(missing, [],
    `index.html 引用了不存在的脚本（真机表现：window.K* undefined、面板整页失效）：${missing.join(', ')}`);
});

test('webroot 下的每个 .js 都必须被 index.html 声明（否则是没人加载的死文件）', () => {
  const declared = new Set(scriptFiles());
  const onDisk = fs.readdirSync(WEBROOT).filter(f => f.endsWith('.js'));
  const orphan = onDisk.filter(f => !declared.has(f));
  assert.deepStrictEqual(orphan, [],
    `这些文件没有任何页面加载它（忘了在 index.html 里声明？）：${orphan.join(', ')}`);
});
