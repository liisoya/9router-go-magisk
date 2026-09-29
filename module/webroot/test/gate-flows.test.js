/* 门禁流程级回归测试 —— 2026-09-28 走查发现的两个活体 bug 的回归缝：
 *
 *   bug A（optimize）：planSteps 整计划求值时 backup-gate 无 fact → 假拦，
 *     DNS 优选在"有可用上游"的健康路径上必然中止（用户看到的是错误的
 *     "❌ 没有可用率 ≥50% 的上游"文案）。修复后：探测 → Top5 → 备份 →
 *     写配置 → 热重载，全链可达。
 *
 *   bug B（engUpdate）：file-gate 通过后 sum-gate 无 fact → 假拦，
 *     引擎更新在"校验和"之前必然中止，install-engine 不可达。
 *     修复后：下载 → 体积/ELF 门禁 → SHA256 → install-engine 全链可达。
 *
 * 两个 bug 与 scanOrphans（orphan-scan.test.js）同构：planSteps 的"缺 fact = 拒绝"
 * 语义下，调用方分阶段执行时只能伪造/缺失后续 fact。修复 = planGate 阶段切片。
 * 运行：node --test module/webroot/test/
 */
'use strict';
const { test } = require('node:test');
const assert = require('node:assert');
const { createHarness } = require('./lib/app-harness.js');

const SHA = 'a'.repeat(64);
const PANEL_LINE = [
  'port=20130 bind=loopback module_version=v1.9.3-r1 versioncode=109030',
  'engine_version=1.9.4 engine_ver_src=runtime engine_ver_healed=0',
  'lan_ip=192.168.1.2 dns=up dns_pid=101 engine=up engine_pid=202',
  'watchdog=up watchdog_pid=303 factory_key=1 apikeys_total=1',
  'mem_total=5809472 mem_avail=1814288 engine_rss=24316 dns_rss=1128',
  'upstreams_b64=bmFtZXNlcnZlciAxMTkuMjkuMjkuMjk=',
  'mod_url= accel_sel='
].join(' ');
const PROBE_OUT = [
  '223.5.5.5            v4  www.baidu.com   28ms  2/2',
  '119.29.29.29         v4  www.baidu.com   45ms  2/2'
].join('\n');

let mode = 'optimize';       // optimize / engUpdate
let fileGatePass = true;     // engUpdate：注入 file-gate 失败形态
let captured = [];

