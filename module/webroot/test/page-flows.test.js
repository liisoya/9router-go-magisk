/* 页面流程的「诚实度」回归 —— 上一步的结果必须是下一步的前提（2026-09-29 架构走查 A5/A6）
 *
 * 为什么单独有它：这两条 bug 是同一件事的两面。
 *   A5 page-overview.savePort     : 丢弃 restart-engine 的返回值，却无条件 toast「✅ 已重启」
 *                                   （新端口被占用 / 引擎起不来时照样报成功）
 *   A6 page-dns.saveUpstreams     : 丢弃 backupOnce 的返回值，仍改写配置
 *                                   （备份失败 → 用户配置被覆盖且没有 .initial 可回滚）
 * 它们各自的邻居（restartAll / optimize）**一直是在判的** —— 同一份安全知识只对了一半。
 * 所以这里断言的是行为而非实现：成败文案必须跟着真实结果走；回滚点不可用时**不得下发写入命令**。
 * 走真实入口（btn-* 的 onclick），命令经 bridge 的桩回答 —— 与真机同一套代码路径。
 */
'use strict';
const test = require('node:test');
const assert = require('node:assert');
const { createHarness } = require('./lib/app-harness.js');

// refresh() 会问 panel：给一份最小可解析的 payload，避免用空串去撞解析层
const PANEL_OK = 'port=20130 bind=loopback module_version=v1.0.0-r1 versioncode=100 ' +
  'engine_version=1.0.0 engine_ver_src=runtime engine_ver_healed=0 lan_ip=127.0.0.1 ' +
  'dns=up dns_pid=2 engine=up engine_pid=1 watchdog=up watchdog_pid=3 ' +
  'factory_key=0 apikeys_total=1 mem_total=1 mem_avail=1 engine_rss=1 dns_rss=1 ' +
  'upstreams_b64= mod_url= accel_sel=';

function boot(opts) {
  const cmds = [];
  const h = createHarness({
    execHandler: cmd => {
      cmds.push(cmd);
      if (cmd.includes('no-src')) return opts.backup === false ? 'fail' : 'ok';   // KB.backupOnce
      if (cmd.includes('write-ok')) return opts.write === false ? '' : 'write-ok'; // KB.writeFile
      if (cmd.includes('restart-engine')) return opts.restart || 'engine=up';      // KB.ops
      if (cmd.includes('start-user')) return opts.start || 'engine=up';            // KB.ops
      if (cmd.includes('stop-user')) return opts.stop || 'stopped';                // KB.ops
      if (cmd.includes('reload-dns')) return opts.reload === undefined ? 'reloaded' : opts.reload;
      if (cmd.includes('panel')) return PANEL_OK;                                  // refresh()
      return '';
    }
  });
  return { els: h.loadApp(), cmds };
}

const wroteConfig = cmds => cmds.some(c => c.includes('write-ok'));
// 让一串微任务/定时器跑完：`renderAccelCur()` 是 async 且调用点不 await 它，
// 断言若不等一拍就会跑在它前面 —— 那样连"渲染成了故障文案"都抓不到（本轮真的踩了一次假绿）。
const tick = () => new Promise(r => setTimeout(r, 0));

test('A5 savePort：引擎没起来必须报失败，不得谎报成功', async () => {
  const { els } = boot({ restart: 'engine=down' });
  els.get('in-port').value = '20131';
  await els.get('btn-port').onclick();
  const msg = els.get('toast').textContent;
  assert.ok(msg.includes('❌'), `应报失败，实际文案：「${msg}」`);
  assert.ok(!msg.includes('已重启'), `不得谎报成功，实际文案：「${msg}」`);
});

test('A5 savePort：引擎起来了才报成功（对照，防“永远报失败”）', async () => {
  const { els } = boot({ restart: 'engine=up' });
  els.get('in-port').value = '20131';
  await els.get('btn-port').onclick();
  assert.ok(els.get('toast').textContent.includes('✅'),
    `应报成功，实际文案：「${els.get('toast').textContent}」`);
});

test('A6 saveUpstreams：回滚点建不起来时不得改写配置', async () => {
  const { els, cmds } = boot({ backup: false });
  els.get('upstreams').value = '223.5.5.5\n119.29.29.29';
  await els.get('btn-save').onclick();
  assert.ok(!wroteConfig(cmds), '备份失败却仍下发了写入配置的命令（用户将失去唯一回滚点）');
  assert.ok(els.get('toast').textContent.includes('回滚点'),
    `应提示回滚点不可用，实际文案：「${els.get('toast').textContent}」`);
});

test('A6 saveUpstreams：备份可用时照常改写（对照，防“永远不写”）', async () => {
  const { els, cmds } = boot({ backup: true });
  els.get('upstreams').value = '223.5.5.5';
  await els.get('btn-save').onclick();
  assert.ok(wroteConfig(cmds), '回滚点可用却没写配置（保存功能被误伤）');
});

