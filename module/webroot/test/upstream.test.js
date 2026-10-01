/* upstream.js 离线测试 —— 上游 release 地址契约（纯函数：无设备、无外网）。
 * 运行：node --test module/webroot/test/
 *
 * 本文件的地址类用例就是 2026-09-27 用户实测「下载并更新引擎必然失败」的复现：
 *   version.json 的 latestVersion 是裸版本号 "1.9.3"，而上游 release 的 tag 是 "v1.9.3"，
 *   面板直接把它当 tag 拼 URL → 404（加速节点回的是 9 字节 "Not Found"，
 *   也就是 2026-09-26 那桩「404 正文被当引擎装上」事故里的同一个东西）。
 * 修复前：releaseTag / engineAssetUrl / engineSumsUrl / 回潮扫描 这几条必红。
 *
 * 配套（同一条门禁的两半，缺一不可）：
 *   · bridge-commands.test.js —— fetch 必须带 -L（否则 302 的资产地址只剩空正文）
 *   · parsers.js checksumGate —— 摘要取不到即拒绝（fail-closed，本文件末条联动断言）
 *   · 真机 T13 —— 在设备上真的把这条链路走一遍（只读，不安装）
 */
'use strict';
const { test } = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');

const KU = require('../upstream.js');

// 上游真实地址（v1.9.3 = 当前最新，本机与真机都已实测可达）
const ARM64_V193 = 'https://github.com/luqman-v1/9router-go/releases/download/v1.9.3/9router-go-linux-arm64';
// 修复前 app.js 拼出来的那个地址（每次下载都 404 的元凶）：tag 缺 v
const ARM64_V193_BROKEN = 'https://github.com/luqman-v1/9router-go/releases/download/1.9.3/9router-go-linux-arm64';

// ── releaseTag：上游 tag 一律带 v，而版本清单给的是裸版本号 ──
test('releaseTag：裸版本号必须补 v（本次故障的全部原因）', () => {
  assert.strictEqual(KU.releaseTag('1.9.3'), 'v1.9.3');
  assert.strictEqual(KU.releaseTag('1.10.0'), 'v1.10.0');
});
test('releaseTag：已带 v 时幂等；空白/大小写/预发布后缀兜底', () => {
  assert.strictEqual(KU.releaseTag('v1.9.3'), 'v1.9.3');
  assert.strictEqual(KU.releaseTag('V1.9.3'), 'V1.9.3');
  assert.strictEqual(KU.releaseTag('  1.9.3  '), 'v1.9.3');
  assert.strictEqual(KU.releaseTag('v1.10.0-rc1'), 'v1.10.0-rc1');
});
test('releaseTag：空值返回空串（不拼出 "…/download//asset" 这种更难懂的 404）', () => {
  for (const bad of ['', '   ', null, undefined]) assert.strictEqual(KU.releaseTag(bad), '');
  assert.strictEqual(KU.engineAssetUrl(''), '');
  assert.strictEqual(KU.engineSumsUrl(''), '');
});

// ── 回归用例：地址必须落在上游真实存在的 tag 上 ──
test('engineAssetUrl：裸版本号也指向 v 标签的资产（修复前此条红）', () => {
  assert.strictEqual(KU.engineAssetUrl('1.9.3'), ARM64_V193);
  assert.notStrictEqual(KU.engineAssetUrl('1.9.3'), ARM64_V193_BROKEN,
    '又拼成了缺 v 的地址 —— 那正是「每次下载都 404」的原因');
});
test('engineSumsUrl：校验和与二进制必须同一个 tag（否则两道门禁永远对不上）', () => {
  assert.strictEqual(KU.engineSumsUrl('1.9.3'),
    'https://github.com/luqman-v1/9router-go/releases/download/v1.9.3/SHA256SUMS.txt');
  const dir = u => u.replace(/\/[^/]+$/, '');
  assert.strictEqual(dir(KU.engineSumsUrl('1.9.3')), dir(KU.engineAssetUrl('1.9.3')));
  assert.strictEqual(dir(KU.engineSumsUrl('v1.9.3')), dir(KU.engineAssetUrl('1.9.3')));
});
test('仓库与资产名：引擎资产只在引擎仓库（模块仓库的 release 里没有引擎二进制）', () => {
  assert.ok(KU.engineAssetUrl('1.9.3').includes(`/${KU.ENGINE_REPO}/`));
  assert.ok(!KU.engineAssetUrl('1.9.3').includes(KU.MODULE_REPO));
  assert.strictEqual(KU.ENGINE_ASSET, '9router-go-linux-arm64');
  assert.strictEqual(KU.SUMS_ASSET, 'SHA256SUMS.txt');
  // 清单地址落在各自仓库的 main 分支
  assert.ok(KU.ENGINE_VERSION_URL.includes(`/${KU.ENGINE_REPO}/`));
  assert.ok(KU.MOD_UPDATE_URL.includes(`/${KU.MODULE_REPO}/`));
});
// 项目地址（更新页那个链接）必须与更新源**同一个仓库**：主页给人看、update.json 给机器读，
// 两处指向不同仓库时，"点了项目地址看到的版本"和"检查更新拿到的版本"就会对不上。
test('PROJECT_URL：由仓库名派生，与模块更新源同仓库', () => {
  assert.strictEqual(KU.PROJECT_URL, `https://github.com/${KU.MODULE_REPO}`);
  assert.ok(KU.PROJECT_URL.includes(`/${KU.MODULE_REPO}`));
  assert.ok(KU.MOD_UPDATE_URL.includes(`/${KU.MODULE_REPO}/`));
});

