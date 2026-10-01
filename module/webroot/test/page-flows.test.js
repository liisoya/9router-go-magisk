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
      // refresh()：opts.panel 让用例能注入"面板带回来的字段"（例：验证 mod_url 不被采纳）
      if (cmd.includes('panel')) return opts.panel || PANEL_OK;
      if (cmd.includes('-P -j 8')) return opts.probe || '';   // dnsfwd 探测输出（优选用）
      if (cmd.includes('rm -f')) return 'rm-ok';   // KB.remove 自报标记（2026-10-01 起写族自报）
      if (cmd.includes('am start')) return 'Starting: Intent { act=android.intent.action.VIEW }';
      return '';
    }
  });
  // 观察模式间隔可注入（CFG.observeMs）：必须在 createHarness 之后设（它重置 global.window）、
  // loadApp 之前设（vm 上下文取的是这份引用）
  if (opts.observeMs) global.window.CFG.observeMs = opts.observeMs;
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

// ── C4（2026-09-30 架构扫描）：加速选中的读取只有一处入口 ────────────────────────
// 为什么用源码级断言：要走到那条读取分支需要驱动"选中/测速"流程（依赖 curl 测速桩），
// 成本高且脆；而这里要锁的是 **locality**（同一知识只有一处）与**判别式语义**（ok:false
// 只能来自读失败，与"没选"严格区分）—— 两者都是可以机械判定的形状（同 .prev / escAttr 那两条）。
// I6 就是漏掉这条知识造成的：清掉选中后无参重渲染，把正确状态渲染成「未知（读取失败）」。
test('C4 加速选中：读取只经 readAccelSel（不得再内联 readFile，读失败必须可辨）', () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const src = fs.readFileSync(path.join(__dirname, '..', 'page-update.js'), 'utf8');
  const inline = (src.match(/KB\.readFile\(ACCEL_SEL\)/g) || []).length;
  assert.strictEqual(inline, 1,
    `内联读取加速选中出现了 ${inline} 处 —— 必须只经 readAccelSel()，否则"读失败≠没选"迟早被漏掉一处（I6）`);
  assert.ok(/async function readAccelSel\(\)/.test(src), '缺少 readAccelSel 单一入口');
  assert.ok(/r\.ok === false\) return \{ ok: false, sel: '' \}/.test(src),
    'readAccelSel 必须把读失败与"没选"分开（ok:false + 空串），否则调用方无法分辨故障与直连');
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

test('B1b 源码顺序：DNS_STORE.refreshPrev 必须先删旧 .prev 再备份（否则“回滚上一版”永远回到首版）', () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const src = fs.readFileSync(path.join(__dirname, '..', 'page-dns.js'), 'utf8');
  // 2026-10-01 架构评审 #7：这条次序知识从 optimize 的体内搬进了 DNS_STORE.refreshPrev ——
  // **断言跟着 seam 走**：守的仍是同一条不变量，只是它的家换了地方。
  const at = src.indexOf('const DNS_STORE');
  assert.ok(at > 0, '找不到 DNS_STORE（次序的唯一所有者）');
  const store = src.slice(at, src.indexOf('};', at));
  const iRemove = store.indexOf('KB.remove(DNS_PREV)');
  const iBackup = store.indexOf('KB.backupOnce(UPSTREAMS, DNS_PREV)');
  assert.ok(iRemove > 0, '没有找到「删旧 .prev」这一步（backupOnce 对已存在的目标会跳过 cp）');
  assert.ok(iBackup > iRemove, `顺序反了：删(${iRemove}) 必须在备份(${iBackup}) 之前`);
  // 而且这条次序真的在优选路径上 —— 模块写对了却没人用等于没有
  assert.ok(src.includes('await DNS_STORE.refreshPrev()'), 'optimize 必须走 DNS_STORE.refreshPrev');
});

