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
  assert.strictEqual(KP.stateLabel('dns', 'disabled').text, '已关闭（用户设置）');
  assert.strictEqual(KP.stateLabel('dns', 'yielded').tone, 'warn');
  assert.strictEqual(KP.stateLabel('dns', 'down').text, '未运行');
  assert.strictEqual(KP.stateLabel('watchdog', 'stale').text, '未运行（已武装，下次重启生效）');
  assert.strictEqual(KP.stateLabel('watchdog', 'down').text, '未启用');
});

// 严重度四档（2026-09-30 用户反馈「显示运行的状态感觉很多、很乱」，改为**底色**表状态）：
// 绿=正常在跑 / 灰=用户要它别跑（退出）/ 琥珀=要留意 / 红=真故障。
// 这里锁的是"什么算故障"这一判断本身 —— 把用户主动关掉的画成红色，等于界面在报警。
test('状态严重度：用户意图的「退出」是灰档，不是红档', () => {
  assert.strictEqual(KP.stateLabel('engine', 'stopped').tone, 'off', '「已停止」是用户意图，不是故障');
  assert.strictEqual(KP.stateLabel('dns', 'disabled').tone, 'off', '「已关闭」是用户意图，不是故障');
  assert.strictEqual(KP.stateLabel('watchdog', 'down').tone, 'off', '守护未启用不等于故障');
  // 真故障仍是红档
  assert.strictEqual(KP.stateLabel('engine', 'down').tone, 'err');
  assert.strictEqual(KP.stateLabel('dns', 'down').tone, 'err');
  // 待留意档：已让路 / 已武装但没跑 —— 都不是故障，但也不该安静地当正常
  assert.strictEqual(KP.stateLabel('dns', 'yielded').tone, 'warn');
  assert.strictEqual(KP.stateLabel('watchdog', 'stale').tone, 'warn');
  // 每一档都要有对应色值（off 漏配会让 style.color 落成 undefined，静默不生效）
  for (const tone of ['ok', 'warn', 'err', 'off']) {
    assert.ok(KP.TONE_COLORS[tone], `TONE_COLORS 缺 ${tone}`);
  }
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

// ── 概览三块读数的短形态（2026-09-30：三块并排，底色表状态 + 只留 PID）──
test('stateTile：在跑时只给 PID（完整句留给 DNS 页的 kv 行）', () => {
  assert.deepStrictEqual(KP.stateTile('engine', 'up', '24817'), { tone: 'ok', text: 'PID 24817' });
  assert.deepStrictEqual(KP.stateTile('dns', 'up', '101'), { tone: 'ok', text: 'PID 101' });
  assert.deepStrictEqual(KP.stateTile('watchdog', 'up', '303'), { tone: 'ok', text: 'PID 303' });
  // pid 拿不到时不许拼出 "PID undefined"
  assert.deepStrictEqual(KP.stateTile('engine', 'up', ''), { tone: 'ok', text: '运行中' });
});
test('stateTile：没在跑时给短词，且短到能三块并排', () => {
  const cases = [
    ['dns', 'disabled', '已关闭'], ['dns', 'yielded', '已让路'], ['dns', 'down', '未运行'],
    ['engine', 'stopped', '已停止'], ['engine', 'down', '未运行'],
    ['watchdog', 'stale', '待重启'], ['watchdog', 'down', '未启用'],
  ];
  for (const [kind, value, want] of cases) {
    const tile = KP.stateTile(kind, value, '');
    assert.strictEqual(tile.text, want, `${kind}=${value}`);
    // 同一份状态知识两种粒度：短形态必须真的更短，否则概览又会变乱
    assert.ok(tile.text.length <= 4, `${kind}=${value} 的短词太长：${tile.text}`);
    assert.ok(tile.text.length <= KP.stateLabel(kind, value, '').text.length,
      `${kind}=${value} 的短形态没有比完整句短`);
  }
});
test('stateTile：未登记的词不冒充正常（与 stateLabel 同一条纪律）', () => {
  assert.deepStrictEqual(KP.stateTile('engine', 'yawned', '9'), { tone: 'err', text: '未知' });
  assert.deepStrictEqual(KP.stateTile('nope', 'up', '1'), { tone: 'err', text: '未知' });
});
test('状态词表：每个登记词都要有可用 tone 与短词（漏配 = 底色/文字静默失效）', () => {
  for (const [kind, table] of Object.entries(KP.LIFECYCLE_STATES)) {
    for (const [word, entry] of Object.entries(table)) {
      assert.ok(KP.TONE_COLORS[entry.tone], `${kind}=${word} 的 tone=${entry.tone} 在 TONE_COLORS 里没有色值`);
      assert.strictEqual(typeof entry.short, 'string', `${kind}=${word} 缺 short 短词`);
      assert.strictEqual(KP.stateTile(kind, word, '1').tone, entry.tone, `${kind}=${word} 底色档不一致`);
    }
  }
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
// ── 内存文案（2026-10-01 架构评审 #2：概览的引擎/DNS 牌与 DNS 页的状态牌都要显示内存，
//    各写一份格式化就会漂，所以收成 KP.fmtMem）──
test('fmtMem：kB → 面板文案；0 / 读不到一律 "-"（不冒充 0 kB）', () => {
  assert.strictEqual(KP.fmtMem(0), '-');
  assert.strictEqual(KP.fmtMem(''), '-');
  assert.strictEqual(KP.fmtMem(undefined), '-');
  assert.strictEqual(KP.fmtMem('nope'), '-');
  assert.strictEqual(KP.fmtMem(512), '512 kB');
  assert.strictEqual(KP.fmtMem(1024), '1.0 MB');
  assert.strictEqual(KP.fmtMem(24316), '23.7 MB');
  assert.strictEqual(KP.fmtMem('24316'), '23.7 MB', 'panel 输出是字符串，必须能直接吃');
});

// ── planGate 的 fail-closed（2026-10-01 架构评审 #6）──────────────────────────
// 原实现：`if (!step || !step.gate) return { ok: true }` —— 把"阶段名不存在"和
// "这一阶段不是门禁"混在一条 return 里。后果：**阶段名拼错 = 门禁静默失效**。
// 一个永不拦人的门禁比没有门禁更坏，因为它会被当成"已经守住了"。
test('planGate：未知阶段名必须拒绝，而不是静默放行', () => {
  const plan = KP.ORPHAN_CLEAN_PLAN;
  const realPhase = plan.find(s => s.gate).id;
  assert.strictEqual(KP.planGate(plan, realPhase, { ok: true }).ok, true, '真实门禁阶段应放行');
  const bogus = KP.planGate(plan, realPhase + '-typo', { ok: true });
  assert.strictEqual(bogus.ok, false, '阶段名拼错必须红 —— fail-open 的门禁等于没有门禁');
  assert.ok(bogus.reason.includes(realPhase + '-typo'), '理由里要带出拼错的阶段名，便于定位');
  // 非门禁阶段（在计划里、但 step.gate 为假）仍然放行 —— 这条语义不能被上一条吃掉
  const nonGate = plan.find(s => !s.gate);
  if (nonGate) assert.strictEqual(KP.planGate(plan, nonGate.id, { ok: false }).ok, true, '非门禁阶段不拦');
});
// 源码级扫描：调用点手写的阶段名必须真的在对应计划里。
// 为什么用扫描而不是用例：要触发这些调用点得先造出各阶段的前置 fact（成本高且脆），
// 而"调用点的阶段名 ∈ 计划"是个可以机械判定的形状（同 .prev / escAttr 那两条）。
test('计划门禁：每个 planGate 调用点的阶段名都必须真的在对应计划里', () => {
  const fs = require('node:fs');
  const path = require('node:path');
  const dir = path.join(__dirname, '..');
  const PLANS = {
    ORPHAN_CLEAN_PLAN: KP.ORPHAN_CLEAN_PLAN,
    DNS_OPTIMIZE_PLAN: KP.DNS_OPTIMIZE_PLAN,
    ENGINE_UPDATE_PLAN: KP.ENGINE_UPDATE_PLAN,
    MODULE_UPDATE_PLAN: KP.MODULE_UPDATE_PLAN
  };
  const bad = [];
  let sites = 0;
  for (const f of fs.readdirSync(dir).filter(n => n.endsWith('.js'))) {
    fs.readFileSync(path.join(dir, f), 'utf8').split('\n').forEach((line, i) => {
      const m = line.match(/planGate\(\s*KP\.(\w+)\s*,\s*'([^']+)'/);
      if (!m) return;
      sites++;
      const plan = PLANS[m[1]];
      if (!plan) { bad.push(`${f}:${i + 1} 引用了不存在的计划 KP.${m[1]}`); return; }
      if (!plan.some(s => s.id === m[2])) bad.push(`${f}:${i + 1} 阶段「${m[2]}」不在 ${m[1]} 里`);
    });
  }
  assert.ok(sites >= 8, `只扫到 ${sites} 个 planGate 调用点，扫描规则该更新了`);
  assert.deepStrictEqual(bad, [], `这些调用点的阶段名对不上计划：${bad.join('；')}`);
});

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
  // 未知阶段：**拒绝**（2026-10-01 架构评审 #6 改）。
  // 旧断言是放行，理由是"不冒充门禁" —— 但后果是阶段名拼错时门禁静默消失，
  // 而"永不拦人的门禁"比没有更坏：它会被当成"已经守住了"。
  assert.strictEqual(KP.planGate(KP.DNS_OPTIMIZE_PLAN, 'nosuch', null).ok, false,
    '未知阶段必须拒绝（fail-closed）—— 见上面那条 fail-open 用例');
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

// ── isAlreadyRunning：启动类动作的"本来就在跑"判定（2026-10-01 真机）──
// 背景：点「启动服务」/「开启转发器」时目标可能**已经在跑**，此时 shell 走幂等分支，
// 回的是 running / engine=running 而不是 started / engine=up。界面原先只认后者，于是
// 把"本来就在跑"判成失败、弹「❌ 服务未启动」「❌ dnsfwd 开启失败」—— 用户读到"失败"
// 会以为引擎/DNS 起不来（真相是它好端端跑着）。这条判定被两个页面共用，锁在这里。
// ── ACTION_WORDS：动作回执词的唯一所有者（2026-10-01 架构走查候选 2）──────────
// 原先 expect 在 7+ 个调用点手抄，shell 加幂等/失败词时门禁一条不会红（dnsOn 只认
// started 的两起事故面）。词表 + actionOk 收成一处；shell 侧对齐由 contract-keys 门禁守。
test('ACTION_WORDS：每个动词都有非空成功词集，动词集变化必须显式过门禁', () => {
  const KNOWN = ['start-user', 'restart-engine', 'stop-user', 'enable-dns', 'stop-dns',
                 'install-engine', 'install-module'];
  assert.deepStrictEqual(Object.keys(KP.ACTION_WORDS).sort(), [...KNOWN].sort(),
    '词表动词集变了：contract-keys 的 VERB_EMITTERS 需要同步');
  for (const [verb, w] of Object.entries(KP.ACTION_WORDS)) {
    assert.ok(Array.isArray(w.ok) && w.ok.length > 0, `${verb} 的 ok 词集为空`);
  }
});
test('actionOk：命中成功词才算成功；未知 subcmd / 空输出缺省拒绝（与 planGate 同一纪律）', () => {
  assert.strictEqual(KP.actionOk('start-user', 'engine=running'), true);
  assert.strictEqual(KP.actionOk('restart-engine', 'engine=up'), true);
  assert.strictEqual(KP.actionOk('restart-engine', 'dns-pending'), false, '半启动不许算成功');
  assert.strictEqual(KP.actionOk('enable-dns', 'running'), true, '幂等分支不许判失败');
  assert.strictEqual(KP.actionOk('stop-user', 'stopped'), true);
  assert.strictEqual(KP.actionOk('install-module', 'engine=up'), true);
  assert.strictEqual(KP.actionOk('no-such-verb', 'anything'), false, '未知动词必须拒绝');
  assert.strictEqual(KP.actionOk('stop-user', ''), false, '空输出（shell 没吐词）不许算成功');
});

test('isAlreadyRunning：两种幂等回执都判为"已在跑"（不许再说启动失败）', () => {
  assert.strictEqual(KP.isAlreadyRunning('running'), true, 'enable-dns 的幂等分支');
  assert.strictEqual(KP.isAlreadyRunning('engine=running'), true, 'start-user 的幂等分支');
});
test('isAlreadyRunning：真正启动成功的词不是"已在跑"（否则永远显示已在运行）', () => {
  assert.strictEqual(KP.isAlreadyRunning('started'), false);
  assert.strictEqual(KP.isAlreadyRunning('engine=up'), false);
});
test('isAlreadyRunning：失败态与其它意图态一律不冒充"已在跑"', () => {
  for (const w of ['engine=down', 'stopped', 'yielded', 'disabled', 'off-by-user', '', undefined, null]) {
    assert.strictEqual(KP.isAlreadyRunning(w), false, `[${w}] 不该被当成已在跑`);
  }
});
test('isAlreadyRunning：容忍 CRLF 与首尾空白（ksu.exec 的输出形态）', () => {
  assert.strictEqual(KP.isAlreadyRunning('engine=running\r\n'), true);
  assert.strictEqual(KP.isAlreadyRunning('  running \r'), true);
});
