/* parsers.js 离线回归测试 —— fixture 全部来自真机实测输出。
 * 运行：node --test module/webroot/test/
 * 每个用例对应一个历史上真实发生过的 bug（CRLF / box 模式 / 空格对齐 / 并发串扰）。
 */
'use strict';
const { test } = require('node:test');
const assert = require('node:assert');
const KP = require('../parsers.js');

// ── parseProp：module.prop 解析（引擎版本曾因此显示"未知"）──
const MODULE_PROP = 'id=ninerouter-go\nname=9Router Go AI Proxy\nversion=v1.9.1-r1\nversionCode=109010\n';
test('parseProp 提取 version（无 CRLF）', () => {
  assert.strictEqual(KP.parseProp(MODULE_PROP, 'version'), 'v1.9.1-r1');
});
test('parseProp 容忍 CRLF（ksu.exec 返回 \\r\\n）', () => {
  assert.strictEqual(KP.parseProp(MODULE_PROP.replace(/\n/g, '\r\n'), 'version'), 'v1.9.1-r1');
});
test('parseProp 缺失键返回空串（不抛异常）', () => {
  assert.strictEqual(KP.parseProp(MODULE_PROP, 'nope'), '');
});

// ── cmpVer：引擎更新比较 ──
test('cmpVer 数值化比较（1.9 < 1.10，不能按字符串比）', () => {
  assert.strictEqual(KP.cmpVer('1.9.1', '1.10.0'), 1);
  assert.strictEqual(KP.cmpVer('1.10.0', '1.9.1'), -1);
  assert.strictEqual(KP.cmpVer('1.9.1', '1.9.1'), 0);
});

// ── parseOpsStatus：lib/ops.sh status 输出 ──
const OPS_STATUS = 'port=20128\r\nbind=loopback\r\nmodule_version=v1.9.1-r1\r\nengine_version=v1.9.1\r\ndns=up\r\ndns_pid=7809\r\nengine=up\r\nengine_pid=25720\r\nwatchdog=up\r\nwatchdog_pid=25700\r\nfactory_key=1\r\napikeys_total=2\r\n';
test('parseOpsStatus 解析全部键并剥离 CRLF', () => {
  const st = KP.parseOpsStatus(OPS_STATUS);
  assert.strictEqual(st.port, '20128');
  assert.strictEqual(st.dns, 'up');
  assert.strictEqual(st.dns_pid, '7809');
  assert.strictEqual(st.engine_version, 'v1.9.1');
  assert.strictEqual(st.watchdog, 'up');
  assert.strictEqual(st.watchdog_pid, '25700');
  assert.strictEqual(st.factory_key, '1');
});
test('parseOpsStatus 覆盖 dns=yielded（:53 被占自动让路）', () => {
  const st = KP.parseOpsStatus('dns=yielded\nengine=down\n');
  assert.strictEqual(st.dns, 'yielded');
});
test('parseOpsStatus 覆盖 engine=stopped（用户显式停服，与"未运行"必须可区分）', () => {
  const st = KP.parseOpsStatus('engine=stopped engine_pid= watchdog=up watchdog_pid=2338');
  assert.strictEqual(st.engine, 'stopped');
  assert.strictEqual(st.watchdog, 'up');
});
test('parseOpsStatus 兼容单行空格分隔（promise 降级形态）', () => {
  const st = KP.parseOpsStatus('port=20128 bind=loopback module_version=v1.9.1-r1 engine_version=v1.9.1 dns=up engine=up factory_key=1 apikeys_total=2');
  assert.strictEqual(st.port, '20128');
  assert.strictEqual(st.dns, 'up');
  assert.strictEqual(st.engine_version, 'v1.9.1');
  assert.strictEqual(st.factory_key, '1');
  assert.strictEqual(st.apikeys_total, '2');
});

