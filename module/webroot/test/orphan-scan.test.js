/* 孤儿扫描/清理链路回归测试（桩具收敛进 test/lib/app-harness.js）。
 *
 * 事故 1（2026-09-28）：e0619f5 把 scanOrphans 的直接判断重构成 planSteps 计划化时，
 * 只给了 scan 一个 fact —— planSteps 语义是"遇到第一个未通过的门禁就停"，scan 过了
 * 之后 recheck 门禁没有 fact → 默认拒绝 → blockedBy 永远非空，「扫描结果异常」警告
 * 每次都显示，孤儿清理功能整体失联（与扫描/DB/传输无关：28 次真机采样数据全部健康）。
 *
 * 事故 2（2026-09-28）：cleanOrphans 快照 SQL 把全部单引号 .replace 成两个 →
 * scope IN (''customModels'',…) 是 sqlite3 语法错 → 0 字节快照 → 永远"快照失败"。
 *
 * 判据（跑真实的 app.js，桩 shell 桥）：
 *   · 健康扫描 → 必须列出孤儿并启用清理按钮，不显示警告；
 *   · 不可信扫描（有别名但无节点/连接）→ 必须中止判定（2026-09-25 防误删护栏）；
 *   · 清理链路 → 快照 SQL 不得翻倍引号、DELETE 必须真实发出、必须有回执；
 *   · 复查读失败 → 必须中止（C2：读失败不得冒充"查无存活"放行删除）。
 * 运行：node --test module/webroot/test/
 */
'use strict';
const { test } = require('node:test');
const assert = require('node:assert');
const { createHarness } = require('./lib/app-harness.js');

const WARN = '扫描结果异常';

let sqlFixture = '';        // 每个用例注入自己的扫描结果
let failRecheck = false;    // 注入：复查命令读失败（sqlite3 无输出形态）
let captured = [];          // 捕获的命令
let onSnapshot = null;      // 测试钩子：清理流程的命令执行时同步触发一次（模拟"清理在途时用户又点了扫描"）
// 注意触发点选在**复查**命令（比快照更早）：桩的回调都是 setTimeout(0)，从快照处触发时
// 第二次扫描的回包会排在快照之后 → DELETE 早就发出去了，打不中窗口（真机上每个 shell
// 往返是秒级，窗口是真实存在的）。从复查处触发时，第二次扫描的回包先于快照落地，
// 于是 state.orphans 在"快照冻结 targets 之后、DELETE 之前"被换掉 —— 正是那个 bug。
const h = createHarness({ execHandler: cmd => {
  captured.push(cmd);
  if (onSnapshot && cmd.includes('EXISTS') && cmd.includes('__SQL_OK__')) { const f = onSnapshot; onSnapshot = null; f(); }
  // 复查：正常吐 __SQL_OK__（sqlFile 凭它判 ok）；failRecheck 注入读失败形态（无输出）
  if (cmd.includes('EXISTS') && cmd.includes('__SQL_OK__')) return failRecheck ? '' : '__SQL_OK__';
  // 桩模拟 sqlite3 的行为：SQL 里出现翻倍引号特征 → 语法错 → 零输出
  //（不能拿裸 "''" 判定：shq 的 '\'' 转义天然含相邻两个单引号）
  if (cmd.includes('.mode insert kv')) {
    return (cmd.includes("''customModels''") || cmd.includes("LIKE ''")) ? '' : 'snap-ok';
  }
  if (cmd.includes('DELETE FROM kv') && cmd.includes('__SQL_OK__')) return '__SQL_OK__';  // 删除成功
  if (cmd.includes('providerNodes')) return sqlFixture + '\n__SQL_OK__';                   // 扫描（sqlFile 凭标记判 ok）
  return '';
}});

const LIVE_NODE = 'openai-compatible-chat-069fdcc2-f29b-41da-b3b9-11f4702667ac';
const ORPHAN = 'openai-compatible-chat-ce065e91-ca59-4649-af61-5b93cf02c186';

test('健康扫描必须给出孤儿列表（planSteps 重构曾让它永远报警告）', async () => {
  // 节点/连接都在（live），kv 里有一个已删节点的 UUID 别名（孤儿）
  sqlFixture = [
    LIVE_NODE,                                  // providerNodes.id
    'codebuddy-cn',                             // providerConnections.provider
    LIVE_NODE,                                  // providerConnections.provider（UUID 形）
    LIVE_NODE + '|gpt-x|llm',                   // kv customModels（存活别名）
    ORPHAN + '|ghost|llm'                       // kv customModels（孤儿别名）
  ].join('\n');
  failRecheck = false;
  const els = h.loadApp();
  await els.get('btn-scan').onclick();

  const box = els.get('orphan-list').innerHTML;
  assert.ok(!box.includes(WARN),
    `扫描数据健康却显示「${WARN}」——scan 门禁之后的 recheck 门禁没有 fact，planSteps 永远拦截`);
  assert.ok(box.includes(ORPHAN), '孤儿别名没有被列出来');
  const btn = els.get('btn-clean-orphans');
  assert.strictEqual(btn.disabled, false, '清理按钮应该可用');
  assert.ok(btn.textContent.includes('1'), '清理按钮应显示 1 项');
});

