// @ts-check
/* app-boot.js — 引导：刷新首屏，并用 panel 快照里的加速节点初始化「当前加速」
 *
 * 必须是 index.html 清单里的**最后一个**脚本（各页的按钮绑定要先完成）。
 * 2026-09-28 从 app.js 纯搬迁（零行为变化）。
 */

refresh().then(st => renderAccelCur(st && st.accel_sel));