// ── parseMeminfo / parseProcRss：资源占用曾全部显示 "-" ──
const MEMINFO = 'MemTotal:        5809472 kB\r\nMemFree:          912004 kB\r\nMemAvailable:    1465228 kB\r\n';
test('parseMeminfo 提取 total/avail（CRLF 容忍）', () => {
  const mi = KP.parseMeminfo(MEMINFO);
  assert.strictEqual(mi.total, 5809472);
  assert.strictEqual(mi.avail, 1465228);
});
test('parseProcRss 提取 VmRSS', () => {
  assert.strictEqual(KP.parseProcRss('VmRSS:    102400 kB'), 102400);
  assert.strictEqual(KP.parseProcRss('Name: 9router-go'), null);
});

// ── dnsfwd -P 解析：输出为空格对齐（od -c 实锤无 tab），曾解析出 0 行 ──
const PROBE_OUT = [
  // 明文（真机实测行）
  '  223.5.5.5                          v4  www.baidu.com                2ms  2/2  183.240.99.224,111.45.11.5',
  // DoH：upstream 字段本身含空格
  '  doh https://223.5.5.5:443/dns-query doh www.baidu.com             455ms  2/2  183.240.99.224,111.45.11.5',
  // DoT
  '  dot 223.5.5.5:853                  dot www.baidu.com             121ms  2/2  111.45.11.5',
  // 可用率不足，应被过滤
  '  8.8.8.8                            v4  www.baidu.com               96ms  0/2  ',
  // fake-ip（TUN 接管）：dnsfwd.c:1368 在行尾追加标记（真机实测形状），必须被重罚
  '  1.1.1.1                            v4  www.baidu.com                3ms  2/2  198.18.0.1  ← fake-ip(TUN 接管)',
].join('\n');
test('parseDnsProbeOutput 解析空格对齐输出（含 DoH upstream 空格）', () => {
  const rows = KP.parseDnsProbeOutput(PROBE_OUT);
  assert.strictEqual(rows.length, 4);
  // 评分 = 可用率×100 − RTT/50：明文 2ms → 99.96 最高；DoT 121ms → 97.6；DoH 455ms → 90.9
  assert.strictEqual(rows[0].upstream, '223.5.5.5');
  assert.strictEqual(rows[1].upstream, 'dot 223.5.5.5:853');
  assert.strictEqual(rows[2].upstream, 'doh https://223.5.5.5:443/dns-query');
});
test('parseDnsProbeOutput 过滤可用率 <50%', () => {
  const rows = KP.parseDnsProbeOutput(PROBE_OUT);
  assert.ok(rows.every(r => r.upstream !== '8.8.8.8'));
  const one = KP.parseDnsProbeOutput('  8.8.8.8  v4  x.com  10ms  0/2');
  assert.strictEqual(one.length, 0);
});
test('parseDnsProbeOutput 按评分降序（可用率权重 > 延迟）', () => {
  const rows = KP.parseDnsProbeOutput(PROBE_OUT);
  for (let i = 1; i < rows.length; i++) assert.ok(rows[i - 1].score >= rows[i].score);
});
test('parseDnsProbeLine 坏行返回 null（不抛异常）', () => {
  assert.strictEqual(KP.parseDnsProbeLine(''), null);
  assert.strictEqual(KP.parseDnsProbeLine('garbage line'), null);
});
test('parseDnsProbeOutput：fake-ip 上游罚 100（index.html 的承诺曾静默缺失）', () => {
  const rows = KP.parseDnsProbeOutput(PROBE_OUT);
  const fake = rows.find(r => r.upstream === '1.1.1.1');
  assert.ok(fake, 'fake-ip 行必须被解析出来');
  assert.strictEqual(fake.fakeip, true);
  // 2/2 可用、3ms：不罚分是 99.94，罚 100 后是 −0.06
  assert.strictEqual(fake.score, 100 - 3 / 50 - 100);
  assert.strictEqual(rows[0].fakeip, false, '普通行不得被误判 fake-ip');
  assert.ok(fake.score < rows[0].score, '假 IP 上游绝不能排到前面');
});

// ── 上游行规范化 ──
test('normUpstream 归一化四种输入', () => {
  assert.strictEqual(KP.normUpstream('119.29.29.29'), 'nameserver 119.29.29.29');
  assert.strictEqual(KP.normUpstream('https://doh.pub/dns-query'), 'doh https://doh.pub/dns-query');
  assert.strictEqual(KP.normUpstream('nameserver 223.5.5.5'), 'nameserver 223.5.5.5');
  assert.strictEqual(KP.normUpstream('# comment'), '# comment');
});
test('upType 分类', () => {
  assert.strictEqual(KP.upType('nameserver 1.1.1.1'), '明文');
  assert.strictEqual(KP.upType('doh https://doh.pub/dns-query'), 'DoH');
  assert.strictEqual(KP.upType('dot 223.5.5.5'), 'DoT');
});

