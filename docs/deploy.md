# 云端部署（Docker + Caddy）

## 架构

```
浏览器 ──HTTPS:8788──> Caddy（自签名证书，唯一对外端口）
                           │ 内网 HTTP
                           ▼
                     apiunit（非 root，仅容器网络可达）
                           │
                           ▼
                     /data 数据卷
```

- 宿主机只暴露 `8788/tcp`，后端容器不发布端口。
- Caddy 使用内置 CA 为 `SITE_HOST` 签发证书；没有域名时用公网 IP 即可。
- 登录密码、会话 Cookie、上游 API Key 全程走 TLS，明文 HTTP 对外不再可用。

## 快速开始

```bash
cp .env.example .env
# 编辑 .env：填写 AUTH_PASSWORD（20 位以上随机串）和 SITE_HOST（公网 IP 或域名）
# 生成密码：openssl rand -base64 24

docker compose up -d --build
docker compose ps
```

访问 `https://<SITE_HOST>:8788`。

首次访问浏览器会提示证书不受信任，这是自签名证书的正常提示。将 Caddy 的根证书导入系统或浏览器的“受信任的根证书颁发机构”后即可消除警告：

```bash
docker compose cp caddy:/data/caddy/pki/authorities/local/root.crt ./apiunit-root.crt
```

`root.crt` 只用于本机信任，不要提交到代码仓库。

## 无法访问 Docker Hub 时

构建需要 `node:22-alpine`、`golang:1.26-alpine`、`alpine:3.22`，运行需要 `caddy:2-alpine`。若服务器拉取 Docker Hub 超时，可先通过镜像站拉取并改成官方标签：

```bash
MIRROR=docker.m.daocloud.io/library
for img in node:22-alpine golang:1.26-alpine alpine:3.22; do
  docker pull "$MIRROR/$img"
  docker tag "$MIRROR/$img" "$img"
done
docker pull docker.1ms.run/library/caddy:2.10-alpine
docker tag docker.1ms.run/library/caddy:2.10-alpine caddy:2-alpine
```

Go 模块默认走 `goproxy.cn`，npm 默认官方源；如需更换：

```bash
docker build \
  --build-arg GOPROXY=https://proxy.golang.org,direct \
  --build-arg NPM_REGISTRY=https://registry.npmmirror.com \
  -t apiunit:latest .
```

## 数据与备份

| 卷 | 内容 | 说明 |
| --- | --- | --- |
| `apiunit-data` | `apiunit.db`、`secret.key`、分词器、检测图片 | 核心数据，必须备份 |
| `caddy-data` | Caddy 内部 CA 与证书 | 丢失后需重新信任根证书 |

```bash
# 备份数据卷
docker run --rm -v apiunit-data:/data -v "$PWD":/backup alpine \
  tar czf /backup/apiunit-data-$(date +%F).tgz -C /data .
```

恢复时把归档解回同名卷即可。**`secret.key` 丢失后，数据库里的 API Key 与已加密配置无法恢复。**

## 主密钥分离（推荐）

默认主密钥保存在 `DATA_DIR/secret.key`，与 SQLite 同卷。更稳妥的做法是把主密钥放到独立的 Docker secret：

```bash
mkdir -p secrets
openssl rand -base64 32 > secrets/apiunit_master_key
# 打开 docker-compose.yml 中被注释的 APIUNIT_MASTER_KEY_FILE 与 secrets 段
docker compose up -d
```

注意：

- 首次启动前配置好，或先清空旧数据重新录入凭据；已有数据用旧密钥加密，换钥后无法解密。
- 备份 `secret.key` 或 `secrets/apiunit_master_key` 本身，不要与数据备份放在同一位置。
- 没有 Docker secret 时也可用环境变量 `APIUNIT_MASTER_KEY`（base64/hex），但会出现在 `docker inspect` 中，不如文件挂载。

## 更新与维护

### 每次更新前先备份

```bash
mkdir -p backups
# 先停后端，避免 SQLite WAL 在打包过程中变化；几秒即可完成
docker compose stop apiunit
docker run --rm -v apiunit_apiunit-data:/data:ro -v "$PWD/backups":/backup alpine \
  tar czf /backup/apiunit-$(date +%F-%H%M).tgz -C /data .
docker compose start apiunit
```

备份文件里包含 `secret.key`，与数据放在一起可以完整恢复；但这也意味着备份本身要妥善保管（建议加密后异地存放）。建议每季度做一次恢复演练，确认备份真的可用。

### 标准更新流程

```bash
# 1. 备份（见上）
# 2. 拉取代码
git pull
# 3. 构建新镜像，并打一个版本标签便于回滚
docker compose build
docker tag apiunit:latest apiunit:$(date +%Y%m%d-%H%M)
# 4. 切换
docker compose up -d
# 5. 验证
docker compose ps
curl -k https://127.0.0.1:8788/api/health
```

先 `docker compose build` 再 `up -d` 时，Compose 会用新镜像重建 `apiunit` 容器，数据卷不受影响。数据库迁移在启动时自动执行（只做加表/加列），但更新前仍建议备份。

### 回滚

```bash
docker images apiunit                      # 找到上一个版本标签
docker tag apiunit:20261128-1015 apiunit:latest
docker compose up -d
```