const h = createHarness({ execHandler: cmd => {
  captured.push(cmd);
  // ── 通用 ──
  if (/ops\.sh' 'panel'|ops\.sh panel/.test(cmd)) return PANEL_LINE;
  if (/ops\.sh' 'reload-dns'/.test(cmd)) return 'reloaded';
  // **写命令必须排在读命令前面判断**：写 github-accel 的命令里也含 'github-accel' 这个路径，
  // 把"含 github-accel"当成读会让自动选中被静默判成写失败（这个用例正是为此而加，第一次跑就抓到）。
  if (cmd.includes('base64 -d')) return 'write-ok';                  // writeFile
  if (cmd.includes('github-accel')) return '';                       // readFile(ACCEL_SEL/ACCEL_LIST)：空
  // 批量测速（A5）：每批回 3 行，ms 随索引递增 → i=0 恒为"最快"，用于验证"测速即选中"
  if (cmd.includes('accel-batch')) {
    return [0, 1, 2].map(i => `${i}\thttps://node-${i}/\t200 0.${100 + i}`).join('\n');
  }
  if (cmd.includes('version.json')) return '{"latestVersion":"1.9.4"}';
  if (cmd.includes('__READ_OK__')) return '__READ_OK__';            // readFile（内容可为空）
  if (cmd.includes('.initial') || cmd.includes('.prev')) return 'ok';// backupOnce
  // ── optimize ──
  if (mode === 'optimize' && cmd.includes('bin/dnsfwd')) return PROBE_OUT;
  // ── engUpdate ──
  if (mode === 'engUpdate') {
    if (cmd.includes('curl -fsSL')) return 'dl-ok';                                   // 下载
    if (cmd.includes('wc -c')) return fileGatePass ? '26000000' : '9';               // 体积
    if (cmd.includes('od -An')) return fileGatePass ? '7f454c46' : 'deadbeef';       // ELF 魔数
    if (cmd.includes('curl -sL')) return `${SHA}  9router-go-linux-arm64\n`;         // SHA256SUMS
    if (cmd.includes('sha256sum')) return `${SHA}  /data/local/tmp/9r-eng.new`;      // 实测摘要
    if (/ops\.sh' 'install-engine'/.test(cmd)) return 'engine=up';
  }
  return '';
}});

test('DNS 优选全链可达：探测 → Top5 → 备份 → 写配置 → 热重载（bug A 回归）', async () => {
  mode = 'optimize'; captured = [];
  const els = h.loadApp();
  await els.get('btn-opt').onclick();

  const tbl = els.get('opt-table').innerHTML;
  assert.ok(tbl.includes('<table>'), '探测结果表没有渲染（rows-gate 假拦？）');
  const finalWrite = captured.find(c => c.includes('dns-upstreams.conf') && c.includes('base64 -d'));
  assert.ok(finalWrite, '最终配置写入不可达 —— 优选在备份门禁处被假拦（bug A）');
  assert.ok(finalWrite.includes('base64 -d >') && captured.some(c => /ops\.sh' 'reload-dns'/.test(c)),
    '写完配置必须热重载生效');
});

test('引擎更新全链可达：门禁 → 校验和 → install-engine（bug B 回归）', async () => {
  mode = 'engUpdate'; fileGatePass = true; captured = [];
  const els = h.loadApp();
  await els.get('btn-refresh').onclick();     // 先让 refresh 跑完：state.engineVersion 就绪
  await els.get('btn-eng-check').onclick();   // 拿上游 latestVersion → state.engLatest
  await els.get('btn-eng-update').onclick();

  assert.ok(captured.some(c => /ops\.sh' 'install-engine'/.test(c)),
    'install-engine 不可达 —— file-gate 通过后被无 fact 的 sum-gate 假拦（bug B）');
  assert.ok(els.get('eng-out').textContent.includes('完成'), '更新成功必须有回执');
});

test('file-gate 不过时 install-engine 仍不可达（门禁不因 planGate 而松动）', async () => {
  mode = 'engUpdate'; fileGatePass = false; captured = [];
  const els = h.loadApp();
  await els.get('btn-refresh').onclick();
  await els.get('btn-eng-check').onclick();
  await els.get('btn-eng-update').onclick();

  assert.ok(!captured.some(c => /ops\.sh' 'install-engine'/.test(c)),
    'file-gate 失败时绝不允许 install');
  assert.ok(els.get('eng-out').textContent.includes('已中止'), '必须显式中止');
});

// ── A5：测速即选中 ──
// 原先测速只排序、要用户再手点一个"选" —— 那正是"得先选节点才能用"的摩擦来源。
// 桩里 ms 随索引递增 → i=0 恒为最快 → 对应 builtin 清单第一个（chunk[0]，验证索引映射没错位）。
test('GitHub 测速结束后自动把最快节点写进 github-accel', async () => {
  mode = 'speed'; captured = [];
  const els = h.loadApp();
  await els.get('btn-speed').onclick();

  const w = captured.find(c => c.includes('github-accel') && c.includes('base64 -d'));
  assert.ok(w, '测速结束必须写 github-accel（否则又回到"还得手点一个"）');
  const b64 = (w.match(/'([A-Za-z0-9+/=]+)' \| base64 -d/) || [])[1];
  assert.ok(b64, '写命令的形状变了（本门禁需要同步）');
  assert.strictEqual(Buffer.from(b64, 'base64').toString().trim(), 'https://github.cnxiaobai.com/',
    '选中的必须是最快那个（桩里 i=0 最快 → builtin 清单第一个）');
  assert.ok(els.get('accel-list').innerHTML.includes('已自动选中最快'), '界面要如实说明它自动选了');
});
