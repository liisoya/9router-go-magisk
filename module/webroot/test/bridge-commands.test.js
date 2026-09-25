/* bridge.js 命名操作层命令构造器离线测试 —— 纯函数，无设备依赖。
 * 运行：node --test module/webroot/test/
 * 对应的历史 bug：内联 heredoc 内容撞定界符、strip 单引号破坏 URL、
 * 路径/URL 未转义、mksh 环境（设备端）引号语义差异。
 */
'use strict';
const { test } = require('node:test');
const assert = require('node:assert');

global.window = { CFG: { MODDIR: '/data/adb/modules/ninerouter-go', DATA_DIR: '/data/adb/9router-go' } };
const KB = require('../bridge.js');
const C = KB._cmds;

// ── 引号转义（唯一出口：shq，经构造器输出验证）──
test('readFile：路径单引号包裹', () => {
  assert.strictEqual(C.readFile('/data/x'), "cat '/data/x' 2>/dev/null");
});
test("shq：单引号转义为 '\\' ''（经 readFile 输出验证）", () => {
  assert.strictEqual(C.readFile("/x/y'z"), "cat '/x/y'\\''z' 2>/dev/null");
});

// ── writeFile：内容走 base64 通道，任意字符安全 ──
test('writeFile：临时文件原子落盘，原文不出现在命令里', () => {
  const cmd = C.writeFile('/data/adb/9router-go/port', "20130'\n__EOF__");
  assert.ok(cmd.startsWith("printf '%s' '"));
  assert.ok(cmd.includes("| base64 -d > '/data/adb/9router-go/port'.tmp && mv '/data/adb/9router-go/port'.tmp '/data/adb/9router-go/port'"));
  assert.ok(!cmd.includes('__EOF__'), 'base64 后不含定界符');
  assert.ok(!cmd.includes("'\\n'"), '换行不在命令字面量中');
});
test('b64Decode：ASCII 与中文（UTF-8）往返', () => {
  assert.strictEqual(KB.b64Decode('bmFtZXNlcnZlciAyMjMuNS41LjU='), 'nameserver 223.5.5.5');
  assert.strictEqual(KB.b64Decode('5Lit'), '中');
  assert.strictEqual(KB.b64Decode(''), '');
  assert.strictEqual(KB.b64Decode('!!!bad!!!'), '');
});

// ── appendLine：换行折叠，完整保留单引号（曾 strip 引号破坏 URL）──
test('appendLine：单引号不丢失、换行折叠为空格', () => {
  const cmd = C.appendLine('/d/accel-list.conf', "https://a'b/x/");
  assert.ok(cmd.includes("https://a'\\''b/x/"), '单引号经 shq 安全转义');
  assert.ok(cmd.endsWith(">> '/d/accel-list.conf'"));
  const folded = C.appendLine('/d/f', 'a\nb');
  assert.ok(!/\n/.test(folded.replace(/^printf '[^']*' '/, '').replace(/' >>.*/, '')) || folded.includes("'a b'"), '换行折叠为空格');
});

// ── 备份/恢复 ──
test('backupOnce：dst 存在即跳过，结尾 ; true 吞掉退出码', () => {
  const cmd = C.backupOnce('/d/up', '/d/up.initial');
  assert.strictEqual(cmd, "[ -f '/d/up' ] && [ ! -f '/d/up.initial' ] && cp '/d/up' '/d/up.initial'; true");
});
test('restoreBackup：ok/none 语义', () => {
  assert.strictEqual(C.restoreBackup('/d/a', '/d/b'),
    "[ -f '/d/a' ] && cp '/d/a' '/d/b' && echo ok || echo none");
});

// ── 网络 ──
test('curlTiming：-w 格式与 URL 转义', () => {
  const cmd = C.curlTiming('https://x/?a=1&b=2');
  assert.ok(cmd.startsWith("curl -o /dev/null -s -m 8 -w '%{http_code} %{time_total}' '"));
  assert.ok(cmd.endsWith("?a=1&b=2'"));
});
test('download：dl-ok 哨兵与超时透传', () => {
  const cmd = C.download('https://gh/x', '/data/local/tmp/f.new', 300);
  assert.ok(cmd.includes("-m 300 -o '/data/local/tmp/f.new' 'https://gh/x' && echo dl-ok"));
});
test('fetch / sha256 / zipList：命令形状', () => {
  assert.strictEqual(C.fetch('https://x', 20), "curl -s -m 20 'https://x'");
  assert.strictEqual(C.sha256('/tmp/f'), "sha256sum '/tmp/f'");
  assert.strictEqual(C.zipList('/tmp/m.zip'), "unzip -l '/tmp/m.zip'");
});

// ── 模块专属 ──
test('probeDns：MODDIR 来自 CFG，-P -j 8', () => {
  assert.strictEqual(C.probeDns('/d/dns-candidates.tmp'),
    "/data/adb/modules/ninerouter-go/bin/dnsfwd -f '/d/dns-candidates.tmp' -P -j 8 2>&1");
});
test('sqlSnapshot：mkdir + .mode insert + 重定向', () => {
  const cmd = C.sqlSnapshot("SELECT * FROM kv WHERE key='a';", '/data/adb/9router-go/backups/snap.sql');
  assert.ok(cmd.startsWith("mkdir -p '/data/adb/9router-go/backups'; "));
  assert.ok(cmd.includes(".mode insert kv"));
  assert.ok(cmd.includes("> '/data/adb/9router-go/backups/snap.sql'"));
  // SQL 整体过 shq：内含单引号被转义为 '\''
  assert.ok(cmd.includes("SELECT * FROM kv WHERE key='\\''a'\\'';"));
});

// ── promise 降级形态包裹（Phase 4：多行安全收敛在 bridge 一层）──
test('promiseWrap：多行命令整体 base64 收敛为单行', () => {
  const cmd = C.promiseWrap("cat > /d/cc.sql <<'__EOSQL__'\nSELECT 1;\n__EOSQL__\nsqlite3 db < /d/cc.sql");
  assert.ok(cmd.startsWith('{ '), '复合命令包裹');
  assert.ok(cmd.includes("\n} 2>/dev/null | base64 | tr -d '\n'"), 'base64 管道收敛为单行');
  assert.ok(cmd.includes("SELECT 1;"), '内部命令原样保留');
});
