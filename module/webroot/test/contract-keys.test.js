/* 契约门禁：shell 输出的 key=value 键 ↔ WebUI 消费的键
 *
 * 为什么有这条门禁（C2）：`ops.sh status/panel` 的接口是"一行 20+ 个 key=value"，
 * 而键名契约原先**四处手抄**（shell emit / JS parse / JS consume / 测试 fixture），
 * 没有机械断言保证一致。漂移是静默的：`st.watchdog_state` 这种错拼只会得到 undefined，
 * 界面安静地空着（历史上 `engine_version` 显示"未知"、`factory_key` 恒 0 都是这一类）。
 *
 * 做法：**源码就是契约的单一来源** —— 从 shell 的 emit 模板里抽出键集合，
 * 从 app.js 抽"被消费的键"，断言两者一致（消费的必须被 emit；emit 的要么被消费、
 * 要么登记在"仅供信息/内部"清单里）。不需要额外维护一份手写键表。
 *
 * 运行：node --test module/webroot/test/
 */
'use strict';
const { test } = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const LIB = path.join(__dirname, '..', '..', 'lib');
const shell = ['ops.sh', 'lifecycle.sh']
  .map(f => fs.readFileSync(path.join(LIB, f), 'utf8')).join('\n');
const app = fs.readFileSync(path.join(__dirname, '..', 'app.js'), 'utf8');

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
    `app.js 读了 shell 没输出的键：${missing.join(', ')}（要么补 emit，要么改 app.js）`);
});

test('shell emit 的每个键要么被 WebUI 消费、要么在 EMIT_ONLY 里有理由', () => {
  const unused = [...emitted].filter(k => !consumed.has(k) && !EMIT_ONLY.has(k)).sort();
  assert.deepStrictEqual(unused, [],
    `emit 了但没人读的键：${unused.join(', ')}（删掉，或加进 EMIT_ONLY 并写理由）`);
});
