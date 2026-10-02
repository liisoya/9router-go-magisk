/* 面板装配层测试的共享桩具（两处收口合成一处）
 *
 * 收口 1（C5）：此前 ~90 行桩在 app-wiring / orphan-scan 各复制一份。
 * 收口 2（S1）：**加载哪些脚本、按什么顺序，唯一来源是 index.html 的 `<script src>` 清单** ——
 *   与浏览器行为一致（多个经典 script 在同一词法作用域里按序执行）。
 *   这样"新增文件忘了在 index.html 声明 / 忘了在测试里加载 / 忘了推送"这类漂移不可能发生：
 *     漏声明 → scriptFiles() 里没有它，而「每个 .js 都必须被声明」的用例会红；
 *     漏推送 → 文件不存在，加载即抛错（真机上的表现曾是 window.KUpstream undefined、整页失效）。
 *
 * 用法：
 *   const h = createHarness({ execHandler: cmd => cmd.includes('x') ? 'out' : '' });
 *   const els = h.loadApp();
 *   await els.get('btn-scan').onclick();
 *
 * 桩只提供「宿主全局」（window / ksu / localStorage）与最小 DOM；命令输出由调用方的
 * execHandler 按命令内容回答。脚本在 vm 上下文里跑**真实文件** —— 断言面是真实行为，
 * 不是复制出来的名字。parsers / bridge / upstream 也不再由 node require 注入，
 * 而是和 app 一样经 index.html 清单在同一个 vm 里跑（免得出现"两个 realm 两套实例"）。
 */
'use strict';
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const WEBROOT = path.join(__dirname, '..', '..');
const INDEX_HTML = path.join(WEBROOT, 'index.html');
const SCRIPT_SRC_RE = /<script[^>]*\bsrc=["']([^"']+)["'][^>]*>/gi;

/** index.html 按出现顺序声明的本地脚本（相对 webroot）—— 唯一清单 */
function scriptFiles() {
  const html = fs.readFileSync(INDEX_HTML, 'utf8');
  const files = [];
  for (const m of html.matchAll(SCRIPT_SRC_RE)) files.push(m[1]);
  if (!files.length) throw new Error('index.html 里没有任何 <script src>，唯一清单为空');
  // 外链脚本不参与本地加载（当前无外链；有的话也不该走这条断言）
  return files.filter(f => !/^https?:\/\//i.test(f));
}

function createHarness({ execHandler }) {
  global.window = { CFG: { MODDIR: '/data/adb/modules/ninerouter-go', DATA_DIR: '/data/adb/9router-go' } };
  global.localStorage = {
    getItem: k => (k === '__kmod_exec_mode' ? 'cb3' : null),
    setItem() {}, removeItem() {}
  };

  // 宿主 exec 桩。回调形态（cb3/cb2）传的是**回调名**，宿主在**发起它的那个 realm** 里解析它：
  // bridge.js 在 vm 里跑时，回调挂在 vm 自己的 window 上（而不是 node 的 global.window）。
  //
  // 所以桩必须**按 realm 各建一份**。曾经共用一个可变的 activeWin：后一次 loadApp 会把它顶掉，
  // 前一个 realm 的在途命令就再也找不到自己的回调 → 永不完成 → 每个都等满 120s 超时，
  // 表现是"用例全绿但进程不退出"（整个门禁被拖死，且看不出是谁）。
  function makeExec(win) {
    return {
      exec(cmd, opts, cb) {
        const out = (execHandler(cmd) || '') + '';
        const payload = (out ? out + '\n' : '') + '__KMOD_DONE__0';
        setTimeout(() => {
          const fn = typeof cb === 'function' ? cb : win[cb];
          if (typeof fn === 'function') fn(payload);
        }, 0);
      }
    };
  }
  // node realm 的桩：供 require('../bridge.js') 的用例（命令构造器）与自带桩的调用方使用
  global.ksu = makeExec(global.window);

  function makeEl(id) {
    return {
      id, textContent: '', innerHTML: '', value: '', disabled: false, className: '',
      style: {}, dataset: {}, onclick: null,
      // classList 记录调用：既有用例当 no-op 用不受影响；观察模式用例要断言 on 的加/摘
      classList: {
        _calls: [],
        add(c) { this._calls.push(['add', c]); },
        remove(c) { this._calls.push(['remove', c]); },
        toggle() {}, contains: () => false
      },
      querySelectorAll: () => [], appendChild() {}, remove() {}, select() {}, focus() {}, addEventListener() {}
    };
  }

  /** 按 index.html 声明的顺序，在同一个 vm 上下文里跑完全部脚本（等价于浏览器） */
  let lastWin = null;
  function loadApp() {
    const els = new Map();
    const win = {
      CFG: global.window.CFG,
      localStorage: global.localStorage,
      setTimeout, clearTimeout, setInterval, clearInterval, console,
      TextEncoder, TextDecoder, btoa, atob, Promise, Date, Math, JSON,
      prompt: () => null,
      document: {
        getElementById: id => {
          if (!els.has(id)) els.set(id, makeEl(id));
          return els.get(id);
        },
        querySelectorAll: () => [], createElement: () => makeEl('tmp'),
        body: { appendChild() {}, removeChild() {} }, execCommand: () => true
      }
    };
    win.window = win;
    win.self = win;
    win.ksu = makeExec(win);

    const ctx = vm.createContext(win);
    lastWin = win;
    for (const rel of scriptFiles()) {
      const file = path.join(WEBROOT, rel);
      if (!fs.existsSync(file)) {
        throw new Error(`index.html 声明了 ${rel}，但 webroot 下没有这个文件（漏提交/漏推送？）`);
      }
      vm.runInContext(fs.readFileSync(file, 'utf8'), ctx, { filename: rel });
    }
    // 返回**按需创建**的视图：桩 Map 只收录"初始化时被请求过"的 id，而真实 DOM 里元素总是存在。
    // 需要驱动输入框的新用例（如 page-flows 里给 in-port/upstreams 赋值）因此能像浏览器一样拿到元素；
    // 既有用例只读它已有的键（`[...els.keys()]` 找 btn-* 的绑定），行为不变。
    return new Proxy(els, {
      get(t, p) {
        if (p === 'get') {
          return id => {
            if (!t.has(id)) t.set(id, makeEl(id));
            return t.get(id);
          };
        }
        const v = Reflect.get(t, p, t);
        return typeof v === 'function' ? v.bind(t) : v;
      }
    });
  }

  /** 捕获初始化链上的 unhandledRejection（refresh() 的异常正是这样冒出去的） */
  function recordRejections() {
    const list = [];
    const on = r => list.push(r);
    process.on('unhandledRejection', on);
    return { list, stop: () => process.off('unhandledRejection', on) };
  }

  // lastWin：最近一次 loadApp 的 vm 全局 —— 顶层函数（如 page-dns 的 toggleCandidate）
  // 就挂在那里，测试可以直接驱动（芯片是 innerHTML 字符串，桩 DOM 的 querySelectorAll 驱动不了）
  return { loadApp, recordRejections, get lastWin() { return lastWin; } };
}

module.exports = { createHarness, scriptFiles, WEBROOT };
