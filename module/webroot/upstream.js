// @ts-check
/* upstream.js — 上游/模块 release 地址契约的唯一所有者。
 *
 * 深 module：把「上游怎么命名它的发布物」这件事收在一处 —— 版本清单地址、release tag 形态、
 * 资产名、校验和文件名、加速前缀怎么拼。纯函数、无 DOM、无 shell 副作用，可离线断言。
 *
 * 为什么单独一个文件（AGENT-CONVENTIONS §2.1「先问谁是所有者」）：
 *   2026-09-27 用户实测「下载并更新引擎」必然失败 —— app.js 拿 version.json 的
 *   `latestVersion`（"1.9.3"，**不带 v**）直接拼 release URL，而上游的 tag 是 `v1.9.3`，
 *   于是 GitHub 与每一个加速节点都回 404（实测 size=9 的 "Not Found"）。那个 9 字节正文正是
 *   2026-09-26「404 正文被当引擎装上」事故的同一个东西 —— 当时加了 -f 与两道门禁，只是把
 *   「静默装坏」变成「显式失败」，**下载地址本身从来没对过**。
 *   这条约定既不是解析（parsers.js）也不是 shell 命令构造（bridge.js），散在 app.js 里就没人
 *   能离线断言它，所以给它一个所有者，并配一条会红的门禁（test/upstream.test.js，含"app.js
 *   不许再手写 release URL"的回潮扫描）。
 *
 * 依赖：index.html 先加载本文件，再加载 parsers.js / bridge.js / app.js。
 */
(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.KUpstream = factory();
  // 类型闸说明：`this` 分支转 any —— 本文件带了 module.exports，TS 按 CJS 模块处理，
  // 顶层 `this` 的类型是"模块导出对象"，而浏览器分支要的是宿主全局（同 parsers.js / bridge.js）。
})(typeof self !== 'undefined' ? self : /** @type {any} */ (this), function () {
  'use strict';

  // 引擎（上游）与模块各自的仓库：引擎资产只在引擎仓库发，模块 zip 只在模块仓库发。
  const ENGINE_REPO = 'luqman-v1/9router-go';
  const MODULE_REPO = 'liisoya/9router-go-magisk';
  // 模块只支持 arm64（customize.sh 安装期按 ro.product.cpu.abi 已判定并 abort 其它架构）
  const ENGINE_ASSET = '9router-go-linux-arm64';
  const SUMS_ASSET = 'SHA256SUMS.txt';

  // 版本清单（两条更新通道各自的"检查"数据源）
  const ENGINE_VERSION_URL = `https://raw.githubusercontent.com/${ENGINE_REPO}/main/version.json`;
  const DEFAULT_MOD_UPDATE_URL = `https://raw.githubusercontent.com/${MODULE_REPO}/main/update.json`;
  // 加速节点测速的打靶目标：引擎仓库 main 上的 VERSION（真实存在的小文件，代价/收益比最高）
  const ENGINE_VERSION_FILE_URL = `https://raw.githubusercontent.com/${ENGINE_REPO}/main/VERSION`;

  // 上游 release 的 tag 一律带 v（github.com/luqman-v1/9router-go/releases 实测 v1.9.0–v1.9.3，
  // 与它自己的 CI 触发条件 `tags: v*` 一致）；而 version.json 的 latestVersion 是裸版本号
  // （"1.9.3"）—— 两者不是同一种东西，拼 URL 必须以 **tag 形态**为准：缺 v 就补，已带 v
  // （含大写 V）原样保留。空值返回空串，让调用方显式失败：拼出 "…/download//asset" 只会
  // 换来一个更难懂的 404（本次故障就是这样被掩盖了一个版本的）。
  function releaseTag(ver) {
    const s = String(ver == null ? '' : ver).trim();
    if (!s) return '';
    return /^v/i.test(s) ? s : 'v' + s;
  }

  function releaseAssetUrl(repo, ver, asset) {
    const tag = releaseTag(ver);
    if (!tag) return '';
    return `https://github.com/${repo}/releases/download/${tag}/${asset}`;
  }

  // 面板「下载并更新引擎」的两个下载目标：同一 tag 下的二进制与校验和（缺任一个都不该装）
  const engineAssetUrl = ver => releaseAssetUrl(ENGINE_REPO, ver, ENGINE_ASSET);
  const engineSumsUrl = ver => releaseAssetUrl(ENGINE_REPO, ver, SUMS_ASSET);

  // 加速节点只能代理 GitHub；用户自定义的更新源可能根本不是 GitHub。
  const GITHUB_URL = /^https?:\/\/(github\.com|raw\.githubusercontent\.com|objects\.githubusercontent\.com)\//;
  const isGithubUrl = url => GITHUB_URL.test(String(url == null ? '' : url));

  // withAccel：只做拼接（调用方已确定这条 URL 要走加速；空前缀 = 直连）。
  // withAccelIfGithub：GitHub 域才加前缀 —— 自定义更新源照原样直连。两个动词都在这儿，
  // 免得"什么时候该加前缀"这个判断再散回调用方。
  function withAccel(url, prefix) {
    const u = String(url == null ? '' : url);
    const p = String(prefix == null ? '' : prefix).trim();
    return p ? p + u : u;
  }
  function withAccelIfGithub(url, prefix) {
    const u = String(url == null ? '' : url);
    return isGithubUrl(u) ? withAccel(u, prefix) : u;
  }

  // SHA256SUMS.txt → 指定资产的期望摘要（拿不到返回空串，由 parsers.checksumGate 按
  // fail-closed 判死 —— 判据仍在 parsers.js，这里只负责"这个文件长什么样"）。
  // 文件是 `sha256␣␣文件名` 每行一条（二进制模式会写成 `sha256␣␣*文件名`）。
  // 按**文件名整词**比对，不用子串：子串匹配会让 9router-go-linux-amd64 这类行有被误命中的机会。
  function parseSumFor(sumsText, asset) {
    const want = String(asset == null ? '' : asset).trim();
    if (!want) return '';
    for (const line of String(sumsText == null ? '' : sumsText).replace(/\r/g, '').split('\n')) {
      const m = line.trim().match(/^([0-9a-f]{64})\s+\*?(.+)$/i);
      if (m && m[2].trim() === want) return m[1].toLowerCase();
    }
    return '';
  }

  return {
    ENGINE_REPO, MODULE_REPO, ENGINE_ASSET, SUMS_ASSET,
    ENGINE_VERSION_URL, DEFAULT_MOD_UPDATE_URL, ENGINE_VERSION_FILE_URL,
    releaseTag, releaseAssetUrl, engineAssetUrl, engineSumsUrl,
    isGithubUrl, withAccel, withAccelIfGithub, parseSumFor
  };
});