// ── 孤儿判定：曾误删 oc/qd 内置别名的自定义模型（结构护栏）──
const LIVE = new Set([
  'openai-compatible-chat-069fdcc2-f29b-41da-b3b9-11f4702667ac', // 存活节点
  'deepseek', 'qoder', 'codebuddy-cn'                            // 存活连接
]);
const KV_LINES = [
  'oc|mimo-v2.5-free|llm',                                            // 内置别名 → 永不清理
  'openrouter|google/gemini-2.5-pro|llm',                             // 内置别名 → 永不清理
  'openai-compatible-chat-069fdcc2-f29b-41da-b3b9-11f4702667ac|x|llm',// 存活节点 → 不清理
  'openai-compatible-chat-ce065e91-ca59-4649-af61-5b93cf02c186|x|llm',// 已删节点 → 孤儿
  'qd|mimo-v2.6-flash-free|llm'                                       // 内置别名 → 永不清理
];
test('孤儿判定：UUID 形状且不在存活集合才判定', () => {
  const aliases = KP.extractAliases(KV_LINES);
  const orphans = KP.computeOrphans(aliases, LIVE);
  assert.deepStrictEqual(orphans, ['openai-compatible-chat-ce065e91-ca59-4649-af61-5b93cf02c186']);
});
test('孤儿判定：内置别名（oc/qd/openrouter）结构性豁免', () => {
  const aliases = KP.extractAliases(KV_LINES);
  const orphans = KP.computeOrphans(aliases, new Set()); // 即使存活集合为空
  assert.ok(!orphans.includes('oc'));
  assert.ok(!orphans.includes('qd'));
  assert.ok(!orphans.includes('openrouter'));
});
// 2026-09-30 用户反馈（清理孤儿后模型仍以内部 ID 出现）把边界暴露出来：
// "节点已删、但连接还在"的 UUID 别名**仍然可路由**（provider 就是连接上的 provider）→
// 它绝不能进孤儿清单。改"存活集合只认节点"看起来更干净，实际会误删能用的模型。
test('孤儿判定：只剩连接（节点已删）的 UUID 别名不算孤儿 —— 它仍可路由', () => {
  const dangling = 'openai-compatible-chat-b5b35395-3025-4810-90cd-5473475261b8';
  const aliases = KP.extractAliases([dangling + '|some-model|llm']);
  assert.deepStrictEqual(KP.computeOrphans(aliases, new Set([dangling])), [],
    '只剩连接的别名被判成孤儿 → 清理会误删仍可路由的模型');
});
test('存活集合：连接 provider 必须计入（扫描行里不含 |）', () => {
  const { live } = KP.parseScanLines(['deepseek', 'openai-compatible-chat-069fdcc2-f29b-41da-b3b9-11f4702667ac']);
  assert.ok(live.has('deepseek') && live.has('openai-compatible-chat-069fdcc2-f29b-41da-b3b9-11f4702667ac'),
    '连接 provider 没进存活集合 → 该 provider 的自定义模型会被误删');
});

