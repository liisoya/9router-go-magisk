/* bridge.js 命名操作层命令构造器离线测试 —— 纯函数，无设备依赖。
 * 运行：node --test module/webroot/test/
 * 对应的历史 bug：内联 heredoc 内容撞定界符、strip 单引号破坏 URL、
 * 路径/URL 未转义、mksh 环境（设备端）引号语义差异。
 */
'use strict';
const { test } = require('node:test');
const assert = require('node:assert');

global.window = { CFG: { MODDIR: '/data/adb/modules/ninerouter-go', DATA_DIR: '/data/adb/9router-go' } };
// 形态缓存命中 cb3 → 不触发探测；ksu.exec 由本测试伪造（离线可测 bridge 的执行层）
global.localStorage = { getItem: k => (k === '__kmod_exec_mode' ? 'cb3' : null), setItem() {}, removeItem() {} };
global.ksu = {
  exec(cmd, opts, cb) {
    // 伪造真机行为：命令自己吐自报标记才算成功；否则只有哨兵（旧实现下 sqlite 报错的形态）
    const marker = ['snap-ok', 'write-ok', 'append-ok', '__SQL_OK__', '__READ_OK__'].find(m => cmd.includes(m));
    const out = marker ? marker + '\n' : '';
    setTimeout(() => global.window[cb](out + '__KMOD_DONE__0'), 0);
  }
};
const KB = require('../bridge.js');
const C = KB._cmds;

