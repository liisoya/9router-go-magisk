// @ts-check
/* app-boot.js — 引导：刷新首屏，并用 panel 快照里的加速节点初始化「当前加速」
 *
 * 必须是 index.html 清单里的**最后一个**脚本（各页的按钮绑定要先完成）。
 * 2026-09-28 从 app.js 纯搬迁（零行为变化）。
 */

refresh().then(st => renderAccelCur(st && st.accel_sel));

// ── 观察模式（默认关、不记忆，2026-10-01）────────────────────────────────
// 回答"要不要自动刷新"：panel 每次都是**新起一个 root shell**（su + ops.sh panel：
// sqlite3 计数 + /proc 读数 + base64 upstreams），5s 一次 CPU 占比 <1%，但只有"人正盯着
// 面板"时才有意义 —— 所以做成**点开才刷的观察开关**（排查内存变化时让数值自己动）。
// 默认关、不跨会话记忆：忘了关最多延续到页面关闭，不会长期后台唤醒
// （Android WebView 对不可见页本会暂停 timer；hidden 显式判一道，防恢复瞬间的堆叠）。
// 为什么全量 panel 而不是"只刷内存"的轻命令：panel 是单命令拿全的唯一事实源，
// 再开一条只读 RSS 的轻命令 = 两套数据源 + 契约键维护，省的只是几百毫秒 —— 不值。
// 间隔经 CFG.observeMs 注入（测试用），生产默认 5000；开关本身即状态（.on 亮品牌色）。
const OBSERVE_MS = Number(/** @type {any} */ (CFG).observeMs) || 5000;
const _observeBtn = document.getElementById('btn-observe');
let _observeTimer = 0;
if (_observeBtn) _observeBtn.onclick = () => {
  if (_observeTimer) {   // 再点一次 = 关闭：清定时器、摘状态，一切回到默认关
    clearInterval(_observeTimer); _observeTimer = 0;
    _observeBtn.classList.remove('on');
    _observeBtn.textContent = '观察';
    return;
  }
  _observeBtn.classList.add('on');
  _observeBtn.textContent = '观察中';   // 开关本身即状态：胶囊文字就是状态牌
  refresh();   // 点开立即刷一次，不等第一个周期
  _observeTimer = setInterval(() => { if (!document.hidden) refresh(); }, OBSERVE_MS);
};
