/* 契约门禁：shell 输出的 key=value 键 ↔ WebUI 消费的键
 *
 * 为什么有这条门禁（C2）：`ops.sh status/panel` 的接口是"一行 20+ 个 key=value"，
 * 而键名契约原先**四处手抄**（shell emit / JS parse / JS consume / 测试 fixture），
 * 没有机械断言保证一致。漂移是静默的：`st.watchdog_state` 这种错拼只会得到 undefined，
 * 界面安静地空着（历史上 `engine_version` 显示"未知"、`factory_key` 恒 0 都是这一类）。
 *
 * 做法：**源码就是契约的单一来源** —— 从 shell 的 emit 模板里抽出键集合，
 * 从面板脚本抽"被消费的键"，断言两者一致（消费的必须被 emit；emit 的要么被消费、
 * 要么登记在"仅供信息/内部"清单里）。不需要额外维护一份手写键表。
 *
 * 运行：node --test module/webroot/test/
 */
'use strict';
const { test } = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');
const { scriptFiles, WEBROOT } = require('./lib/app-harness.js');

const LIB = path.join(__dirname, '..', '..', 'lib');
const shell = ['ops.sh', 'lifecycle.sh']
  .map(f => fs.readFileSync(path.join(LIB, f), 'utf8')).join('\n');

// ── 动作回执词契约：KP.ACTION_WORDS（JS 消费侧）↔ shell echo（emit 侧）──
// runOpsAction 是"缺省拒绝"消费：表里的词 shell 不吐了 = 用户会看到假失败；shell 加了
// 新回词而表没认 = 幂等/诚实分支被判失败（2026-10-01 dnsOn 两起事故面）。
// 抽取模式沿用上面的 shellEmitKeys / shellStateWords：只认动词函数体，不扫全文件。
const VERB_EMITTERS = {
  'start-user': ['life_start_user', 'life_settle_report'],
  'restart-engine': ['life_restart_engine', 'life_settle_report'],
  'stop-user': ['life_stop_user'],
  'enable-dns': ['life_ensure_dns'],
  'stop-dns': null,             // 回词由 ops.sh 分派行内联 echo（见断言内的特例）
  'install-engine': ['cmd_install_engine'],
  'install-module': ['cmd_install_module']
};
function verbWords(fn) {
  const m = shell.match(new RegExp('(^|\\n)' + fn + '\\(\\)\\s*\\{([\\s\\S]*?)\\n\\}', 'm'));
  assert.ok(m, `找不到动词函数 ${fn}()（改名了吗？本门禁需要同步）`);
  return [...m[2].matchAll(/echo\s+"?([a-z][a-z0-9-]*(?:=[a-z0-9-]+)?)"?/g)].map(x => x[1]);
}
// 面板是多文件、清单唯一来源 = index.html 的 <script src>：键消费必须扫**全部**脚本。
// 按文件名写死会漏掉新增的页面文件 —— 那些页面读的键就不再受契约约束（静默失覆盖）。
const app = scriptFiles()
  .map(f => fs.readFileSync(path.join(WEBROOT, f), 'utf8')).join('\n');
const KP = require('../parsers.js');

// ── shell 侧：只取"对外 payload 的 emit 函数"体里的 key= 记号 ──
// （不是全文件扫：`echo "engine=up"` 这类状态词不是 payload 键）
const EMITTERS = ['cmd_status', 'cmd_panel', 'life_state'];
function shellEmitKeys(src) {
  const keys = new Set();
  for (const fn of EMITTERS) {
    const m = src.match(new RegExp('(^|\\n)' + fn + '\\(\\)\\s*\\{([\\s\\S]*?)\\n\\}', 'm'));
    assert.ok(m, `找不到 emit 函数 ${fn}()（改名了吗？本门禁需要同步）`);
    // 只认 echo 行**双引号模板段**里的 `key=`：函数体里的局部变量赋值（`_ep=$(...)`）
    // 与 SQL 文本（`key='...'`）都不是 payload 键，必须排除
    for (const line of m[2].split('\n')) {
      if (!/^\s*echo\s/.test(line)) continue;
      for (const q of line.matchAll(/"([^"]*)"/g)) {
        for (const km of q[1].matchAll(/([a-z_][a-z0-9_]*)=/g)) keys.add(km[1]);
      }
    }
  }
  return keys;
}