// ── 跨语言接缝门禁：形状判定的样本只应有一份（2026-09-30 架构扫描 C3）────────────────
// 面板这里判"是不是内部节点 ID"，引擎（internal/db.IsInternalNodeAlias）用同一形状做自愈与
// 列表发布。任一侧改形状都不会报错：面板静默不再识别孤儿，或引擎把内部 ID 发布给用户。
// 两侧因此读**同一份夹具**，各自断言逐样本一致 —— 一侧漂移，两侧之一必红。
test('形状判定：与引擎共用一份样本夹具（任一侧漂移必红）', () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const fixture = path.join(__dirname, '..', '..', '..', 'tools', 'fixtures', 'internal-node-alias-samples.txt');
  const lines = fs.readFileSync(fixture, 'utf8').split('\n')
    .map(l => l.replace(/\r$/, ''))
    .filter(l => l.trim() !== '' && !l.trim().startsWith('#'));
  assert.ok(lines.length >= 10, `样本太少（${lines.length} 行）—— 接缝门禁形同虚设`);
  const bad = [];
  for (const line of lines) {
    const i = line.lastIndexOf(' ');
    const sample = line.slice(0, i), want = line.slice(i + 1).trim() === 'yes';
    if (KP.UUID_ALIAS.test(sample) !== want) bad.push(`${JSON.stringify(sample)} 期望 ${want}`);
  }
  assert.deepStrictEqual(bad, [],
    `面板的 UUID_ALIAS 与引擎的 IsInternalNodeAlias 判定不一致（改形状时两侧要一起改）：${bad.join('; ')}`);
});

// ── 引擎更新装前门禁（2026-09-26 事故 fixture）──
// 事故现场：加速节点对 release 资产返回 404，正文 "Not Found"（9 字节）被装成引擎，
// 备份被同一份垃圾覆盖，设备上再没有可用引擎（引擎与面板全部停摆）。
test('engineFileGate：9 字节 "Not Found"（404 正文）判死', () => {
  const g = KP.engineFileGate(9, '4e6f7420'); // "Not " 的 4 字节十六进制
  assert.strictEqual(g.ok, false);
  assert.ok(/不是引擎二进制/.test(g.reason), g.reason);
});
test('engineFileGate：体积够但文件头不是 ELF（HTML 错误页）判死', () => {
  const g = KP.engineFileGate(6 * 1024 * 1024, '3c68746d'); // "<htm"
  assert.strictEqual(g.ok, false);
  assert.ok(/不是 ELF/.test(g.reason), g.reason);
});
test('engineFileGate：探针读不到就判死，绝不默认放行', () => {
  assert.strictEqual(KP.engineFileGate(0, '').ok, false);
  assert.strictEqual(KP.engineFileGate(NaN, '7f454c46').ok, false);
  assert.strictEqual(KP.engineFileGate(undefined, '7f454c46').ok, false);
});
test('engineFileGate：真实引擎（25428128 字节 + ELF 魔数）通过，大小写不敏感', () => {
  assert.strictEqual(KP.engineFileGate(25428128, '7f454c46').ok, true);
  assert.strictEqual(KP.engineFileGate(25428128, '7F454C46').ok, true);
});
test('checksumGate：取不到校验和即拒绝（旧实现此处放行了 404 正文）', () => {
  const g = KP.checksumGate('', 'abc');
  assert.strictEqual(g.ok, false);
  assert.ok(/未取到 SHA256SUMS/.test(g.reason), g.reason);
  assert.strictEqual(KP.checksumGate('Not Found', '').ok, false, '404 正文不算校验和');
});
test('checksumGate：不匹配拒绝、匹配通过（忽略大小写与空白）', () => {
  const h = 'b9e06b6a85e5590eecac714f4356d227c706df0b9f9daacd764b0372c2ec67d6';
  assert.strictEqual(KP.checksumGate(h, h.toUpperCase() + '\n').ok, true);
  assert.strictEqual(KP.checksumGate(h, h.replace(/^b/, 'a')).ok, false);
});

// ── 生命周期状态词表：词 → 文案/严重度（候选 2）──
test('stateLabel：已登记词给出文案与严重度（与改动前逐字一致）', () => {
  assert.strictEqual(KP.stateLabel('engine', 'up', '1234').text, '运行中 (PID 1234)');
  assert.strictEqual(KP.stateLabel('engine', 'up', '1234').tone, 'ok');
  assert.strictEqual(KP.stateLabel('engine', 'stopped').text, '已停止（用户设置）');
  assert.strictEqual(KP.stateLabel('engine', 'stopped').tone, 'warn');   // 用户意图，不是故障
  assert.strictEqual(KP.stateLabel('dns', 'disabled').text, '已关闭（用户设置）');
  assert.strictEqual(KP.stateLabel('dns', 'yielded').tone, 'warn');
  assert.strictEqual(KP.stateLabel('dns', 'down').text, '未运行');
  assert.strictEqual(KP.stateLabel('watchdog', 'stale').text, '未运行（已武装，下次重启生效）');
  assert.strictEqual(KP.stateLabel('watchdog', 'down').text, '未启用');
});
test('stateLabel：未登记的词不冒充正常（报未知 + 透出原词）', () => {
  const s = KP.stateLabel('engine', 'yawned', '9');
  assert.strictEqual(s.unknown, true);
  assert.strictEqual(s.tone, 'err');
  assert.ok(s.text.includes('engine=yawned'), s.text);
});
test('stateLabel：未知 kind 也不抛（表格被删/拼错时不炸界面）', () => {
  const s0 = KP.stateLabel('nope', 'up', '1');
  assert.strictEqual(s0.unknown, true);
  assert.strictEqual(typeof s0.text, 'string');
});