// ── 引号转义（唯一出口：shq，经构造器输出验证）──
test('readFile：路径单引号包裹 + 自报 read-ok 标记（读失败与空内容可辨）', () => {
  assert.strictEqual(C.readFile('/data/x'), "cat '/data/x' 2>/dev/null && echo __READ_OK__");
});
test("shq：单引号转义为 '\\' ''（经 readFile 输出验证）", () => {
  assert.strictEqual(C.readFile("/x/y'z"), "cat '/x/y'\\''z' 2>/dev/null && echo __READ_OK__");
});
test('readFile 运行层：无标记 = 读失败（ok:false），标记被剥离不进内容', async () => {
  // 直通桩：回显"命令本体"（剥掉调用方追加的哨兵尾巴）—— 覆写构造器的输出可精确控制
  const origExec = global.ksu.exec;
  global.ksu.exec = (cmd, opts, cb) => {
    const body = cmd.replace(/; echo __KMOD_DONE__\$\?$/, '');
    setTimeout(() => global.window[cb](body + '\n__KMOD_DONE__0'), 0);
  };
  const orig = C.readFile;
  try {
    C.readFile = () => 'true';                    // 旧失败形态：无输出
    const bad = await KB.readFile('/d/f');
    assert.strictEqual(bad.ok, false);
    C.readFile = () => 'hello\n__READ_OK__';      // 成功形态
    const good = await KB.readFile('/d/f');
    assert.strictEqual(good.ok, true);
    assert.strictEqual(good.out, 'hello');        // 标记连同换行被剥掉
  } finally {
    global.ksu.exec = origExec;
    C.readFile = orig;
  }
});
test('ops 子命令逐 token 过 shq（ver/路径不再裸拼）', () => {
  assert.strictEqual(
    C.ops('install-engine /data/local/tmp/9r-eng.new 1.9.4'),
    "'/data/adb/modules/ninerouter-go/lib/ops.sh' 'install-engine' '/data/local/tmp/9r-eng.new' '1.9.4'");
});
test('dbOps：孤儿 SQL 形状（不翻倍引号；别名进入 LIKE/EXISTS）', () => {
  const a = 'openai-compatible-chat-069fdcc2-f29b-41da-b3b9-11f4702667ac';
  const snap = C.orphanSnapshotSql([a]);
  assert.ok(snap.includes("key LIKE '" + a + "|%'"), snap);
  assert.ok(!snap.includes("''"), '不得翻倍引号（2026-09-28 快照事故）');
  assert.ok(C.orphanDeleteSql([a]).includes(`DELETE FROM kv WHERE scope='customModels' AND (key LIKE '${a}|%')`));
  const re = C.recheckOrphansSql([a]);
  assert.ok(re.includes(`SELECT '${a}' WHERE EXISTS (SELECT 1 FROM providerNodes WHERE id='${a}')`), re);
  assert.ok(C.scanOrphansSql().includes("SELECT id FROM providerNodes;"));
  // 连接 provider 必须计入存活集合：只有连接（节点已删）的别名**仍然可路由**，
  // 若有人把这条 SELECT 删掉/收窄成"只认节点"，清理会误删仍能用的模型（2026-09-30 边界）。
  assert.ok(C.scanOrphansSql().includes("SELECT DISTINCT provider FROM providerConnections;"),
    '扫描 SQL 丢了连接 provider → 有连接无节点的模型会被误判成孤儿并删除');
  assert.ok(C.recheckOrphansSql([a]).includes("EXISTS (SELECT 1 FROM providerConnections WHERE provider='" + a + "')"),
    '复查 SQL 丢了连接判据 → 删前复查会放行误删');
  assert.ok(C.credScanSql().includes('FROM providerConnections WHERE isActive=1'));
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
  assert.ok(cmd.includes(">> '/d/accel-list.conf' && echo append-ok"), cmd);
  const folded = C.appendLine('/d/f', 'a\nb');
  assert.ok(!/\n/.test(folded.replace(/^printf '[^']*' '/, '').replace(/' >>.*/, '')) || folded.includes("'a b'"), '换行折叠为空格');
});

// ── writeFile / appendLine 自报成败（2026-09-28 审计）：promise 形态下 stderr 被丢弃、
// 退出码恒 0，旧实现 `return !r.err` 把设备上的写失败判成成功 —— 面板谎报"已保存"。
test('writeFile / appendLine 命令自吐成败标记', () => {
  assert.ok(C.writeFile('/d/f', 'x').endsWith("&& echo write-ok"), C.writeFile('/d/f', 'x'));
  assert.ok(C.appendLine('/d/f', 'x').endsWith('&& echo append-ok'));
});
test('writeFile / appendLine 运行层只认标记（无标记 = 失败，不得谎报成功）', async () => {
  const orig = C.writeFile, origA = C.appendLine;
  C.writeFile = () => 'true';            // 旧实现失败形态：无输出
  C.appendLine = () => 'true';
  assert.strictEqual(await KB.writeFile('/d/f', 'x'), false);
  assert.strictEqual(await KB.appendLine('/d/f', 'x'), false);
  C.writeFile = orig; C.appendLine = origA;
  assert.strictEqual(await KB.writeFile('/d/f', 'x'), true);
  assert.strictEqual(await KB.appendLine('/d/f', 'x'), true);
});
test('sqlFile 命令自吐 __SQL_OK__（读失败凭 r.ok 可辨，空结果不再冒充成功）', async () => {
  let sent = '';
  const orig = global.ksu.exec;
  global.ksu.exec = (cmd, opts, cb) => { sent = cmd; return orig(cmd, opts, cb); };
  const r = await KB.sqlFile('SELECT 1;');
  global.ksu.exec = orig;
  assert.ok(sent.includes("< /data/adb/9router-go/cc.sql && echo __SQL_OK__"), sent);
  assert.strictEqual(r.ok, true, '命令自吐标记时 r.ok 必须为 true');
});

// ── 备份/恢复 ──
test('backupOnce：四种结局都有明确标记（不再是「永远 true」）', () => {
  const cmd = C.backupOnce('/d/up', '/d/up.initial');
  for (const marker of ['no-src', 'exists', 'ok', 'fail']) {
    assert.ok(cmd.includes(`echo ${marker}`), `缺标记 ${marker}：${cmd}`);
  }
  assert.ok(!cmd.trim().endsWith('; true'),
    '不允许再拿 `; true` 吞掉结果 —— 那会让"备份失败就不改写"的门禁永远通过');
  assert.ok(cmd.includes("cp '/d/up' '/d/up.initial'"), cmd);
});
test('restoreBackup：ok/none 语义', () => {
  assert.strictEqual(C.restoreBackup('/d/a', '/d/b'),
    "[ -f '/d/a' ] && cp '/d/a' '/d/b' && echo ok || echo none");
});

// ── 网络 ──
// 批量测速（A5）：原先是面板侧 15+ 次**串行** exec（最坏 15×8s ≈ 120s，且无一字进度）。
// 现在并发放在 **shell 内部**：单次 exec 里每节点一个后台子 shell、各自写自己的输出文件，
// wait 后统一 cat —— 绕开 bridge 的全局串行队列（并发 ksu.exec 在部分管理器上会串扰），
// 也就不需要"按宿主差异化放开并发"那套机制。
test('curlTimingBatch：并发在 shell 内部 + 每节点独立文件 + 索引写在行里', () => {
  const cmd = C.curlTimingBatch(['https://x/?a=1&b=2', 'https://y/'], 'b0');
  assert.ok(cmd.startsWith('mkdir -p '), '必须先建批量输出目录');
  assert.strictEqual((cmd.match(/ curl -o \/dev\/null/g) || []).length, 2, '每个 URL 一个 curl');
  assert.strictEqual((cmd.match(/2>\/dev\/null &/g) || []).length, 2,
    '每个 URL 一个后台任务（并发只在 shell 内部，不放开 exec 并发）');
  assert.ok(cmd.includes('wait; cat'), '必须 wait 后统一 cat');
  assert.ok(cmd.includes("b0-0.out'") && cmd.includes("b0-1.out'"),
    '每节点必须有独立输出文件（共用 stdout 会互相插队）');
  assert.ok(cmd.includes("'https://x/?a=1&b=2'"),
    '含 & 的 URL 必须被单引号完整包裹（曾因 strip 引号破坏含引号 URL）');
  assert.ok(cmd.includes('rm -rf'), '临时目录必须清理');
});
test('curlTimingBatch：-w 契约 + 连接超时（解析侧靠它提取 code/time/原因）', () => {
  const cmd = C.curlTimingBatch(['https://x/'], 'b5');
  assert.ok(cmd.includes("curl -o /dev/null -s -m 8 --connect-timeout 3 -w '%{http_code} %{time_total} %{errormsg}\\n'"),
    '-w 或超时参数变了 → parsers.parseCurlTimings 解析不出来（两端必须同时改）');
  assert.ok(cmd.includes('--connect-timeout 3'),
    '缺连接超时 → 挂死的节点会拖满 -m 8（真机实测：5.03s → 3.00s）');
});
test('download：dl-ok 哨兵与超时透传', () => {
  const cmd = C.download('https://gh/x', '/data/local/tmp/f.new', 300);
  assert.ok(cmd.includes("-m 300 -o '/data/local/tmp/f.new' 'https://gh/x' && echo dl-ok"));
});
// fetch 必须带 -L：GitHub release 资产地址是 302 跳转（真机实测：无 -L → 302 size=0），
// 不跟随重定向就拿不到 SHA256SUMS → 面板永远"取不到校验和"直接拒绝更新（2026-09-27）。
// 与 download 的取舍相反：这里**不加 -f**，404 正文要留给调用方展示。
test('fetch：必须带 -L（跟随重定向），且不带 -f（保留 404 正文供诊断）', () => {
  const cmd = C.fetch('https://x', 20);
  assert.ok(cmd.startsWith('curl -sL '), '缺 -L（--location）会在 302 的 release 资产地址上只拿到空正文');
  assert.strictEqual(cmd, "curl -sL -m 20 'https://x'");
  assert.ok(!/-f/.test(cmd), 'fetch 不该带 -f：404 正文是诊断信息，由调用方判断');
});
test('fetch / sha256 / zipList：命令形状', () => {
  assert.strictEqual(C.fetch('https://x'), "curl -sL -m 15 'https://x'");
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

// ── sqlSnapshot 的成败判据 ──
// 真机实证（2026-09-26）：`sqlite3 db ".mode insert kv" <坏SQL> > out` → `rc=1 size=0`，
// 而 `>` 已经把 0 字节文件建出来了；旧实现 `return !r.err`（exec 层错误才有 err，且不看 code）
// 判成"成功" → 界面放行删除，而 `backups/kv-before-orphan-clean-*.sql` 其实是空的（现场确有该文件）。
test('sqlSnapshot 命令自带成败判据：非空才算成功，失败删空文件', () => {
  const cmd = C.sqlSnapshot('SELECT * FROM kv;', '/d/backups/s.sql');
  assert.ok(cmd.includes('&& [ -s '), '必须有"文件非空"判据');
  assert.ok(cmd.includes('echo snap-ok') && cmd.includes('echo snap-fail'));
  assert.ok(cmd.includes("rm -f '/d/backups/s.sql'"), '失败必须删掉 0 字节文件');
});
test('sqlSnapshot()：shell 报失败时返回 false（不得凭 !r.err 假成功）', async () => {
  const orig = C.sqlSnapshot;
  C.sqlSnapshot = () => 'echo snap-fail';
  assert.strictEqual(await KB.sqlSnapshot('SELECT 1', '/d/s.sql'), false);
  C.sqlSnapshot = () => 'true'; // 旧实现的失败形态：命令无任何输出
  assert.strictEqual(await KB.sqlSnapshot('SELECT 1', '/d/s.sql'), false);
  C.sqlSnapshot = () => 'echo snap-ok';
  assert.strictEqual(await KB.sqlSnapshot('SELECT 1', '/d/s.sql'), true);
  C.sqlSnapshot = orig;
});

// ── promise 降级形态包裹（Phase 4：多行安全收敛在 bridge 一层）──
test('promiseWrap：多行命令整体 base64 收敛为单行', () => {
  const cmd = C.promiseWrap("cat > /d/cc.sql <<'__EOSQL__'\nSELECT 1;\n__EOSQL__\nsqlite3 db < /d/cc.sql");
  assert.ok(cmd.startsWith('{ '), '复合命令包裹');
  assert.ok(cmd.includes("\n} 2>/dev/null | base64 | tr -d '\n'"), 'base64 管道收敛为单行');
  assert.ok(cmd.includes("SELECT 1;"), '内部命令原样保留');
});

// ── 引擎更新：下载器必须对 HTTP 错误失败 + 装前门禁探针（2026-09-26 事故）──
// curl 没有 -f 时，404 也写正文并 echo dl-ok → 9 字节 "Not Found" 被当引擎装上。
test('download：必须带 -f（HTTP ≥400 即非零退出），路径与 URL 经 shq', () => {
  const cmd = C.download('https://x/y?q=1&z=2', '/data/local/tmp/9r-eng.new', 300);
  assert.ok(/-f/.test(cmd), '缺 -f 就会把 404 正文当成功');
  assert.ok(cmd.includes("curl -fsSL -m 300 -o '/data/local/tmp/9r-eng.new'"), cmd);
  assert.ok(cmd.trim().endsWith('&& echo dl-ok'), cmd);
});
test('download：没有 dl-ok（HTTP 失败形态）→ 运行层返回 false', async () => {
  const orig = C.download;
  C.download = () => 'echo nothing';
  assert.strictEqual(await KB.download('u', 'o', 1), false);
  C.download = orig;
});
test('fileSize / elfMagic：只读探针，输出收敛为单值供 engineFileGate 判', () => {
  assert.ok(C.fileSize('/d/a b').includes("wc -c < '/d/a b'"), C.fileSize('/d/a b'));
  const m = C.elfMagic('/d/engine');
  assert.ok(m.includes('head -c 4'), m);
  assert.ok(m.includes('od -An -tx1'), m);
});
