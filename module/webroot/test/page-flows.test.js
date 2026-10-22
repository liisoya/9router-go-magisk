/* 页面流程的「诚实度」回归 —— 上一步的结果必须是下一步的前提（2026-09-29 架构走查 A5/A6）
 *
 * 为什么单独有它：这两条 bug 是同一件事的两面。
 *   A5 page-overview.savePort     : 丢弃 restart-engine 的返回值，却无条件 toast「✅ 已重启」
 *                                   （新端口被占用 / 引擎起不来时照样报成功）
 *   A6 page-dns.saveUpstreams     : 丢弃备份结果，仍改写配置
 *                                   （备份失败 → 用户配置被覆盖且没有 .initial 可回滚）
 * 2026-10-15 C4 起 A6 的保护搬进 seam：写入只经 ops.sh write-upstreams，备份失败由
 * seam 拒绝（write-failed-no-backup）——本文件相应断言"seam 拒写时 UI 不谎报成功"。
 * 它们各自的邻居（restartAll / optimize）**一直是在判的** —— 同一份安全知识只对了一半。
 * 所以这里断言的是行为而非实现：成败文案必须跟着真实结果走；seam 拒写时**不得报成功**。
 * 走真实入口（btn-* 的 onclick），命令经 bridge 的桩回答 —— 与真机同一套代码路径。
 */
'use strict';
const test = require('node:test');
const assert = require('node:assert');
const { createHarness } = require('./lib/app-harness.js');

// refresh() 会问 panel：给一份最小可解析的 payload，避免用空串去撞解析层
// 候选池单一来源文件的最小形状（page-dns 芯片/优选读取；与 module/etc/dns-candidates 同格式）
const CAND_FILE = [
  '# 9router-go · DNS 优选候选池（单一事实源）',
  'cat=明文 label=腾讯     nameserver 119.29.29.29',
  'cat=明文 label=阿里     nameserver 223.6.6.6'
].join('\n');

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
      if (cmd.includes('write-ok')) return opts.write === false ? '' : 'write-ok'; // KB.writeFile
      if (cmd.includes('restart-engine')) return opts.restart || 'engine=up';      // KB.ops
      if (cmd.includes('start-user')) return opts.start || 'engine=up';            // KB.ops
      if (cmd.includes('stop-user')) return opts.stop || 'stopped';                // KB.ops
      if (cmd.includes('reload-dns')) return opts.reload === undefined ? 'reloaded' : opts.reload;
      // C4：上游清单的写/恢复只经 seam（ops.sh write-upstreams / restore-upstreams）。
      // 备份→原子写→热重载都在 seam 内，页面只认判据词——这里按用例注入各失败形态。
      if (cmd.includes('write-upstreams')) {
        if (opts.backup === false) return 'write-failed-no-backup';
        if (opts.write === false) return 'write-failed-write';
        if (opts.reload === false) return 'write-ok-reload-failed';
        return 'write-ok-reloaded';
      }
      if (cmd.includes('echo write-ok')) return 'write-ok';   // KB.writeFile（候选临时文件等）
      if (cmd.includes('restore-upstreams')) return opts.restore === false ? 'restore-none' : 'restore-ok-reloaded';
      // refresh()：opts.panel 让用例能注入"面板带回来的字段"（例：验证 mod_url 不被采纳）
      if (cmd.includes('panel')) return opts.panel || PANEL_OK;
      if (cmd.includes('etc/dns-candidates')) {
        // 候选池单一来源（cat=/label= 元数据行，readFile 走 __READ_OK__ 哨兵）
        return (opts.candidates || CAND_FILE) + '\n__READ_OK__';
      }
      if (cmd.includes('optimize-dns')) return 'reoptimized';   // 优选 seam 判据词
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

// C4 起，"写配置"= 经 seam 下发 ops.sh write-upstreams（备份/原子写/热重载都在里面）
const wroteConfig = cmds => cmds.some(c => c.includes('write-upstreams'));
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

test('A5 savePort：引擎起来了才报成功（对照，防"永远报失败"）', async () => {
  const { els } = boot({ restart: 'engine=up' });
  els.get('in-port').value = '20131';
  await els.get('btn-port').onclick();
  assert.ok(els.get('toast').textContent.includes('✅'),
    `应报成功，实际文案：「${els.get('toast').textContent}」`);
});

test('A6 saveUpstreams：回滚点建不起来时 seam 拒写、界面不谎报成功', async () => {
  const { els, cmds } = boot({ backup: false });
  els.get('upstreams').value = '223.5.5.5\n119.29.29.29';
  await els.get('btn-save').onclick();
  // C4：写入只经 seam；"备份失败就不改写"的保证从"JS 预检"升级为"seam fail-closed"
  // （ops.sh write-upstreams 判 write-failed-no-backup 并拒绝落盘）。
  assert.ok(cmds.some(c => c.includes('write-upstreams')), '写入没走 seam（ops.sh write-upstreams）');
  assert.ok(!cmds.some(c => c.includes('.prev') && c.includes('cp ')),
    '页面又在自发备份——纪律应只在 seam（两份实现必然漂）');
  const msg = els.get('toast').textContent;
  assert.ok(msg.includes('回滚点'), `应提示回滚点不可用，实际文案：「${msg}」`);
  assert.ok(!msg.includes('✅'), `拒写不得报成功，实际文案：「${msg}」`);
});

test('A6 saveUpstreams：备份可用时照常改写（对照，防"永远不写"）', async () => {
  const { els, cmds } = boot({ backup: true });
  els.get('upstreams').value = '223.5.5.5';
  await els.get('btn-save').onclick();
  assert.ok(wroteConfig(cmds), '回滚点可用却没写配置（保存功能被误伤）');
});