// ── 引擎版本"来源"自检文案（Phase 26：面板谎报旧版本时用户要能一眼看出）──
test('engineVersionSourceLabel：三种来源 + 是否刚自愈', () => {
  assert.strictEqual(KP.engineVersionSourceLabel('runtime', false), '运行期记录');
  assert.strictEqual(KP.engineVersionSourceLabel('package', true), '包内 · 刚自愈');
  assert.strictEqual(KP.engineVersionSourceLabel('none', false), '无来源');
});
test('engineVersionSourceLabel：来源缺失/未知不冒充正常', () => {
  assert.strictEqual(KP.engineVersionSourceLabel(undefined, undefined), '未知来源(undefined)');
  assert.ok(KP.engineVersionSourceLabel('bogus', false).includes('bogus'));
});
// ── "先门禁后动作"：顺序不变量离线可断言（2026-09-30 架构扫描 C1 重整）──────────
// 背景：这里曾经有两个求值器 —— planSteps（按计划顺序 walk，但**生产零调用**，只有测试在用）
// 与 planGate（生产唯一入口，却只用计划判断"这个阶段是不是门禁"，**顺序不参与求值**）。
// 于是"顺序离线可断言"实际只断言了常量数组本身；运行期的先后由各调用点的 await 顺序决定，
// 把它写反**不会有任何门禁变红**（等于假信心）。
// 现在的分工：
//   · **顺序**由流程用例守 —— gate-flows.test.js / orphan-scan.test.js 断言真实命令序列；
//   · 这里只锁两件可机械判定的事：**计划结构**与**调用点与计划一致**（见下）。
test('计划结构：ENGINE_UPDATE_PLAN 的 install 之前必须有两个门禁', () => {
  const idx = KP.ENGINE_UPDATE_PLAN.findIndex(s => s.id === 'install');
  assert.ok(idx > 0, 'ENGINE_UPDATE_PLAN 里没有 install 步骤');
  assert.deepStrictEqual(KP.ENGINE_UPDATE_PLAN.slice(0, idx).filter(s => s.gate).map(s => s.id),
    ['file-gate', 'sum-gate']);
});
test('计划结构：DNS_OPTIMIZE_PLAN 的 write 之前必须有 rows-gate 与 backup-gate', () => {
  const idx = KP.DNS_OPTIMIZE_PLAN.findIndex(s => s.id === 'write');
  assert.ok(idx > 0, 'DNS_OPTIMIZE_PLAN 里没有 write 步骤');
  assert.deepStrictEqual(KP.DNS_OPTIMIZE_PLAN.slice(0, idx).filter(s => s.gate).map(s => s.id),
    ['rows-gate', 'backup-gate']);
});
test('计划结构：ORPHAN_CLEAN_PLAN 的 snapshot 是门禁且排在 delete 之前', () => {
  const ids = KP.ORPHAN_CLEAN_PLAN.map(s => s.id);
  const i = ids.indexOf('snapshot');
  assert.ok(i >= 0 && i < ids.indexOf('delete'), `顺序不对：${ids.join(' → ')}`);
  assert.strictEqual(KP.ORPHAN_CLEAN_PLAN[i].gate, true, 'snapshot 必须是门禁（快照失败不得删除）');
});
test('计划结构：MODULE_UPDATE_PLAN 的 zip-gate 在 install 之前', () => {
  const ids = KP.MODULE_UPDATE_PLAN.map(s => s.id);
  const i = ids.indexOf('zip-gate');
  assert.ok(i >= 0 && i < ids.indexOf('install'), `顺序不对：${ids.join(' → ')}`);
});
// 调用点与计划必须一致：`planGate(KP.X_PLAN, 'phase', …)` 的阶段名一旦拼错/改名，
// find 返回 undefined → `.gate` 为假 → 该门禁被**静默当成非门禁跳过**（比报错危险得多）。
// 这条扫描把"阶段名必须存在于对应计划、且确实是门禁"变成会红的约束（跨文件，机械可判定）。
test('调用点与计划一致：planGate 的每个阶段名都必须是该计划里的门禁', () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const dir = path.join(__dirname, '..');
  const bad = [];
  let seen = 0;
  for (const f of fs.readdirSync(dir).filter(n => n.endsWith('.js'))) {
    const src = fs.readFileSync(path.join(dir, f), 'utf8');
    const re = /planGate\(\s*KP\.([A-Z_]+)\s*,\s*'([^']+)'/g;
    let m;
    while ((m = re.exec(src)) !== null) {
      seen++;
      const plan = KP[m[1]];
      if (!Array.isArray(plan)) { bad.push(`${f}: 未知计划 ${m[1]}`); continue; }
      const step = plan.find(s => s.id === m[2]);
      if (!step) bad.push(`${f}: 阶段 '${m[2]}' 不在 ${m[1]} 里（会被静默当成非门禁跳过）`);
      else if (step.gate !== true) bad.push(`${f}: 阶段 '${m[2]}' 在 ${m[1]} 里但不是门禁`);
    }
  }
  assert.ok(seen >= 6, `只扫到 ${seen} 个 planGate 调用点 —— 扫描没生效？`);
  assert.deepStrictEqual(bad, [], bad.join('; '));
});
// ── planGate：阶段切片求值（2026-09-28 三次同构事故的接口级修复）──
// 调用方分阶段执行，每次只持有本阶段 fact；若传整计划让"缺 fact 的门禁 = 拒绝"生效，
// 必然被下一道门禁假拦（scanOrphans 事故 + optimize/engUpdate 两个活体）。
test('planGate：只求值指定阶段，后续门禁的 fact 缺失不再假拦', () => {
  // 与事故同构的调用形态：rows-gate 过了、backup-gate 没有 fact
  const v = KP.planGate(KP.DNS_OPTIMIZE_PLAN, 'rows-gate', { ok: true });
  assert.strictEqual(v.ok, true, '本阶段通过就必须放行，不得被后续无 fact 门禁假拦');
});
test('planGate：本阶段 fact 未过 → 拒绝并透出 reason', () => {
  const v = KP.planGate(KP.DNS_OPTIMIZE_PLAN, 'rows-gate', { ok: false, reason: '没有可用率 ≥50% 的上游' });
  assert.strictEqual(v.ok, false);
  assert.strictEqual(v.reason, '没有可用率 ≥50% 的上游');
});
test('planGate：缺 fact / 错误阶段 / 非门禁阶段的行为', () => {
  assert.strictEqual(KP.planGate(KP.DNS_OPTIMIZE_PLAN, 'rows-gate', null).ok, false, '缺 fact = 拒绝');
  assert.strictEqual(KP.planGate(KP.DNS_OPTIMIZE_PLAN, 'probe', { ok: true }).ok, true, '非门禁阶段不拦');
  assert.strictEqual(KP.planGate(KP.DNS_OPTIMIZE_PLAN, 'nosuch', null).ok, true, '未知阶段不拦（不冒充门禁）');
});
test('planGate：每道真实门禁都能用计划常量单独求值（阶段覆盖完整）', () => {
  for (const plan of [KP.ORPHAN_CLEAN_PLAN, KP.ENGINE_UPDATE_PLAN, KP.MODULE_UPDATE_PLAN, KP.DNS_OPTIMIZE_PLAN]) {
    const gates = plan.filter(s => s.gate).map(s => s.id);
    assert.ok(gates.length > 0, '计划必须有门禁');
    for (const g of gates) {
      assert.strictEqual(KP.planGate(plan, g, { ok: true }).ok, true, `${g} 用 ok:true 必须放行`);
      assert.strictEqual(KP.planGate(plan, g, { ok: false }).ok, false, `${g} 用 ok:false 必须拒绝`);
    }
  }
});

