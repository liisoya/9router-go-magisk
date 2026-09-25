# MAGISK.md — 模块层使用与构建说明

> 引擎/Dashboard 本身见上游 `README.md`；本文件只讲模块层。

## 目录

```
9router-go-magisk/
├── cmd/ internal/ web/ ...   # 上游 v1.9.1 引擎源码（原样，零魔改）
├── module/                   # Magisk/KernelSU 模块层
│   ├── module.prop           # id=ninerouter-go + updateJson
│   ├── customize.sh          # 安装期：ABI 检查 + chmod 兜底
│   ├── service.sh            # 开机：schema 引导 + ops.sh(start-dns/seed-key) + 引擎拉起
│   ├── lib/ops.sh            # 运维唯一实现（seam）：status/start-dns/stop-dns/seed-key/get-port
│   ├── action.sh             # 管理器「操作」按钮：状态显示（数据来自 ops.sh）
│   ├── uninstall.sh          # 卸载：停进程（保留数据）
│   ├── bin/                  # 9router-go（构建产物）、dnsfwd、sqlite3
│   ├── etc/schema.sql        # 数据库 schema 引导（上游 DATABASE.md 派生，构建期断言防漂移）
│   └── webroot/              # 模块 WebUI（KSU/WebUIX）：概览/DNS/一致性检查/更新
│       ├── index.html        # HTML 结构（MODDIR 由构建期注入 __MOD_ID__）
│       ├── parsers.js        # 纯函数解析层（可离线 node --test 回归）
│       ├── bridge.js         # root-shell 桥（串行队列 / CRLF / sqlFile 环境补偿）
│       ├── app.js            # UI 装配与事件
│       └── test/             # 解析层 fixture 回归测试
├── tools/                    # dnsfwd.c、patch-clipboard.py、gen-schema.py、patches/
├── build.sh                  # 一键构建（7 步全校验管线）
├── CONTEXT.md                # 术语表
└── docs/adr/                 # 架构决策记录
```

## 离线测试

```bash
node --test module/webroot/test/parsers.test.js   # 解析层 fixture 回归（真机实测输出）
```

解析层（parsers.js）在浏览器与 node 双端加载：环境差异 bug（CRLF / 输出格式 /
解析规则）可离线红绿回归，不再依赖刷真机验证。

## 构建

```bash
./build.sh        # 产物 dist/9router-go-<版本>-arm64.zip
```

- web 前端：优先 bun，退化 npm
- 引擎：`CGO_ENABLED=0 GOOS=linux GOARCH=arm64` 静态编译，嵌入 `web/dist`
- dnsfwd：用仓库内预编译产物；重编见 `tools/build-dnsfwd.sh`（需 aarch64 sysroot）

## 安装与首次使用

1. KernelSU / Magisk 刷入 zip
2. 重启后引擎监听 **:20130**（默认，与上游一致），Dashboard 即引擎地址
3. 首次启动自动生成 Dashboard 登录密码：
   `adb shell su -c "cat /data/adb/9router-go/initial-password"`
4. DNS 管理：管理器 → 模块 → WebUI

## 数据目录

`/data/adb/9router-go/`（全新安装，不迁移旧模块数据）

| 文件 | 说明 |
|---|---|
| `db/data.sqlite` | 引擎数据库（供应商/密钥/设置） |
| `initial-password` | 首启生成的 Dashboard 密码 |
| `dns-upstreams.conf` | DNS 上游列表（WebUI 可编辑，严禁 127.0.0.1） |
| `dns-bind` | dnsfwd 绑定范围：loopback（默认）/ any |
| `port` | 持久端口（可选，默认 20130） |
| `9router.pid` / `dnsfwd.pid` | 进程 PID |

## 模块 WebUI 功能

管理器 → 模块 → WebUI 打开（KernelSU 内置 / WebUIX），四页签：

| 页签 | 功能 |
|---|---|
| 概览 | 引擎/dnsfwd 状态与内存占用、引擎端口修改（写 `port` 文件并重启） |
| DNS | 上游增删改、`dnsfwd -P` 探测、优选评分（可用率/RTT/fake-ip）一键应用 Top5、回滚、绑定范围 |
| 一致性检查 | 修复 Dashboard 出厂 key、清理孤儿 customModels/disabledModels（已删节点的残留，即客户端模型列表里 `openai-compatible-chat-<uuid>` 噪音的来源）、无凭据活跃连接警告 |
| 更新 | GitHub 加速节点本机测速选优（初始清单来自 moretools.app 聚合，可自定义）、引擎 release 检查/更新（SHA256 校验）、模块 zip 覆盖更新 |

- 加速节点选中值存 `$DATA_DIR/github-accel`，自定义清单 `$DATA_DIR/accel-list.conf`
- 模块更新源存 `$DATA_DIR/module-update-url`（默认指向 fork 的 `update.json`，
  发布 release 时在仓库根放 `{version, versionCode, zipUrl, changelog}`）
- module.prop 的 `updateJson` 字段供 KernelSU 管理器原生在线更新（与本 WebUI 通道独立）

## Dashboard 复制按钮补丁

HTTP（非 localhost）访问 Dashboard 时 `navigator.clipboard` 不存在、复制按钮
全部失效——上游问题。构建时由 `tools/patch-clipboard.py` 向 `web/dist/index.html`
注入 polyfill 降级实现（上游源码零改动，注入块带 BEGIN/END 标记）。
上游修复后：`CLIPBOARD_PATCH=0 ./build.sh` 跳过注入。

## 卸载

管理器直接卸载即可停进程；数据目录保留，需彻底清除请手动删 `/data/adb/9router-go/`。