// ── 加速前缀：只有 GitHub 域才加（自定义更新源可能根本不是 GitHub）──
test('withAccel：空前缀 = 直连；有前缀 = 原样拼接', () => {
  assert.strictEqual(KU.withAccel('https://github.com/a', ''), 'https://github.com/a');
  assert.strictEqual(KU.withAccel('https://github.com/a', '   '), 'https://github.com/a');
  assert.strictEqual(KU.withAccel('https://github.com/a', 'https://gh/'), 'https://gh/https://github.com/a');
});
test('withAccelIfGithub：自定义更新源不加前缀（曾无条件加前缀 → 自建源必然取不到）', () => {
  assert.strictEqual(KU.withAccelIfGithub('https://github.com/x/y', 'https://gh/'),
    'https://gh/https://github.com/x/y');
  assert.strictEqual(KU.withAccelIfGithub('https://raw.githubusercontent.com/x/y', 'https://gh/'),
    'https://gh/https://raw.githubusercontent.com/x/y');
  assert.strictEqual(KU.withAccelIfGithub('https://my.own.host/update.json', 'https://gh/'),
    'https://my.own.host/update.json');
  assert.strictEqual(KU.isGithubUrl('https://example.com/path/github.com/x'), false,
    '路径里出现 github.com 不算 GitHub 域');
});

// ── SHA256SUMS.txt 的期望摘要（fixture = v1.9.3 真实文件内容）──
const SUMS_V193 = [
  '491bddfa88f7cdc5b3fa6b0e04bf5ade59d37ef2b2c2a3aa6900f0e7718467bf  9router-go-linux-amd64',
  '8b52af39a88ff0660ee88c878781db5b416db174d38ecff7318fdf92e12c309e  9router-go-linux-arm64',
  '6c7562e4e4ebf675633cdf3cf6aa37f5cfa44d0d0a24f9ce48b5c62b11b1dcf9  9router-go-darwin-amd64',
  '7f88b1a1a8f35087bf9ce8448c2cf74f469420464402f7008122e04f5a497a7d  9router-go-darwin-arm64',
  '32daf9437521dd25123770f372b010a97eb2fcff5f9083cf29f7639dfc1c467d  9router-go-windows-amd64.exe',
  ''
].join('\n');
test('parseSumFor：按文件名整词取 arm64 摘要（不被 amd64 / darwin-arm64 行误命中）', () => {
  assert.strictEqual(KU.parseSumFor(SUMS_V193, KU.ENGINE_ASSET),
    '8b52af39a88ff0660ee88c878781db5b416db174d38ecff7318fdf92e12c309e');
});
test('parseSumFor：CRLF / 二进制模式星号 / 空与脏输入', () => {
  const want = '8b52af39a88ff0660ee88c878781db5b416db174d38ecff7318fdf92e12c309e';
  assert.strictEqual(KU.parseSumFor(SUMS_V193.replace(/\n/g, '\r\n'), KU.ENGINE_ASSET), want);
  assert.strictEqual(KU.parseSumFor(`${'a'.repeat(64)}  *${KU.ENGINE_ASSET}`, KU.ENGINE_ASSET), 'a'.repeat(64));
  assert.strictEqual(KU.parseSumFor('', KU.ENGINE_ASSET), '');
  assert.strictEqual(KU.parseSumFor('Not Found', KU.ENGINE_ASSET), '');
  assert.strictEqual(KU.parseSumFor(SUMS_V193, ''), '');
});
// 取不到 → 空串 → checksumGate 必须拒绝（跨文件联动：避免判据与提取各改各的）
test('取不到摘要时 checksumGate 必须拒绝（fail-closed 联动）', () => {
  const KP = require('../parsers.js');
  const actual = 'b'.repeat(64);
  assert.strictEqual(KP.checksumGate(KU.parseSumFor('Not Found', KU.ENGINE_ASSET), actual).ok, false);
  assert.strictEqual(KP.checksumGate(KU.parseSumFor(SUMS_V193, KU.ENGINE_ASSET), actual).ok, false);
  assert.strictEqual(
    KP.checksumGate(KU.parseSumFor(SUMS_V193, KU.ENGINE_ASSET),
      '8b52af39a88ff0660ee88c878781db5b416db174d38ecff7318fdf92e12c309e').ok, true);
});