// ── parseScanLines / parseCredScan：从装配层抽出的纯解析 ──
test('parseScanLines：无 | 行 = 存活，| 行首段 = 别名', () => {
  const { live, aliases } = KP.parseScanLines([
    'openai-compatible-chat-069fdcc2-f29b-41da-b3b9-11f4702667ac',  // 节点
    'codebuddy-cn',                                                  // 连接
    'oc|big-pickle|llm',                                             // kv 别名
    ''                                                               // 空行忽略
  ]);
  assert.ok(live.has('openai-compatible-chat-069fdcc2-f29b-41da-b3b9-11f4702667ac'));
  assert.ok(live.has('codebuddy-cn'));
  assert.ok(aliases.has('oc'));
  assert.strictEqual(live.size, 2);
});
test('parseScanLines：容忍 CRLF', () => {
  const { live, aliases } = KP.parseScanLines(['node-a\r', 'oc|m|llm\r']);
  assert.ok(live.has('node-a') && aliases.has('oc'));
});
test('parseCredScan：缺 key 的 apiKey 连接与缺 token 的 oauth 连接都要报', () => {
  const rows = KP.parseCredScan([
    "id1|openai|apiKey|null|null|null",       // apiKey 缺 → 报
    "id2|claude|oauth|x|null|tok",            // oauth 有 token → 不报
    "id3|gemini|apiKey|k|null|null",          // 有 key → 不报
    "id4|bad|weird|null|null|null"            // 未知 authType 无 key → 报
  ].join('\n'));
  assert.deepStrictEqual(rows.map(r => r.provider), ['openai', 'bad']);
});