test('A6 对照：seam 报写入失败时文案也必须说失败', async () => {
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

test('A5b startSvc：起来了才报成功（对照，防"永远报失败"）', async () => {
  const { els } = boot({ start: 'engine=up' });
  await els.get('btn-start-svc').onclick();
  assert.ok(els.get('toast').textContent.includes('✅'),
    `应报成功，实际文案：「${els.get('toast').textContent}」`);
});

test('A5c 热重载失败必须报出来（seam verdict：write-ok-reload-failed；"四个调用点全传 silent"的旧病由此绝根）', async () => {
  const { els } = boot({ backup: true, reload: false });   // seam 判"写了但热重载没生效"
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

test('B1b 备份次序的唯一所有者已随写入搬进 seam：life_upstreams_backup_prev 先删 .prev 再备，页面不得自发备份', () => {
  const fs = require('node:fs');
  const path = require('node:path');
  // 2026-10-01 架构评审 #7：次序知识从 optimize 体内搬进 JS 的 DNS_STORE.refreshPrev；
  // 2026-10-15 C4：随"写入"一起搬进 shell（ops.sh write-upstreams → lifecycle 的
  // life_upstreams_backup_prev）。守的还是同一条不变量：先删旧 .prev 再备份，
  // 否则"回滚上一版"永远回到首版。断言跟着 seam 走，只是家又换了一次。
  const life = fs.readFileSync(path.join(__dirname, '..', '..', 'lib', 'lifecycle.sh'), 'utf8');
  const at = life.indexOf('life_upstreams_backup_prev() {');
  assert.ok(at > 0, '找不到 life_upstreams_backup_prev（备份次序的唯一实现）');
  const fn = life.slice(at, life.indexOf('life_upstreams_backup_initial'));
  const iRemove = fn.indexOf('rm -f "$LIFE_UPSTREAMS.prev"');
  const iBackup = fn.indexOf('cp "$LIFE_UPSTREAMS" "$LIFE_UPSTREAMS.prev"');
  assert.ok(iRemove > 0, '没有找到「删旧 .prev」这一步（跳过式备份会让回滚永远回到首版）');
  assert.ok(iBackup > iRemove, `顺序反了：删(${iRemove}) 必须在备份(${iBackup}) 之前`);
  // 而且写配置真的走 seam —— 模块写对了却没人用等于没有
  const page = fs.readFileSync(path.join(__dirname, '..', 'page-dns.js'), 'utf8');
  assert.ok(page.includes('KB.writeUpstreams('), 'page-dns 必须经 KB.writeUpstreams 走 seam');
  assert.ok(!/KB\.writeFile\(UPSTREAMS/.test(page), 'page-dns 又在直写上游文件（绕过 ops.sh，C4 要消灭的正是这个）');
  assert.ok(!/KB\.backupOnce\(UPSTREAMS/.test(page), 'page-dns 又在自发备份（纪律应只在 seam）');
});

// ── 优选契约（2026-10-22 完整优选收编后）：排序与写盘都在 shell 侧 ──────────
// 旧契约（JS 编排探测→Top5→write-upstreams）随 ADR-0010 完整优选落地而退役：
// 评分公式唯一实现在 dnsfwd.c probe_score（TSV score 列），写盘/排序唯一实现在
// ops.sh optimize-dns（lifecycle.life_dns_reoptimize）。页面职责 = 候选池展示 +
// rows-gate 预检 + 发动作命令 + 翻译判据词。这里钉住页面侧行为：
//   ① 探测输出有 ≥50% 可用行 → 发 optimize-dns（rows-gate 不拦健康路径）
//   ② 全部 <50% → 不发动作、不改写（门禁仍由页面本阶段求值）
//   ③ 写盘内容/顺序由 tools/test-dns-optimize.sh 在 shell 侧钉住（单一所有者）
const PROBE_TSV_FAST_ALI = [
  'upstream\tfamily\tdomain\trtt_ms\tok/total\tn_ans\tanswers\tfakeip\ttcp_ms\ttcp_ok/total\ttcp_ip\tscore',
  '119.29.29.29\tv4\twww.baidu.com\t300\t2/2\t1\t1.2.3.4\tno\t-1\t0/0\t-\t40.0',
  '223.6.6.6\tv4\twww.baidu.com\t8\t2/2\t1\t1.2.3.4\tno\t-1\t0/0\t-\t92.0'
].join('\n');

test('优选：有可用上游 → 动作命令下发（optimize-dns seam，2026-10-22 收编）', async () => {
  const { els, cmds } = boot({ probe: PROBE_TSV_FAST_ALI, backup: true });
  await els.get('btn-opt').onclick();
  assert.ok(cmds.some(c => c.includes('optimize-dns')), '优选动作没经 ops.sh optimize-dns');
  assert.ok(els.get('toast').textContent.includes('优选已应用'),
    `判据词没翻译成用户提示：「${els.get('toast').textContent}」`);
});

test('优选：全部 <50% → 不发动作、保持原配置（rows-gate 仍由页面求值）', async () => {
  const weak = PROBE_TSV_FAST_ALI.split('\n')[0] +
    '\n119.29.29.29\tv4\twww.baidu.com\t300\t0/2\t1\t-\tno\t-1\t0/0\t-\t0.0';
  const { els, cmds } = boot({ probe: weak, backup: true });
  await els.get('btn-opt').onclick();
  assert.ok(!cmds.some(c => c.includes('optimize-dns')), '无可用上游却发了写盘动作');
  assert.ok(els.get('opt-table').innerHTML.includes('没有可用率'),
    '门禁拒绝原因没展示给用户');
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
