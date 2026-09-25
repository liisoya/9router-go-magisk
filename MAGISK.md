# MAGISK.md — 模块层使用与构建说明

> 引擎/Dashboard 本身见上游 `README.md`；本文件只讲模块层。

## 目录

```
9router-go-magisk/
├── cmd/ internal/ web/ ...   # 上游 v1.9.1 引擎源码（原样，零魔改）
├── module/                   # Magisk/KernelSU 模块层
│   ├── module.prop           # id=ninerouter-go
│   ├── customize.sh          # 安装期：ABI 检查 + chmod 兜底
│   ├── service.sh            # 开机：dnsfwd + 引擎拉起
│   ├── action.sh             # 管理器「操作」按钮：状态显示
│   ├── uninstall.sh          # 卸载：停进程（保留数据）
│   ├── bin/                  # 9router-go（构建产物）、dnsfwd、sqlite3
│   └── webroot/index.html    # DNS 管理 WebUI（KSU/WebUIX）
├── tools/dnsfwd.c            # DNS 转发器源码（含构建脚本）
├── build.sh                  # 一键构建模块 zip
├── CONTEXT.md                # 术语表
└── docs/adr/                 # 架构决策记录
```

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

## 卸载

管理器直接卸载即可停进程；数据目录保留，需彻底清除请手动删 `/data/adb/9router-go/`。