test('不可信扫描（有别名但无节点/连接）必须仍然中止判定（2026-09-25 误删护栏）', async () => {
  // 只有 kv 别名，读不到任何节点/连接 —— 结果不可信，绝不判定
  sqlFixture = [ORPHAN + '|ghost|llm'].join('\n');
  failRecheck = false;
  const els = h.loadApp();
  await els.get('btn-scan').onclick();

  const box = els.get('orphan-list').innerHTML;
  assert.ok(box.includes(WARN), '不可信扫描必须显示警告并中止判定');
  assert.strictEqual(els.get('btn-clean-orphans').disabled, true, '不可信扫描下清理按钮必须禁用');
});

test('清理链路必须走通：快照 SQL 不得翻倍引号（曾致 sqlite3 语法错 → 永远"快照失败"）', async () => {
  sqlFixture = [LIVE_NODE, LIVE_NODE, ORPHAN + '|ghost|llm'].join('\n');
  failRecheck = false;
  captured = [];
  const els = h.loadApp();
  await els.get('btn-scan').onclick();
  await els.get('btn-clean-orphans').onclick();

  const snapCmd = captured.find(c => c.includes('.mode insert kv'));
  assert.ok(snapCmd, '清理必须先发出快照命令');
  // 事故特征标记（shq 的 '\'' 转义本身含相邻 ''，不能用裸 '' 判定）：
  assert.ok(!snapCmd.includes("''customModels''") && !snapCmd.includes("LIKE ''"),
    '快照 SQL 含翻倍引号 —— sqlite3 会直接语法错，快照永远失败（真机 2026-09-28 事故）');
  assert.ok(snapCmd.includes('customModels') && snapCmd.includes('disabledModels')
    && snapCmd.includes(ORPHAN),
    '快照 SQL 必须覆盖两个 scope 与被判定的孤儿别名');

  const delCmd = captured.find(c => c.includes('DELETE FROM kv') && c.includes('__SQL_OK__'));
  assert.ok(delCmd, '快照通过后必须发出 DELETE（否则清理没有真实执行）');
  assert.ok(delCmd.includes(ORPHAN), 'DELETE 必须针对被判定的孤儿别名');

  assert.ok(els.get('toast').textContent.includes('已一次性清理 1 项'),
    '清理成功必须有明确回执');
});

test('复查读失败必须中止删除（C2：读失败不得冒充"查无存活"）', async () => {
  sqlFixture = [LIVE_NODE, LIVE_NODE, ORPHAN + '|ghost|llm'].join('\n');
  failRecheck = true;   // 注入：复查读失败（sqlite3 无输出，sqlFile 重试后 ok:false）
  captured = [];
  const els = h.loadApp();
  await els.get('btn-scan').onclick();
  await els.get('btn-clean-orphans').onclick();
  assert.ok(els.get('toast').textContent.includes('复查读取失败'),
    '复查读失败必须显式中止，不得当"查无存活"放行删除');
  assert.ok(!captured.some(c => c.includes('DELETE FROM kv')),
    '复查读失败时 DELETE 不可达');
});

// I5 的残留（2026-09-30 架构扫描发现，非新问题）：
// 快照用**冻结的 targets**（对 ✓），删除却用 **state.orphans**（可被改写 ✗）。
// 「检查孤儿数据」按钮在清理期间没有被禁用 → 在快照的 await 窗口里点它，state.orphans
// 会被换成另一批 → DELETE 与快照**不是同一集合**：本批漏删、另一批没有快照却被删，
// 界面还照样报"✅ 已一次性清理 N 项"（谎报成功）；极端下批次变空 → DELETE 变成
// `... AND ()` 语法错，仍然报成功。ORPHAN_CLEAN_PLAN 的核心不变量就此被击穿。
test('快照在途时发生并发扫描：DELETE 必须仍等于快照的那一批（I5 残留）', async () => {
  const OTHER = 'openai-compatible-chat-b5b35395-3025-4810-90cd-5473475261b8';
  sqlFixture = [LIVE_NODE, LIVE_NODE, ORPHAN + '|ghost|llm'].join('\n');
  failRecheck = false;
  captured = [];
  const els = h.loadApp();
  await els.get('btn-scan').onclick();

  // 快照执行期间，用户又点了一次「检查孤儿数据」→ 本批换成 OTHER
  onSnapshot = () => {
    sqlFixture = [LIVE_NODE, LIVE_NODE, OTHER + '|other|llm'].join('\n');
    els.get('btn-scan').onclick();          // 不 await：模拟并发
  };
  await els.get('btn-clean-orphans').onclick();
  for (let i = 0; i < 6; i++) await new Promise(r => setTimeout(r, 0));

  const snapCmd = captured.find(c => c.includes('.mode insert kv'));
  const delCmd = captured.find(c => c.includes('DELETE FROM kv') && c.includes('__SQL_OK__'));
  assert.ok(snapCmd && delCmd, '快照与删除两个命令都必须发出');
  assert.ok(snapCmd.includes(ORPHAN), '快照针对的是本批 A');
  assert.ok(delCmd.includes(ORPHAN),
    'DELETE 必须删本批 A —— 换成 B 就是"快照与删除不是同一集合"（B 无快照可回滚、A 漏删）');
  assert.ok(!delCmd.includes(OTHER), 'DELETE 不得包含未经快照的 B');
});
