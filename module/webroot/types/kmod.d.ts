/* 面板脚本的宿主全局声明（唯一一份）
 *
 * 类型**从实现派生**：KParsers / KBridge / KUpstream 的形状一律用 `typeof import(...)` 取，
 * 不在这里手写一份会漂的清单 —— 手写清单自己就会漂，那是本仓库反复吃过亏的地方
 * （见 AGENT-CONVENTIONS §2.1 与 upstream.js 的回潮扫描）。
 *
 * 本文件是**全局脚本**（无顶层 import/export），所以 `interface Window` 会与 DOM lib 的
 * Window 合并；这正是我们要的：面板脚本在浏览器里就是靠 window 上的这几个名字交接的。
 */

/** node 侧 UMD 分支：离线测试（node --test）require 这些文件时走 `module.exports = factory()` */
declare var module: { exports: unknown };

/** 宿主注入的 root shell 桥（KernelSU 内置 / WebUI X）。形态差异见 bridge.js 的自适应探测：
 * 3 参回调 / 2 参回调 / 返回 Promise 三种，哪种能收回输出就用哪种，不赌。
 * 三种形态的返回值统一放宽为 any —— 这是**外部边界**（宿主实现我们控制不了），
 * 类型闸在这里没有可断言的事实，硬写一个会变成假精度。 */
declare var ksu: {
  exec(cmd: string, cb: (chunk: string) => void): any;
  exec(cmd: string, opts: string, cb: (chunk: string) => void): any;
  exec(cmd: string): any;
};

/* 面板的 DOM 约定（一次声明，避免 55 处断言）
 *
 * 面板只从 document 取元素，并统一把它们当"带 value/disabled/onclick/dataset 的元素"用；
 * index.html 的结构**就是契约**（Step 4 的 DOMID 门禁会把 id 双向缝住）。
 * 这里只补 DOM lib 里 Element 上没有、而 HTMLElement 上才有的成员 —— 因为
 * `getElementById` 返回 `HTMLElement | null`、`querySelectorAll` 返回 `Element`，
 * 而面板对这两者不加区分地使用。
 *
 * 一律用 any：一是不与 lib 里更精确的声明冲突（如 HTMLElement.onclick 的函数签名），
 * 二是让类型闸专注在真会炸页面的那几类（名字/属性拼错、参数个数、跨文件顶层重名），
 * 而不是把一次收口变成 55 处 DOM 断言。下一档（strictNullChecks / strict）等
 * 逐文件补齐 JSDoc 时再开。
 */
interface Element {
  value: any;
  disabled: any;
  checked: any;
  onclick: any;
  dataset: any;
  style: any;
}

interface Window {
  /** 由 index.html 的内联脚本注入（MODDIR 的 __MOD_ID__ 在打包/直推时注入） */
  CFG: { MODDIR: string; DATA_DIR: string };
  KParsers: typeof import('../parsers.js');
  KBridge: typeof import('../bridge.js');
  KUpstream: typeof import('../upstream.js');
}
