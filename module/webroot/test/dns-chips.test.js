/* DNS 候选芯片的自动生效流程（2026-10-02 用户反馈两连）：
 *   ① 变绿后再点一次必须能取消 —— 且**自动写盘生效**，不再要求手动「保存」；
 *   ② 旧判定是 includes 子串，'dot 223.5.5.5' 被 doh URL 误判已加入（纯函数侧已在
 *      parsers.test.js 锁定）—— 这里走**真实装配层**：toggleCandidate（芯片点击体）
 *      → DNS_STORE 备份 → writeFile → reload-dns，命令经 bridge 桩回答，与真机同路径。
 * 芯片是 innerHTML 字符串，桩 DOM 的 querySelectorAll 驱动不了 → 经 harness 暴露的
 * lastWin（vm 全局）直接调用顶层 toggleCandidate。
 */
'use strict';
const test = require('node:test');
const assert = require('node:assert');
const { createHarness } = require('./lib/app-harness.js');

const PANEL_OK = 'port=20130 bind=loopback module_version=v1.0.0-r1 versioncode=100 ' +
  'engine_version=1.0.0 engine_ver_src=runtime engine_ver_healed=0 lan_ip=127.0.0.1 ' +
  'dns=up dns_pid=2 engine=up engine_pid=1 watchdog=up watchdog_pid=3 ' +
  'factory_key=0 apikeys_total=1 mem_total=1 mem_avail=1 engine_rss=1 dns_rss=1 ' +
  'upstreams_b64= mod_url= accel_sel=';

function boot(opts = {}) {
  const cmds = [];
  const writes = [];   // 每次 KB.writeFile 落盘的内容（base64 解出）
  const h = createHarness({
    execHandler: cmd => {
      cmds.push(cmd);
      // writeFile（dns-upstreams）：顺手解出内容供断言
      if (cmd.includes('dns-upstreams.conf') && cmd.includes('base64 -d')) {
        const m = cmd.match(/printf '%s' '([A-Za-z0-9+/=]+)' \| base64 -d/);
        if (m) writes.push(Buffer.from(m[1], 'base64').toString('utf8'));
        return opts.write === false ? '' : 'write-ok';
      }
      if (cmd.includes('no-src')) return 'exists';        // KB.backupOnce（命令里含字面量分支）
      if (cmd.includes('reload-dns')) return opts.reload === undefined ? 'reloaded' : opts.reload;
      if (cmd.includes('panel')) return PANEL_OK;
      if (cmd.includes('rm -f')) return 'rm-ok';
      return '';
    }
  });
  const els = h.loadApp();
  return { els, cmds, writes, win: h.lastWin };
}
const tick = () => new Promise(r => setTimeout(r, 0));
// 首屏 refresh（panel 快照）是异步落地并覆写编辑缓冲区的 —— 不等它稳定就驱动，
// 断言会与初始化竞态（实测：textarea 被首屏快照写回空，测试假红）
async function settle() { for (let i = 0; i < 6; i++) await tick(); }

test('芯片点击自动生效：加入 → 备份 + 写盘 + 热重载，无需手动「保存」', async () => {
  const { els, cmds, writes, win } = boot();
  await settle();
  await win.toggleCandidate('dot 223.5.5.5');
  await tick();
  assert.strictEqual(els.get('upstreams').value, 'dot 223.5.5.5');
  assert.deepStrictEqual(writes, ['dot 223.5.5.5\n'], '落盘内容必须与切换结果一致');
  assert.ok(cmds.some(c => c.includes('reload-dns')), '写盘后必须热重载');
  assert.ok(els.get('toast').textContent.includes('已加入并生效'));
});

test('再点一次 = 取消：写盘内容移除该行，其它行原样保留', async () => {
  const { els, writes, win } = boot();
  await settle();
  els.get('upstreams').value = 'nameserver 119.29.29.29\ndot 223.5.5.5';
  await win.toggleCandidate('dot 223.5.5.5');
  await tick();
  assert.strictEqual(els.get('upstreams').value, 'nameserver 119.29.29.29');
  assert.deepStrictEqual(writes, ['nameserver 119.29.29.29\n']);
  assert.ok(els.get('toast').textContent.includes('已移除并生效'));
});

test('移除最后一条上游必须被拦（dnsfwd 拿空配置 = 解析全挂），且不下发写入命令', async () => {
  const { els, cmds, writes, win } = boot();
  await settle();
  els.get('upstreams').value = 'dot 223.5.5.5';
  await win.toggleCandidate('dot 223.5.5.5');
  await tick();
  assert.strictEqual(els.get('upstreams').value, 'dot 223.5.5.5', '缓冲区必须原样保留');
  assert.strictEqual(writes.length, 0, '不得写盘');
  assert.ok(!cmds.some(c => c.includes('reload-dns')), '不得热重载');
  assert.ok(els.get('toast').textContent.includes('至少保留一条'));
});

test('写盘失败：缓冲区还原到切换前，不谎报生效', async () => {
  const { els, win } = boot({ write: false });
  await settle();
  els.get('upstreams').value = '';
  await win.toggleCandidate('dot 223.5.5.5');
  await tick();
  assert.strictEqual(els.get('upstreams').value, '', '必须还原到切换前');
  assert.ok(els.get('toast').textContent.includes('已还原'));
  assert.ok(!els.get('toast').textContent.includes('已加入并生效'));
});

test('热重载失败：配置已写入（缓冲区不还原），但不得谎报"已生效"', async () => {
  const { els, writes, win } = boot({ reload: 'fail' });
  await settle();
  await win.toggleCandidate('dot 223.5.5.5');
  await tick();
  assert.deepStrictEqual(writes, ['dot 223.5.5.5\n'], '写盘已发生');
  assert.strictEqual(els.get('upstreams').value, 'dot 223.5.5.5');
  assert.ok(!els.get('toast').textContent.includes('已加入并生效'), '不得谎报生效');
});

test('候选池补全：明文 6 / DoH 5 / DoT 4（2026-10-02 用户反馈"选项很少"）', async () => {
  const { els } = boot();
  await settle();
  const html = els.get('cand-chips').innerHTML;
  const chips = (html.match(/class="chip[ "]/g) || []).length;
  assert.strictEqual(chips, 15, `芯片总数应为 15，实际 ${chips}`);
  for (const must of ['dot.pub', '120.53.53.53', 'dns.alidns.com', '180.76.76.76', '1.2.4.8']) {
    assert.ok(html.includes(must), `候选池缺 ${must}`);
  }
});