回滚只切换镜像，不动数据卷。若新版本已写入旧版本无法解析的数据（例如运行配置加密），需要同时恢复更新前的数据卷备份。

### 修改登录密码

```bash
# 编辑 .env 中的 AUTH_PASSWORD
docker compose up -d          # 只会重建 apiunit
```

会话签名包含密码摘要，改密后所有旧会话立即失效，不需要额外操作。

### 更换访问地址 / 证书

- 只改 IP 或域名：修改 `.env` 的 `SITE_HOST`，执行 `docker compose up -d`，Caddy 会自动签发新地址的证书；根证书不变，无需重新信任。
- 换域名并改用正式证书：按文末“换成受信任证书”修改，放行 80/443 后再启动。
- `caddy-data` 卷保存内部 CA。删除该卷会生成新的根证书，需要重新导入信任。

### 主密钥

默认主密钥是数据卷里的 `secret.key`，与数据库一起备份即可。主密钥一旦更换，已有 API Key 和加密配置将无法解密，因此：

- 想与数据分离存放，请在**首次启动前**按“主密钥分离”配置。
- 运行中迁移主密钥，可把现有 `secret.key` 原样复制出来作为外部密钥文件，再启用 `APIUNIT_MASTER_KEY_FILE`，确认服务正常后再从数据卷删除 `secret.key`。
- 主密钥丢失或泄漏时，只能删除并重新录入所有上游与加密配置；数据库中的测试历史仍可读取。
- 目前没有自动轮换工具，不要在未备份的情况下更换。

### 日志、磁盘与清理

```bash
docker compose logs --tail=200 apiunit caddy
docker compose exec apiunit df -h /data
```

- 测试历史按 `RETENTION_DAYS` 每天自动清理；也可在“设置”里手动清理。
- 容器日志已限制为每个服务 10 MB × 3 份，不会无限增长。
- 镜像更新后可清理旧镜像：`docker image prune -f`。不要在生产机上执行 `docker system prune -a`。
- 基础镜像的安全更新：定期执行 `docker compose build --pull` 后重新 `up -d`。
- Caddy 单独升级：`docker compose pull caddy && docker compose up -d`（升级大版本时先改 compose 里的版本号并检查 Caddyfile）。

### 迁移到新服务器

1. 在新服务器拉取代码和 `.env`。
2. 把旧服务器的最新数据备份复制过去。
3. 创建并恢复数据卷：

```bash
docker volume create apiunit_apiunit-data
docker run --rm -v apiunit_apiunit-data:/data -v "$PWD/backups":/backup alpine \
  sh -c 'tar xzf /backup/apiunit-YYYYMMDD-HHMM.tgz -C /data'
```

4. 如果 IP 变了，修改 `SITE_HOST`；`docker compose up -d`。
5. 把旧服务器的 `caddy-data` 卷也迁移过去可沿用同一张根证书；否则新服务器会生成新根证书，需要重新导入信任。

### 故障排查

| 现象 | 处理 |
| --- | --- |
| 访问返回 502 | `docker compose ps` 看 apiunit 是否 healthy；`docker compose logs apiunit` |
| 浏览器证书警告 | 导入 `apiunit-root.crt`；确认访问地址与 `SITE_HOST` 完全一致 |
| 登录一直 429 | 等待 15 分钟，或 `docker compose restart apiunit` 清空内存中的限流记录 |
| 上游 Key 全部失效/显示为空 | 主密钥变了或丢失，检查 `secret.key` / `APIUNIT_MASTER_KEY_FILE` |
| 拉取镜像超时 | 按“无法访问 Docker Hub 时”使用镜像站 |
| 8788 被占用 | 修改 `docker-compose.yml` 中 caddy 的宿主机端口映射 |
| 更新后启动失败 | 回滚镜像；必要时恢复数据卷备份 |

## 已有上游与内置限制

- **上游强制 HTTPS**：默认拒绝对公网地址使用明文 HTTP 发送 API Key。仅本机/内网 HTTP 放行。确需放行时设置 `ALLOW_INSECURE_UPSTREAM=true`。
- **登录限流**：同一来源 10 分钟内连续 5 次密码错误，封禁 15 分钟。
- **会话 Cookie**：`HttpOnly` + `SameSite=Strict` + `Secure`，有效期 7 天。
- **文件权限**：进程 umask 为 `077`，数据目录 0700，SQLite 与密钥文件 0600。
- **运行配置加密**：运行配置中可能包含自定义探针请求头凭据，已整体 AES-256-GCM 加密落库。
- **免登录图片路由**：生图检测的图片需要通过公网链接供 Adobe/OpenAI 在线验签抓取，因此该路由不校验登录。链接由不可枚举的运行 ID + 随机文件名构成，按 `RETENTION_DAYS` 自动清理；不要把该链接当作私密存储。

## 换成受信任证书

若后续注册域名并允许放行 80/443，可把 `deploy/Caddyfile` 的站点地址改为 `https://{$SITE_HOST}`、去掉 `tls internal`，并把 compose 的端口映射改为 `"443:443"`（ACME 验证还需要 `"80:80"`），Caddy 会自动申请并续期正式证书。
