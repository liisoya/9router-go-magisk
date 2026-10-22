DNS 完整优选落地 + 引擎基线 1.9.14（dnsfwd TSV score 列 / 候选池单一来源 / 守护周期自动优选）。

按 ADR-0009 人工合并上游 v1.9.14（70 提交，冲突以上游为准 + 回插模块定制）。
本仓库无对应 decolua/9router PR —— 引擎侧是上游全量同步；模块侧是自主功能。

真机验收（Wi-Fi adb 192.168.10.10:5555，mihomo TUN 开启）发现并修复两处：dnsfwd load_file 不识别候选池 cat=/label= 元数据行（none-viable 根因）；探测 socket 被 TUN 劫持（dns-hijack any:53 → 死地址也 100 分，判据失真）——lifecycle 探测现在自动识别 TUN 默认路由并 -i 绑物理网卡绕行（O7 断言）。

- 模块: https://github.com/liisoya/9router-go-magisk
- issue: 无（ADR-0010 挂账功能的落地）