test('A6 对照：写入失败时文案也必须说失败', async () => {
  const { els } = boot({ backup: true, write: false });
  els.get('upstreams').value = '223.5.5.5';
  await els.get('btn-save').onclick();
  assert.ok(els.get('toast').textContent.includes('❌'),
    `写入失败应报失败，实际文案：「${els.get('toast').textContent}」`);
});

// ── B5：属性语境必须用 escAttr（一个名字两种语境是出错的根源）────────────────────
// 源码级扫描：属性值里的插值只允许 escAttr/safe 包装，出现 `${esc(` 即红。
// 为什么用扫描而不是用例：要走通这些渲染路径需要 stub prompt/state，成本高且易脆；
// 而"属性里不许出现 esc("是个可以机械判定的形状（与上游地址回潮扫描同一手法）。
test('B5 属性语境的插值不得再用 esc（必须 escAttr）', () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const dir = path.join(__dirname, '..');
  const bad = [];
  for (const f of fs.readdirSync(dir).filter(n => n.endsWith('.js'))) {
    const src = fs.readFileSync(path.join(dir, f), 'utf8');
    src.split('\n').forEach((line, i) => {
      if (/="${esc\(/.test(line)) bad.push(`${f}:${i + 1}`);
    });
  }
  assert.deepStrictEqual(bad, [],
    `这些地方把 esc 用在了属性值里（应改 escAttr，否则值里的 " 会越出属性）：${bad.join(', ')}`);
});

// ── 诊断补修（2026-09-29 第二轮）：同族"上一步结果没成为下一步前提" ────────────────
test('A5b startSvc：引擎没起来必须报失败，不得谎报成功', async () => {
  const { els } = boot({ start: 'engine=down' });
  await els.get('btn-start-svc').onclick();
  const msg = els.get('toast').textContent;
  assert.ok(msg.includes('❌'), `应报失败，实际文案：「${msg}」`);
  assert.ok(!msg.includes('已启动'), `不得谎报成功，实际文案：「${msg}」`);
});

test('A5b startSvc：起来了才报成功（对照，防“永远报失败”）', async () => {
  const { els } = boot({ start: 'engine=up' });
  await els.get('btn-start-svc').onclick();
  assert.ok(els.get('toast').textContent.includes('✅'),
    `应报成功，实际文案：「${els.get('toast').textContent}」`);
});

test('A5c reloadDns：热重载失败必须报出来（四个调用点全传 silent，过去是死代码）', async () => {
  const { els } = boot({ backup: true, reload: '' });   // '' !== 'reloaded'
  els.get('upstreams').value = '223.5.5.5';
  await els.get('btn-save').onclick();
  assert.ok(els.get('toast').textContent.includes('热重载'),
    `配置已写盘却不生效，界面必须说清，实际文案：「${els.get('toast').textContent}」`);
});

test('B5b 清除选中：必须显示「直连 GitHub」，不得渲染成「未知（读取失败）」', async () => {
  // 这条曾经是**源码级扫描**，理由写在 FIXPLAN 34.1 里：「桩跑不通 KB.readFile，
  // 断言永远为真」。2026-09-29 复核证明那个理由是**误判** —— 桩确实会把
  // `cat '<path>' 2>/dev/null && echo __READ_OK__` 发出去（实测命令日志可见），
  // 真正导致假绿的是**异步渲染没等到拍**（renderAccelCur() 的调用点不 await 它）。
  // 现在改成真 DOM 断言，并已被判定性实验证明能红：把 clearAccel 变异成无参版后，
  // 这里渲染出的是「未知（读取失败）」（"文件不存在"与"读失败"在 readFile 里是同一个
  // ok:false）→ 断言精确报出该文案。
  const { els } = boot({ backup: true });
  await els.get('btn-accel-clear').onclick();
  await tick(); await tick(); await tick();   // 等 async 渲染落地（否则断言跑在它前面）
  assert.equal(els.get('accel-cur').textContent, '直连 GitHub',
    '清除选中后应显示「直连 GitHub」；无参去读刚被删掉的文件会渲染成「未知（读取失败）」');
});

test('B1b 源码顺序：optimize 必须先删旧 .prev 再备份（否则“回滚上一版”永远回到首版）', () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const src = fs.readFileSync(path.join(__dirname, '..', 'page-dns.js'), 'utf8');
  const iRemove = src.indexOf("KB.remove(UPSTREAMS + '.prev')");
  const iBackup = src.indexOf("backupOnce(UPSTREAMS, UPSTREAMS + '.prev')");
  assert.ok(iRemove > 0, '没有找到「删旧 .prev」这一步（backupOnce 对已存在的目标会跳过 cp）');
  assert.ok(iBackup > iRemove, `顺序反了：删(${iRemove}) 必须在备份(${iBackup}) 之前`);
});
