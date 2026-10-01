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
// 这里只放**单页自己的**可变状态。跨页共享的派生量见下面的 snapshot。
const state = {
  modUpdate: null,                // 远端 update.json 内容（modCheck → modUpdate）
  engLatest: '',                  // 引擎上游最新版本（engCheck → engUpdate）
  orphans: []                     // 待清理孤儿别名（scanOrphans → cleanOrphans）
};
// `state.moduleVersion` 已删除（2026-10-01 架构评审）：**写了但全仓无人读** ——
// 死字段会让"谁在读它"这个问题永远答不完，而答案是"没人"。

// ── 面板快照的派生量：**跨页只读**，唯一写入点是 setSnapshot() ──
// 为什么从 state 里单独拎出来：engineVersion 是概览写、更新页读的跨页值，而"更新页必须先
// refresh 才能用 engineVersion"这条顺序原先只写在 gate-flows 的注释里 —— 与 accel 的 I6
// 事故同族（袋式状态的隐式不变量：谁知道什么时候能读）。归一化（trim）也只在这里做一次。
// **更新源不在这里**：它是固定的 KU.MOD_UPDATE_URL（2026-10-01 起不接受用户自定义）——
// 让它跟着快照走，等于又留了一条"可以改"的通道。
const snapshot = {
  engineVersion: ''
};
/** 整份 panel 快照进来 → 归一化写入（唯一入口） */
function setSnapshot(st) {
  snapshot.engineVersion = String((st && st.engine_version) || '').trim();
}

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

// ── 页面快照应用器：每个页面模块自注册，refresh() 只负责广播 ──
// 为什么（2026-10-01 架构评审 #2）：概览页原先直接调 DNS 页的装载函数、还往 DNS 页与更新页的
// DOM 写值 —— 要理解"刷新时 DNS 区为何变化"得在三个文件间来回跳。现在每个页面自己决定
// 渲染哪些 DOM，概览不再知道 upstreams / cand-chips / in-modurl 的存在。
// 注册顺序 = 脚本加载顺序（确定性）；某个页面抛错不影响其它页面。
const PANEL_APPLIERS = [];
function onPanelSnapshot(fn) { PANEL_APPLIERS.push(fn); }
async function applySnapshotToAll(st) {
  // 派生量先归一化，再让各页渲染 —— 这样"哪一页先跑"不影响它们读到什么
  setSnapshot(st);
  for (const fn of PANEL_APPLIERS) {
    try { await fn(st); } catch (e) { console.error('[9r-panel]', e); }
  }
}

/** 「调 ops → 比对状态词 → 诚实回执 → refresh」的**唯一实现**（2026-10-01 架构评审 #4）
 *
 * 为什么收成一处：这条不变量原先手抄 6 遍、各判各的 toast，而 A5 / A5b 两次事故
 * （丢弃 ops.sh 的返回值 → 无条件报成功，与刷新后的状态卡互相打脸）**都发生在这些调用点**。
 * 诚实规则只在这里写一次，新增动作不会漏判；测试也只需跨这一个 interface。
 *
 * 语义要点：
 *   · 成功 = shell 回词命中 `KP.ACTION_WORDS[subcmd].ok`。**其余一律算失败**（缺省拒绝，
 *     未知 subcmd 同样拒绝）——"哪个词算成功"的唯一所有者是词表，调用方不再手抄。
 *   · 失败时把 shell 的**原始输出**透出来 —— 一句笼统的"失败"正是过去事故里最缺的东西。
 *   · 回执前先 refresh（除非 refreshAfter:false）：让 toast 与状态卡说的是同一时刻的事。
 *
 * @param {object} o
 * @param {HTMLElement|null} [o.btn] 有则包 withBusy（busy 态 + 异常兜底）
 * @param {string} [o.label] busy 文案
 * @param {string} o.subcmd ops.sh 子命令（成功词集查 KP.ACTION_WORDS）
 * @param {string|((r:any)=>string|Promise<string>)} o.okMsg
 * @param {string|((r:any)=>string|Promise<string>)} o.failMsg
 * @param {number} [o.okMs] @param {number} [o.failMs]
 * @param {boolean} [o.refreshAfter]
 */
async function runOpsAction(o) {
  const pick = async (v, r) => (typeof v === 'function' ? await v(r) : v);
  const run = async () => {
    const r = await KB.ops(o.subcmd);
    const got = String((r && r.out) || '').trim();
    const ok = KP.actionOk(o.subcmd, r && r.out);
    if (o.refreshAfter !== false) await refresh();
    const msg = ok ? await pick(o.okMsg, r)
                   : (await pick(o.failMsg, r)) + `（shell 回：${got || '空'}）`;
    toast(msg, ok ? (o.okMs || 3200) : (o.failMs || 5000));
    return ok;
  };
  return o.btn ? withBusy(o.btn, o.label, run) : run();
}

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
