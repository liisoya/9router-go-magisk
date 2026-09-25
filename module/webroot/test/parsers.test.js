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
const OPS_STATUS = 'port=20128\r\nbind=loopback\r\nmodule_version=v1.9.1-r1\r\nengine_version=v1.9.1\r\ndns=up\r\ndns_pid=7809\r\nengine=up\r\nengine_pid=25720\r\nfactory_key=1\r\napikeys_total=2\r\n';
test('parseOpsStatus 解析全部键并剥离 CRLF', () => {
  const st = KP.parseOpsStatus(OPS_STATUS);
  assert.strictEqual(st.port, '20128');
  assert.strictEqual(st.dns, 'up');
  assert.strictEqual(st.dns_pid, '7809');
  assert.strictEqual(st.engine_version, 'v1.9.1');
  assert.strictEqual(st.factory_key, '1');
});
test('parseOpsStatus 覆盖 dns=yielded（:53 被占自动让路）', () => {
  const st = KP.parseOpsStatus('dns=yielded\nengine=down\n');
  assert.strictEqual(st.dns, 'yielded');
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
].join('\n');
test('parseDnsProbeOutput 解析空格对齐输出（含 DoH upstream 空格）', () => {
  const rows = KP.parseDnsProbeOutput(PROBE_OUT);
  assert.strictEqual(rows.length, 3);
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
