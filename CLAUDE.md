# Primary A 生产部署约束（eco-sim-20261021 首部署落地）

本项目部署于共享生产服务器 Primary A（120.26.69.202）。

- name: `product-image-engine`；app_id: `product-image`
- 阶段性形态：**无公网域名**（无 DNS、未上 nginx），仅 loopback + SSH 隧道访问；nav-manifest 中行态如实为 `degraded`。
- 服务：`product-image-server`(127.0.0.1:18220, Go) + `product-image-web`(127.0.0.1:18320, Next)
- 目录：程序 `/home/deploy/product-image-engine/`；数据 `/var/lib/product-image/`；密钥 `/etc/product-image/*.env`(0640)
- systemd：`product-image-server.service` / `product-image-web.service`（User=deploy，loopback only）

## 网络铁律
1. 只监听 `127.0.0.1`；禁止 `0.0.0.0`；不开公网端口。
2. 浏览器不得直调平台内部 API；不得经 nginx 暴露 `/internal/v1/*`。
3. 平台公共服务：identity 18101 / billing 18102 / task 18103 / upload 18104 / notify 18105，永不公网。

## 平台接入
- 每 app × service 独立 Token；服务器侧生成，禁入 Git/日志/聊天/前端。
- 内部调用头：`X-App-ID: product-image` + `X-PilotSeaView-Internal-Token: <对应服务专用 token>`。
- identity 用户唯一身份 = `principal_id`，禁止第二套用户 ID。
- 当前 `ECO_BILLING_ENABLED=0`、`FEATURE_GENERATION_ENABLED=0`（诚实未接通，不伪造）；开启前按 hold→spend/refund 走 platform-billing，禁止第二资金账本。

## SQLite / 备份
- DB：`/var/lib/product-image/product-image.db`（WAL）。
- 备份用 SQLite backup API/`.backup`，禁直接 cp 活动 WAL 库；上传 `oss://sqlite-back/sqlite/product-image/YYYY/MM/DD/`（VPC Endpoint `oss-cn-hangzhou-internal.aliyuncs.com`），带 SHA256+manifest；未验证恢复不得声称"备份完成"。

## 共享服务器红线
不动其他项目 nginx/systemd/文件/Token；不为本项目改整机网络策略；密钥不出现在本文件；影响共享基础设施先确认。
