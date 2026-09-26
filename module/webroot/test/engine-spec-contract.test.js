/* 契约门禁：「什么算一个引擎」这条规则，两侧必须逐字一致（架构候选 5）
 *
 * 现状（ADR-0007）：判据在 `parsers.js`（`engineFileGate`：体积 ≥ 5MB 且文件头 7f454c46），
 * 执行前再核一遍在 `ops.sh`（`engine_src_ok`）。这是「判据 vs 执行」的合理分工 —— 不是问题；
 * 问题在于**同一条规则被抄了两遍**：常量两种写法（`5 * 1024 * 1024` 与 `5242880`）、
 * 魔数是裸字面量，两边只靠一句注释「与 parsers.js 对齐」维系。改一边忘另一边不会报错，
 * 只会让"前端放行 / 后端拒绝"（或反过来）在真机上表现为诡异行为。
 *
 * 本门禁把两侧缝死：常量、魔数、检查项的存在性、以及边界语义（含等号）都必须一致。
 * 运行：node --test module/webroot/test/
 */
'use strict';
const { test } = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const KP = require('../parsers.js');
const OPS = fs.readFileSync(path.join(__dirname, '..', '..', 'lib', 'ops.sh'), 'utf8');

// 从 ops.sh 抽常量（顶层 `ENGINE_MIN_BYTES=<数字>`）
const opsMinBytes = Number((OPS.match(/^ENGINE_MIN_BYTES=(\d+)/m) || [])[1]);

// 从 ops.sh 抽"像不像一个引擎"这个函数的函数体 —— 只认这个执行点，不扫全文件
function engineSrcOkBody(src) {
  const m = src.match(/(^|\n)engine_src_ok\(\)\s*\{([\s\S]*?)\n\}/m);
  assert.ok(m, '找不到 engine_src_ok()（改名了吗？本门禁需要同步）');
  return m[2];
}
const BODY = engineSrcOkBody(OPS);

test('体积下限：ops.sh 与 parsers.js 的常量必须相等（两种写法也不许漂）', () => {
  assert.ok(Number.isFinite(opsMinBytes), 'ops.sh 里没找到 ENGINE_MIN_BYTES=<数字>');
  assert.strictEqual(opsMinBytes, KP.ENGINE_MIN_BYTES,
    `ops.sh ${opsMinBytes} ≠ parsers.js ${KP.ENGINE_MIN_BYTES} —— 会出现"前端放行、后端拒绝"`);
  assert.strictEqual(KP.ENGINE_MIN_BYTES, 5 * 1024 * 1024, '下限必须是 5MB（真实产物约 25MB）');
});

test('ELF 魔数：ops.sh 里的字面量与 parsers.js 的常量必须逐字相同', () => {
  const m = BODY.match(/=\s*"([0-9a-f]{8})"/);
  assert.ok(m, 'engine_src_ok 里没找到 8 位十六进制魔数比较（od 输出形态）');
  assert.strictEqual(m[1], KP.ELF_MAGIC, `ops.sh "${m[1]}" ≠ parsers.js "${KP.ELF_MAGIC}"`);
  assert.match(KP.ELF_MAGIC, /^7f454c46$/, 'ELF 头必须是 7f 45 4c 46');
});

test('两侧都真的做了两项检查（删掉任一项必须红）', () => {
  assert.ok(BODY.includes('-ge "$ENGINE_MIN_BYTES"'), 'engine_src_ok 少了体积检查');
  assert.ok(/head -c 4/.test(BODY) && /od -An -tx1/.test(BODY), 'engine_src_ok 少了魔数检查');
  // parsers.js 侧：直接问它，不读源码
  assert.strictEqual(KP.engineFileGate(KP.ENGINE_MIN_BYTES + 1, '3c68746d').ok, false, 'HTML 头必须拒');
  assert.strictEqual(KP.engineFileGate(9, KP.ELF_MAGIC).ok, false, '9 字节（404 正文）必须拒');
});

test('边界语义一致：恰好等于下限时两侧都放行（ops.sh 用 -ge，parsers.js 用 <）', () => {
  assert.strictEqual(KP.engineFileGate(KP.ENGINE_MIN_BYTES, KP.ELF_MAGIC).ok, true);
  assert.strictEqual(KP.engineFileGate(KP.ENGINE_MIN_BYTES - 1, KP.ELF_MAGIC).ok, false);
  assert.ok(BODY.includes('-ge "$ENGINE_MIN_BYTES"'), 'ops.sh 必须用 -ge（含等号）');
});

test('od 输出形态与 parsers.js 的比较口径一致（8 位小写、无分隔）', () => {
  assert.match(KP.ELF_MAGIC, /^[0-9a-f]{8}$/);
  assert.ok(/tr -d '\[:space:\]'/.test(BODY), 'ops.sh 必须去掉 od 的空白，否则比较永远不成立');
  assert.strictEqual(KP.ELF_MAGIC, KP.ELF_MAGIC.toLowerCase(), 'parsers.js 侧比较前会 toLowerCase');
});
