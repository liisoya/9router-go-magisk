// @ts-check
/* app-core.js — 面板通用层：宿主全局取用 / 状态 / 忙碌包装 / 页签
 *
 * 只放**跨页共享**的东西；各页自己的东西在 page-*.js。
 * 加载顺序的唯一来源 = index.html 的 <script src> 清单（test/lib/app-harness.js 从它派生）。
 * 2026-09-28 从 app.js 纯搬迁（零行为变化）。
 */

'use strict';
const CFG = window.CFG;
const KB = window.KBridge;
const KP = window.KParsers;
const KU = window.KUpstream;   // 上游/模块 release 地址契约（唯一所有者，见 upstream.js）
// toast 的定时器收在顶层变量里（原先挂在 DOM 元素上 `t._tm`：自定义属性既靠不住，
// 也让"元素上有什么"变成隐式契约）
let toastTimer = 0;
function toast(msg, ms) {
  const t = document.getElementById('toast');
  t.textContent = msg; t.classList.add('show');
  clearTimeout(toastTimer); toastTimer = setTimeout(() => t.classList.remove('show'), ms || 2400);
}
const esc = s => String(s == null ? '' : s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
// **属性语境**必须多转义引号（2026-09-29 架构走查 B5）：`esc` 只够文本语境，值里若有 `"`
// 就会从 `attr="…"` 里越出去（节点名来自用户输入 → 自伤型属性注入）。
// 两个名字分开，是为了让"这里该用哪个"在调用点一眼可见 —— 一个名字两种语境正是出错的根源。
const escAttr = s => esc(s).replace(/"/g, '&quot;').replace(/'/g, '&#39;');
// ── 跨函数状态收敛点（Phase 3：替代裸 window._modUrl/_modUpdate/_engLatest 与 orphanAliases）──
const state = {
  modUrl: KU.DEFAULT_MOD_UPDATE_URL, // 模块更新源
  modUpdate: null,                // 远端 update.json 内容（modCheck → modUpdate）
  engLatest: '',                  // 引擎上游最新版本（engCheck → engUpdate）
  engineVersion: '',              // 引擎真实版本（panel → 概览/引擎更新比对）
  moduleVersion: '',              // 当前模块版本（refresh，不经 DOM 反解）
  orphans: []                     // 待清理孤儿别名（scanOrphans → cleanOrphans）
};

// ── 统一忙碌包装（Phase 3）：busy 态 / 异常 toast / finally 必然恢复按钮 ──
// 此前 optimize/speedTest 等手工 disable，一旦 await 链抛异常按钮永久卡死、UI 静默死亡。
async function withBusy(btn, busyLabel, fn) {
  const orig = btn ? btn.textContent : '';
  if (btn) { btn.disabled = true; if (busyLabel) btn.textContent = busyLabel; }
  try {
    return await fn();
  } catch (e) {
    console.error('[9r-panel]', e);
    toast('❌ 操作失败：' + (e && e.message || e), 4000);
  } finally {
    if (btn) { btn.disabled = false; btn.textContent = orig; }
  }
}
const $id = id => document.getElementById(id);

/** 长任务的"已用时长"心跳（A5）
 *
 * 为什么需要：DNS 探测/优选走的是**单次** shell 调用（`dnsfwd -P`，最长 60s），而 promise 形态
 * 拿不到中途输出 —— 期间界面只有一句"探测中…"，用户看到的就是"点下去像卡死"。
 * 拿不到真进度时，至少要让人看到它在动、动了多久（这也是排查素材：卡住还是慢）。
 */
async function withElapsed(el, label, fn) {
  if (el) el.style.display = 'block';
  const t0 = Date.now();
  const show = () => { if (el) el.textContent = `${label}… 已用 ${Math.round((Date.now() - t0) / 1000)}s`; };
  show();
  const timer = setInterval(show, 1000);
  try { return await fn(); } finally { clearInterval(timer); }
}

// ── 页签 ──
document.querySelectorAll('nav button').forEach(b => {
  b.onclick = () => {
    document.querySelectorAll('nav button').forEach(x => x.classList.remove('on'));
    document.querySelectorAll('.page').forEach(x => x.classList.remove('on'));
    b.classList.add('on');
    document.getElementById(b.dataset.page).classList.add('on');
  };
});