// ── 优选排序契约（2026-10-01）：以本机实测为准，而不是"推荐顺序" ──────────
// 问过的问题是"按推荐做还是按实测做"。答案落到可执行事实：
// 内置清单里「腾讯」排在「阿里」之前（DNS_CANDIDATES 的排列），这里让阿里快得多 ——
// 若哪天优选改成"按清单顺序"，阿里会被压到腾讯后面，这两条会红。
const PROBE_FAST_ALI = [
  '  119.29.29.29                       v4  www.baidu.com              300ms  2/2  1.2.3.4',
  '  223.6.6.6                          v4  www.baidu.com                8ms  2/2  1.2.3.4'
].join('\n');
// 候选清单与最终配置都经 KB.writeFile，取最后一条 = 真正落盘的那份。
// 内容是 base64 过 shell 的（printf '%s' '<b64>' | base64 -d），断言顺序必须先解出来 ——
// 否则比的是编码串，顺序断言等于没断言。
const lastWrite = cmds => {
  const c = cmds.filter(c => c.includes('write-ok')).pop() || '';
  const m = c.match(/printf '%s' '([A-Za-z0-9+/=]+)'/);
  return m ? Buffer.from(m[1], 'base64').toString('utf8') : c;
};

test('优选排序：按本机实测评分降序（内置清单的排列顺序不参与排序）', async () => {
  const { els, cmds } = boot({ probe: PROBE_FAST_ALI, backup: true });
  await els.get('btn-opt').onclick();
  const w = lastWrite(cmds);
  const iAli = w.indexOf('223.6.6.6'), iTx = w.indexOf('119.29.29.29');
  assert.ok(iAli >= 0 && iTx >= 0, `写入内容里缺上游：${w}`);
  assert.ok(iAli < iTx,
    `阿里 8ms 必须排在腾讯 300ms 之前 —— 现在按清单顺序排了（推荐值压过了实测）：${w}`);
});

test('优选排序：手写的自定义项保留在最前（契约②的例外，即使它没过门槛）', async () => {
  const { els, cmds } = boot({ probe: PROBE_FAST_ALI, backup: true });
  els.get('upstreams').value = 'nameserver 1.1.1.1';   // 不在探针输出里 = 本次没通过门槛
  await els.get('btn-opt').onclick();
  const w = lastWrite(cmds);
  const iCustom = w.indexOf('1.1.1.1'), iAli = w.indexOf('223.6.6.6');
  assert.ok(iCustom >= 0, `自定义项被丢了（用户手写的不能被一次优选淘汰）：${w}`);
  assert.ok(iCustom < iAli, `手写项必须留在最前，实际顺序：${w}`);
});

// ── 更新源必须固定（2026-10-01）：不接受用户自定义 ──────────────────────
// 为什么锁死：更新源决定"从哪儿下载那份会覆盖整个模块目录的 zip"。留一个输入框，
// 就等于把"装谁的代码"变成可配置项 —— 而它出事时的形状是"模块更新异常"，无从归因。
test('更新源：面板不提供任何入口，也不再展示（改不了的地址摆出来没有信息量）', () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const html = fs.readFileSync(path.join(__dirname, '..', 'index.html'), 'utf8');
  assert.ok(!/id="in-modurl"/.test(html), '仍有更新源输入框（用户又能改了）');
  assert.ok(!/id="btn-mod-seturl"/.test(html), '仍有「保存」按钮');
  assert.ok(!/id="mod-url"/.test(html),
    '仍在页面展示更新源（2026-10-01 用户要求去掉：固定与否由 upstream.js + 测试保证，不靠给人看）');
  const src = fs.readFileSync(path.join(__dirname, '..', 'page-update.js'), 'utf8');
  assert.ok(!/module-update-url/.test(src), '仍在读写 module-update-url（可写的更新源 = 可指向任意 zip）');
  assert.ok(!/setModUrl|setSnapshotModUrl/.test(src), '仍在提供「改更新源」的入口');
});