// ── 回潮扫描：装配层不许再手写任何上游地址（地址契约只有一个所有者）──
// 要扫**全部面板脚本**（清单唯一来源 = index.html 的 <script src>）：只按文件名扫一个
// 文件时，拆成多文件后新增的页面就不在覆盖范围内（门禁静默失去覆盖）。
// upstream.js 是地址的所有者，排除它自己。
const { scriptFiles, WEBROOT } = require('./lib/app-harness.js');
const IDX = fs.readFileSync(path.join(WEBROOT, 'index.html'), 'utf8');
const APP = scriptFiles()
  .filter(f => f !== 'upstream.js')
  .map(f => fs.readFileSync(path.join(WEBROOT, f), 'utf8')).join('\n');
test('面板脚本不得再手写 release 地址 / 仓库字面量 / 清单 URL', () => {
  assert.ok(!APP.includes('releases/download/'),
    '又手写 release 地址了 —— 请走 KU.engineAssetUrl / engineSumsUrl');
  assert.ok(!/github\.com\/luqman-v1/.test(APP), '出现引擎仓库字面量（应为 KU.ENGINE_REPO）');
  assert.ok(!/raw\.githubusercontent\.com/.test(APP), '出现清单 URL 字面量（应为 KU.*_URL）');
  assert.ok(/KU\./.test(APP), '没有使用 KUpstream（地址契约的所有者）');
});
// ── 回潮扫描之二：upstream.js **拥有**的名字，面板脚本里必须带 `KU.` 前缀 ──
// 2026-09-27 真机事故：把常量收编进 upstream.js 时漏改了 renderPanel 里的一处裸引用
// （`st.mod_url || DEFAULT_MOD_UPDATE_URL`），面板每次刷新都抛 ReferenceError —— 纯函数
// 用例全绿也拦不住，因为它们不跑装配层。名字清单**从 upstream.js 的返回对象里现取**，
// 不手写：手写的清单自己就会漂，那正是这个仓库反复吃过亏的地方。
const UPSTREAM_SRC = fs.readFileSync(path.join(WEBROOT, 'upstream.js'), 'utf8');
function ownedNames(src) {
  const m = src.match(/return\s*\{([\s\S]*?)\};/);
  assert.ok(m, 'upstream.js 里找不到 return 对象（改名了？本门禁需要同步）');
  return m[1].split(/[,\s]+/).map(s => s.trim()).filter(s => /^[A-Za-z_$][\w$]*$/.test(s));
}
test('面板脚本引用 upstream.js 拥有的名字时必须带 KU. 前缀（不许裸引用）', () => {
  const names = ownedNames(UPSTREAM_SRC);
  assert.ok(names.length >= 10, `只从 upstream.js 解析出 ${names.length} 个名字，解析规则该更新了`);
  // 注释里写名字是正常的（解释为什么要带前缀），先剥掉注释再扫
  const code = APP.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/[^\n]*/g, '$1');
  const bare = names.filter(n => new RegExp(`(?<![.\\w$])${n}(?![\\w$])`).test(code));
  assert.deepStrictEqual(bare, [],
    `面板脚本裸引用了这些名字（应写成 KU.<名字>）：${bare.join(', ')}`);
});

// 地址契约必须先就位：upstream.js 必须是清单里**第一个**脚本。
// （原先只断言"在 parsers.js / app.js 之前"，写死了两个文件名 —— 清单变了它就失效。）
test('index.html 必须加载 upstream.js，且在清单里其余脚本之前', () => {
  const iUp = IDX.indexOf('upstream.js');
  assert.ok(iUp !== -1, 'index.html 没加载 upstream.js → window.KUpstream 为 undefined');
  for (const f of scriptFiles()) {
    if (f === 'upstream.js') continue;
    const i = IDX.indexOf(f);
    assert.ok(i !== -1, `index.html 缺 ${f}（与清单不一致）`);
    assert.ok(iUp < i, `upstream.js 必须在 ${f} 之前加载（地址契约要先就位）`);
  }
});
