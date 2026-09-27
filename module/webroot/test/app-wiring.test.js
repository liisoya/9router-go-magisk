/* app.js 装配层冒烟测试 —— 在桩 DOM + 桩 ksu.exec 里跑**真实的**初始化与 refresh()。
 *
 * 为什么需要它（2026-09-27 真机事故，一路发到 r1 才被发现）：
 *   把 URL 常量收编进 upstream.js 时，漏改了 app.js 里一处裸引用 ——
 *   `renderPanel` 的 `state.modUrl = st.mod_url || DEFAULT_MOD_UPDATE_URL`。
 *   纯函数用例（parsers / bridge / upstream）全绿、回潮扫描也只查字符串字面量，
 *   于是这个 ReferenceError 发到设备上：面板显示 localStorage 里的旧快照
 *   （引擎 1.9.2 / 旧 PID），"服务地址"卡在"加载中"、资源占用全是 "-"，
 *   点重启弹 "DEFAULT_MOD_UPDATE_URL is not defined"。
 *
 * 判据是"**跑一遍真实的装配路径**"，不是再抄一遍名字：
 *   · 有异常 → unhandledRejection 捕获（refresh() 的异常正是这样冒出去的）
 *   · 渲染有没有跑到底 → 断言 renderPanel **靠后**那两块（资源占用 / 服务地址）
 *     和链尾的 renderAccelCur 都留下了痕迹。任一没渲染 = 中途抛错。
 *
 * bridge.js 是从**宿主全局**读 window / ksu / localStorage 的（它是 UMD，在 node 里走
 * module.exports 分支），所以这些桩必须打在 `global` 上；app.js 则在 vm 上下文里跑。
 * 运行：node --test module/webroot/test/
 */
'use strict';
const { test } = require('node:test');
const assert = require('node:assert');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

// 真机形态的 panel 单行输出（值不含空格 —— 见 ops.sh cmd_panel 的约定）
const PANEL_LINE = [
  'port=20130 bind=loopback module_version=v1.9.3-r1 versioncode=109030',
  'engine_version=1.9.4 engine_ver_src=runtime engine_ver_healed=0',
  'lan_ip=192.168.1.2 dns=up dns_pid=101 engine=up engine_pid=202',
  'watchdog=up watchdog_pid=303 factory_key=1 apikeys_total=1',
  'mem_total=5809472 mem_avail=1814288 engine_rss=24316 dns_rss=1128',
  'upstreams_b64=bmFtZXNlcnZlciAxMTkuMjkuMjkuMjk=',
  'mod_url= accel_sel='
].join(' ');

// ── 宿主全局桩（bridge.js 只认这些）──
// 形态缓存必须命中 cb3：否则会先跑 2.5s 的形态探测，而探测用"回调名"而不是函数。
global.window = { CFG: { MODDIR: '/data/adb/modules/ninerouter-go', DATA_DIR: '/data/adb/9router-go' } };
global.localStorage = {
  getItem: k => (k === '__kmod_exec_mode' ? 'cb3' : null),
  setItem() {}, removeItem() {}
};
global.ksu = {
  exec(cmd, opts, cb) {
    let out = '';
    if (cmd.includes('ops.sh panel') || cmd.includes('ops.sh status')) out = PANEL_LINE;
    else if (cmd.includes('github-accel')) out = '';   // 未选加速节点 = 直连
    const payload = out ? out + '\n__KMOD_DONE__0' : '__KMOD_DONE__0';
    setTimeout(() => {
      // cb3 形态传的是**回调名**，真正的函数挂在 window 上（sentinelExec 注册的）
      const fn = typeof cb === 'function' ? cb : global.window[cb];
      if (typeof fn === 'function') fn(payload);
    }, 0);
  }
};
const KB = require('../bridge.js');
const KP = require('../parsers.js');
const KU = require('../upstream.js');

function makeEl(id) {
  return {
    id, textContent: '', innerHTML: '', value: '', disabled: false, className: '',
    style: {}, dataset: {}, onclick: null,
    classList: { add() {}, remove() {}, toggle() {}, contains: () => false },
    querySelectorAll: () => [],
    appendChild() {}, remove() {}, select() {}, focus() {}, addEventListener() {}
  };
}

// 跑一次真实的 app.js：桩掉 DOM，其余（parsers / bridge / upstream）用真货
function loadApp() {
  const els = new Map();
  const rejections = [];
  const onRejection = r => rejections.push(r);
  process.on('unhandledRejection', onRejection);

  const win = {
    CFG: global.window.CFG,
    KParsers: KP,
    KBridge: KB,
    KUpstream: KU,
    localStorage: global.localStorage,
    ksu: global.ksu,
    setTimeout, clearTimeout, setInterval, clearInterval,
    console, TextEncoder, TextDecoder, btoa, atob, Promise, Date, Math, JSON,
    prompt: () => null,
    document: {
      getElementById: id => {
        if (!els.has(id)) els.set(id, makeEl(id));
        return els.get(id);
      },
      querySelectorAll: () => [],
      createElement: () => makeEl('tmp'),
      body: { appendChild() {}, removeChild() {} },
      execCommand: () => true
    }
  };
  win.window = win;
  win.self = win;

  const ctx = vm.createContext(win);
  vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'app.js'), 'utf8'), ctx, { filename: 'app.js' });
  return { els, rejections, onRejection };
}

test('app.js 初始化 + refresh() 必须跑到底（装配层没有裸引用 / 中途抛错）', async () => {
  const { els, rejections, onRejection } = loadApp();
  await new Promise(r => setTimeout(r, 80));   // 等 refresh() 与它的 .then 链跑完
  process.off('unhandledRejection', onRejection);

  const rejected = rejections.map(r => (r && r.stack) || String(r)).join('\n');
  assert.strictEqual(rejections.length, 0,
    `初始化链上抛出了异常（面板会显示旧快照、后面几块渲染不出来）：\n${rejected}`);

  // 状态行（renderPanel 前半段）
  assert.strictEqual(els.get('st-ver').textContent, '1.9.4', '引擎版本行没渲染');
  // 下面三处都在 renderPanel 的**后半段 / 链尾** —— 有值才说明整条路跑到底了
  assert.notStrictEqual(els.get('mem-eng').textContent, '',
    '资源占用没渲染（renderPanel 在它之前抛错了？）');
  assert.notStrictEqual(els.get('mem-dns').textContent, '', 'dnsfwd 内存没渲染');
  assert.ok(els.get('addr-list').innerHTML.includes('20130'),
    '服务地址没渲染（卡在"加载中"的形态）');
  assert.ok(els.get('accel-cur').textContent, '加速节点行没渲染（链尾 renderAccelCur 没跑到）');
});

test('app.js 的每个按钮绑定都真的执行到了（id 写错会静默失联）', () => {
  const { els } = loadApp();
  const ids = [...els.keys()].filter(id => id.startsWith('btn-'));
  assert.ok(ids.length >= 20, `只绑定到 ${ids.length} 个按钮，装配层似乎没跑完`);
  const unbound = ids.filter(id => typeof els.get(id).onclick !== 'function');
  assert.deepStrictEqual(unbound, [],
    `这些按钮没绑上处理函数（app.js 里的 id 写错了？）：${unbound.join(', ')}`);
});