test('更新页的项目地址：由 KU.PROJECT_URL 填入（且是新窗口打开的安全外链）', () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const html = fs.readFileSync(path.join(__dirname, '..', 'index.html'), 'utf8');
  // 结构性安全：target="_blank" 不带 rel=noopener，目标页能经 window.opener 反向操作本页
  assert.ok(!/target="_blank"(?![^>]*\brel=)/.test(html),
    '有 target="_blank" 却没带 rel（缺 noopener 会把 opener 交出去）');
  // 行为：地址由 KU 现取，首屏就填上（不等第一份快照）
  const { els } = boot({});
  const a = els.get('mod-project');
  assert.strictEqual(a.href, 'https://github.com/liisoya/9router-go-magisk',
    '项目地址没被填上（地址必须由 KU.PROJECT_URL 现取，不许写死在 HTML 里）');
  assert.strictEqual(a.textContent, 'liisoya/9router-go-magisk', '一行里塞完整 URL 在手机上必换行');
});

test('观察模式：点开立即刷 + 周期刷 + 状态亮起；再点关闭即停；不记忆', async () => {
  const { els, cmds } = boot({ observeMs: 40 });   // 注入短间隔：生产默认 5000（app-boot.js）
  const cntPanel = () => cmds.filter(c => c.includes('panel')).length;
  // 防挂死：定时器只有"再点一次"才清 —— 断言失败抛错时若不清，setInterval 会挂住整个
  // node --test 进程（真踩过：变异验证时用例红在半路、进程永不退出，看起来像卡死）。
  let opened = false;
  try {
    await els.get('btn-observe').onclick();
    opened = true;
    await tick();
    assert.ok(cntPanel() >= 1, '点开应立即刷新一次，不等第一个周期');
    await new Promise(r => setTimeout(r, 120));   // 跨 2 个以上 40ms 周期
    const during = cntPanel();
    assert.ok(during >= 3, `周期刷新没跑（panel 共 ${during} 次）`);
    assert.ok(els.get('btn-observe').classList._calls.some(c => c[0] === 'add' && c[1] === 'on'),
      '开着却没把开关标 on（开关本身即状态，没有状态牌）');
    assert.strictEqual(els.get('btn-observe').textContent, '观察中', '开着却没把胶囊文案切成「观察中」');
    await els.get('btn-observe').onclick();   // 再点 = 关闭
    opened = false;
    await tick();
    const stopped = cntPanel();
    await new Promise(r => setTimeout(r, 120));
    assert.strictEqual(cntPanel(), stopped, '关闭后仍在刷新（定时器没被清掉）');
    assert.ok(els.get('btn-observe').classList._calls.some(c => c[0] === 'remove' && c[1] === 'on'),
      '关闭后没摘 on 状态');
    assert.strictEqual(els.get('btn-observe').textContent, '观察', '关闭后文案没切回「观察」');
  } finally {
    if (opened) await els.get('btn-observe').onclick();
  }
});

test('项目地址：点击走 root shell 的 am start（WebView 不一定处理 _blank，真机点不动）', async () => {
  const { els, cmds } = boot({});
  await els.get('mod-project').onclick({ preventDefault() {} });
  const c = cmds.find(c => c.includes('am start -a android.intent.action.VIEW'));
  assert.ok(c, `没发打开浏览器的命令：${cmds.join(' | ')}`);
  assert.ok(c.includes('https://github.com/liisoya/9router-go-magisk'), `URL 不对：${c}`);
  assert.ok(els.get('toast').textContent.includes('已在浏览器打开'),
    `应给成功回执，实际：「${els.get('toast').textContent}」`);
});

test('更新源：面板快照带回任何 mod_url 都不许改变实际请求的源', async () => {
  const evil = PANEL_OK.replace('mod_url=', 'mod_url=https://evil.example/x.json');
  const { els, cmds } = boot({ panel: evil });
  await els.get('btn-mod-check').onclick();
  await tick(); await tick();
  const reqs = cmds.filter(c => c.includes('curl'));
  assert.ok(reqs.length, '检查更新没有发出任何请求（断言没落在真实行为上）');
  assert.ok(reqs.some(c => c.includes('liisoya/9router-go-magisk')),
    `没有请求内置更新源，实际命令：${reqs.join(' | ')}`);
  assert.ok(!reqs.some(c => c.includes('evil.example')),
    '用了面板带回 / 残留文件里的 mod_url —— 更新源必须固定，不得被任何外部值顶掉');
});