// ── JS 侧：app.js 消费的键（payload 对象在 app.js 里叫 st）──
function appConsumedKeys(src) {
  const keys = new Set();
  for (const m of src.matchAll(/\bst\.([a-z_][a-z0-9_]*)/g)) keys.add(m[1]);
  for (const m of src.matchAll(/\bst\[['"]([a-z_][a-z0-9_]*)['"]\]/g)) keys.add(m[1]);
  return keys;
}

// 允许"emit 了但 app.js 不直接读"的键：**每个都要写清理由**（这是人工维护的唯一一处，
// 所以门槛设得高：能删的就删，能读的就读，只有"给人读的运维接口"才留）
const EMIT_ONLY = new Set([
  'factory_key',    // 出厂 key 卡片已按 Phase 12 移除（用户无感化）；保留是因为 status 是
                    // 人读的运维接口，且 Phase 0.2 的"读失败必须显式 err 不许冒充 0"判据在它身上
  'apikeys_total',  // 同上（与 factory_key 同一次 sqlite 调用读取，成对保留）
  'bind',           // DNS 绑定范围：绑定范围 UI 已删除（FIXPLAN 3.3 deletion test），仅作状态展示
  'mem_total',      // 2026-09-30：概览的「资源占用」卡片取消（内存并入运行状态读数牌），
  'mem_avail',      // 系统可用/总量不再显示 —— 但这两个字段是 OOM 排查的第一手材料，
                    // 且 cmd_panel 是人读的运维接口（`su -c '.../ops.sh panel'`），故保留 emit。
                    // 要彻底删：连 ops.sh 里 _mem 的读取一起去掉（那属于 shell 变更，需同步台账）
]);

const emitted = shellEmitKeys(shell);
const consumed = appConsumedKeys(app);

test('shell emit 的键集合：非空且包含三态与版本链（防止 emit 函数被改空）', () => {
  for (const k of ['port', 'engine', 'engine_pid', 'dns', 'dns_pid',
                   'watchdog', 'watchdog_pid', 'module_version', 'versioncode', 'engine_version']) {
    assert.ok(emitted.has(k), `缺键 ${k}`);
  }
});

test('WebUI 消费的每个键都必须被 shell emit（错拼/漏 emit = 界面静默空白）', () => {
  const missing = [...consumed].filter(k => !emitted.has(k)).sort();
  assert.deepStrictEqual(missing, [],
    `面板脚本读了 shell 没输出的键：${missing.join(', ')}（要么补 emit，要么改面板脚本）`);
});

test('shell emit 的每个键要么被 WebUI 消费、要么在 EMIT_ONLY 里有理由', () => {
  const unused = [...emitted].filter(k => !consumed.has(k) && !EMIT_ONLY.has(k)).sort();
  assert.deepStrictEqual(unused, [],
    `emit 了但没人读的键：${unused.join(', ')}（删掉，或加进 EMIT_ONLY 并写理由）`);
});

test('动作回执词：ACTION_WORDS 认下的每个词都必须真的被 shell 吐出来', () => {
  for (const [verb, w] of Object.entries(KP.ACTION_WORDS)) {
    const emitters = VERB_EMITTERS[verb];
    assert.ok(emitters !== undefined, `未知动词 ${verb}（VERB_EMITTERS 需要同步）`);
    for (const word of w.ok) {
      // stop-dns 特例：回词由 ops.sh 分派行内联 echo（`stop-dns) life_disable_dns; echo "stopped"`），
      // 没有独立函数体可扫 —— 全文存在性检查保底（弱一点，但 shell 源就这两个文件）
      const hit = emitters === null
        ? shell.includes(`echo "${word}"`)
        : emitters.some(f => verbWords(f).includes(word));
      assert.ok(hit, `${verb} 期待回词「${word}」，但它的 emit 方没有吐过这个词（shell 改词了？ACTION_WORDS 需要同步）`);
    }
  }
});

// ── 值枚举契约：状态词（life_state 是唯一 emit 方 ↔ KP.LIFECYCLE_STATES 是唯一文案映射）──
// 键契约只绑"键"；状态词（up/down/stopped/disabled/yielded/stale）此前在 shell 与 app.js 各写一遍，
// 加一个意图态时 app.js 会静默落到 else 显示"未运行"。这里把两侧双向缝死。
const VAR_TO_KIND = { dns: 'dns', eng: 'engine', wd: 'watchdog' };

// 从 life_state() 函数体里抽 `_dns=up` 这类赋值 —— 只认这个 emit 方，
// 不扫全文件（`cmd_install_engine` 里的 `echo "engine=up"` 是安装结果信号，不是状态词表）
function shellStateWords(src) {
  const m = src.match(/(^|\n)life_state\(\)\s*\{([\s\S]*?)\n\}/m);
  assert.ok(m, '找不到 life_state()（改名了吗？本门禁需要同步）');
  const out = {};
  for (const am of m[2].matchAll(/_(dns|eng|wd)=([a-z_]+)/g)) {
    const kind = VAR_TO_KIND[am[1]];
    if (!out[kind]) out[kind] = new Set();
    out[kind].add(am[2]);
  }
  return out;
}

const emittedStates = shellStateWords(shell);

test('life_state 实际 emit 的每个状态词都在 JS 映射表里（新增意图态 → 必须同步文案）', () => {
  for (const [kind, words] of Object.entries(emittedStates)) {
    for (const w of words) {
      assert.ok(KP.LIFECYCLE_STATES[kind] && KP.LIFECYCLE_STATES[kind][w],
        `shell 会 emit ${kind}=${w}，但 KP.LIFECYCLE_STATES.${kind} 没有它（界面会显示"未知状态"）`);
    }
  }
});

test('JS 映射表里没有 shell 不会 emit 的幽灵词（否则那段文案永远不会出现）', () => {
  for (const [kind, table] of Object.entries(KP.LIFECYCLE_STATES)) {
    const emitted = emittedStates[kind] || new Set();
    const ghosts = Object.keys(table).filter(w => !emitted.has(w)).sort();
    assert.deepStrictEqual(ghosts, [],
      `KP.LIFECYCLE_STATES.${kind} 有幽灵词：${ghosts.join(', ')}（删掉，或让 life_state 真的会 emit）`);
  }
});

test('三个状态的词表都在（防止误删整块后门禁变成空转）', () => {
  for (const kind of ['dns', 'engine', 'watchdog']) {
    assert.ok(KP.LIFECYCLE_STATES[kind], `缺 ${kind} 词表`);
    assert.ok(Object.keys(KP.LIFECYCLE_STATES[kind]).length > 0, `${kind} 词表是空的`);
  }
});