// ── 批量测速输出（A5）──
// 索引必须写在行里：`cat *.out` 的 glob 是**字典序**（b0-10.out 会排在 b0-2.out 之前），
// 靠行序映射回节点在节点数 ≥ 11 时会错位（内置 15 个 + 自定义，必然触发）。
test('parseCurlTimings：按行内索引还原顺序，不靠 cat 的行序', () => {
  const rows = KP.parseCurlTimings([
    '2\thttps://c/\t200 0.300',
    '10\thttps://k/\t200 0.100',
    '0\thttps://a/\t200 0.200'
  ].join('\n'));
  assert.deepStrictEqual(rows.map(r => r.i), [0, 2, 10], '必须按索引排序');
  assert.deepStrictEqual(rows.map(r => r.node), ['https://a/', 'https://c/', 'https://k/']);
  assert.strictEqual(rows[0].ok, true);
  assert.strictEqual(rows[0].ms, 200, 'ms 由 time_total 秒换算');
});
test('parseCurlTimings：超时/非 200/坏行 一律不冒充可用', () => {
  const rows = KP.parseCurlTimings([
    '0\thttps://slow/\t',              // curl -m 8 超时 → 第三段为空
    '1\thttps://e404/\t404 0.010',     // 非 200
    '2\thttps://ok/\t200 0.050',
    'bad line without tabs',           // 坏行 → 忽略
    ''
  ].join('\n'));
  assert.deepStrictEqual(rows.map(r => [r.i, r.ok]), [[0, false], [1, false], [2, true]]);
});
test('parseCurlTimings：失败原因必须带出来（只说"不可用"没法排查）', () => {
  const rows = KP.parseCurlTimings([
    '0\thttps://dead/\t000 3.000825 Could not resolve host: no-such-host-9r.invalid',
    '1\thttps://ok/\t200 0.050'
  ].join('\n'));
  assert.strictEqual(rows[0].ok, false);
  assert.ok(rows[0].err.includes('Could not resolve host'), 'errormsg 要透出来（超时/解析/证书可区分）');
  assert.ok(Math.abs(rows[0].ms - 3000.825) < 1, '失败也要量化耗时（3s 是连接超时踩线）');
  assert.strictEqual(rows[1].err, '', '成功行没有原因');
});
