# 部署指南

yzapi 是单个二进制（前端已内嵌），运行时只依赖一个数据目录。支持四种部署方式，按推荐顺序：

| 方式 | 适用 | 说明 |
|---|---|---|
| 1Panel 编排（Docker Compose） | 已有 1Panel 的服务器 | 本文主线，见第 3 节 |
| Docker / Docker Compose | 任意 Linux 主机 | `docker compose up -d`，见第 4 节 |
| 二进制 + systemd | 不想装 Docker | 静态编译，一个文件，见第 5 节 |
| 多实例 + PostgreSQL | 高可用 | 见第 6 节；本轮验收未覆盖多实例计量 |

## 1. 部署前：数据与"干净部署"

**仓库里没有任何运行数据，不需要清理。** 开发和演示的数据都在仓库外（`/tmp/yzapi-demo`、冒烟脚本的临时目录）；`data/`、`bin/`、`web/dist/`、`dist/` 均被 `.gitignore` 和 `.dockerignore` 排除，不会进入镜像。

干净部署只取决于**服务器上的数据目录是否为空**。首次启动时，程序在数据目录里自动生成：

```
<数据目录>/
  data/db/yzapi.db            SQLite 数据库（含 -wal / -shm）
  data/journal/calls.jsonl    计量 journal 与检查点
  data/security/credential.key 上游 API Key 的加密密钥（丢失则所有上游凭据不可解密）
  data/security/jwt.secret    登录令牌密钥
```

并创建默认用户组和 `admin` 管理员（密码来自 `YZAPI_INITIAL_ADMIN_PASSWORD`，未设置则随机生成并打印到日志；首次登录强制改密）。

如果服务器上曾经试运行过、想彻底重来：停掉容器，清空数据目录再启动即可，例如 `rm -rf /opt/1panel/apps/yzapi/data/*`。不要只删数据库而保留 `credential.key`，反过来也不要——两者要么一起留，要么一起删。

## 2. 构建镜像

前端在打包机（你的电脑或 GitHub Actions）上预先构建，`docker build` 只编译 Go，因此服务器上不需要 Node，也不会碰到 pnpm 在部分 Docker 存储驱动上的兼容问题。最终镜像约 40 MB，以非 root 用户 `yzapi`（uid 10001）运行。三种取得镜像的方式，选一种：

**A. 在服务器上构建（最省事）**

在本机打包（会先构建前端，再把源码和 `web/dist` 打成一个包）：

```bash
./scripts/package-src.sh ~/Desktop/yzapi-src.tar.gz
```

把包上传到服务器（如 1Panel「文件」的 `/opt/1panel/apps/yzapi`），解压后构建：

```bash
tar xzf yzapi-src.tar.gz && cd yzapi && docker build -t yzapi/gateway:1.0.0 .
```

也可以在 1Panel「容器 → 镜像 → 构建」里填名称 `yzapi/gateway:1.0.0`、Dockerfile 选择解压目录中的 `Dockerfile`，效果相同。直接 `git clone` 到服务器再构建则需要先在服务器上 `cd web && pnpm install && pnpm build`。

**A2. 预编译二进制包（小内存服务器首选）**

`go build` 需要 1.5 GB 以上内存，1 到 2 GB 的云主机上会把系统卡死。改为在本机交叉编译，服务器只做一次几秒钟的镜像打包：

```bash
./scripts/package-release.sh ~/Desktop/yzapi-release.tar.gz     # 含静态二进制、Dockerfile.prebuilt、deploy/
```

服务器上：

```bash
tar xzf yzapi-release.tar.gz && cd yzapi && docker build -f Dockerfile.prebuilt -t yzapi/gateway:1.0.0 .
```

同一个包也可直接用于第 5 节的 systemd 部署，二进制就是里面的 `yzapi`。

**B. 本机构建后导入**（Apple Silicon 注意跨架构）

```bash
./scripts/build-image.sh --save          # 默认 linux/amd64，输出 dist/yzapi-<版本>-amd64.tar.gz
```

在 1Panel「容器 → 镜像 → 导入」上传该文件。

**C. GitHub 自动构建（推荐，升级最省事）**

仓库自带 [.github/workflows/docker.yml](../.github/workflows/docker.yml)：推送到 `main` 或打 `v*` 标签后，GitHub Actions 自动构建 amd64 / arm64 镜像并推送到 `ghcr.io/<用户名>/<仓库名>`，标签有 `latest`、版本号（如 `1.0.0`）和提交哈希。服务器上不需要源码，也不需要编译：

```bash
docker pull ghcr.io/<用户名>/<仓库名>:1.0.0
```

私有仓库的镜像需要先登录：GitHub「Settings → Developer settings → Personal access tokens」创建一个只有 `read:packages` 权限的 token，在 1Panel「容器 → 仓库」添加 `ghcr.io`，用户名为 GitHub 用户名、密码为该 token；或在服务器执行 `docker login ghcr.io`。编排里把 `image` 改为 ghcr 地址即可，其余步骤不变。

**D. 推送到其他镜像仓库**

```bash
IMAGE=registry.example.com/team/yzapi ./scripts/build-image.sh --push
```

然后在 1Panel「容器 → 仓库」配置该仓库，编排里引用完整镜像名。

## 3. 部署到 1Panel

### 3.1 准备数据目录

```bash
mkdir -p /opt/1panel/apps/yzapi/data
chown -R 10001:10001 /opt/1panel/apps/yzapi
```

容器以 uid 10001 运行，目录不归它所有会在启动时报 `permission denied`。

### 3.2 创建编排

1Panel「容器 → 编排 → 创建编排」，名称 `yzapi`，来源选"编辑"，粘贴 [deploy/1panel/docker-compose.yml](../deploy/1panel/docker-compose.yml)。按需修改：

- `image`：与第 2 节的标签一致。
- `YZAPI_INITIAL_ADMIN_PASSWORD`：设一个强密码，首次登录会要求再改一次。
- 端口只绑定 `127.0.0.1:8080`，对外由反向代理提供 HTTPS；如果暂时不用反向代理，改为 `"8080:8080"` 并在 1Panel 防火墙放行 8080。

创建后在编排详情看容器状态为 `healthy`（健康检查打 `/health/ready`）。

### 3.3 反向代理与 HTTPS

1Panel「网站 → 创建网站 → 反向代理」：域名填你的域名，代理地址 `http://127.0.0.1:8080`。创建后进入该网站「配置 → 反向代理 → 编辑」，把 `location /` 换成 [deploy/1panel/proxy.conf](../deploy/1panel/proxy.conf) 的内容。关键三点：

- `proxy_buffering off`：否则流式回答会整段攒到结束才吐出。
- `proxy_read_timeout 600s`：长回答不被 60 秒默认超时切断。
- `client_max_body_size 64m`：多模态或长上下文请求体。

然后在「网站 → 证书」申请 Let's Encrypt 证书并在站点开启 HTTPS 与强制跳转。

### 3.4 首次登录与必改设置

浏览器打开 `https://<域名>`，用 `admin` 登录并改密。然后：

1. 「设置 → 基础」：接入地址填 `https://<域名>/v1`（用户中心和模型广场展示给使用者的地址）；站点名称按需。
2. 「账号池」添加上游供应商账号并测试连通；「模型映射」配置对外模型名。
3. 「用户组 / 用户」创建业务用户，用户在用户中心自助创建 API Key。
4. 如果服务器访问境外供应商需要代理，在编排环境变量设 `YZAPI_HTTP_PROXY` 后重建容器。

### 3.5 验证

```bash
curl -s https://<域名>/health/ready
curl -s https://<域名>/v1/models -H "Authorization: Bearer sk-..."
curl -N https://<域名>/v1/chat/completions -H "Authorization: Bearer sk-..." -H "Content-Type: application/json" \
  -d '{"model":"<映射的模型名>","stream":true,"messages":[{"role":"user","content":"你好"}]}'
```

第三条应逐字流式返回；如果一次性整段返回，检查 3.3 的缓冲设置。

### 3.6 备份

备份整个数据目录 `/opt/1panel/apps/yzapi/data`（数据库、journal、两个密钥缺一不可）。1Panel「计划任务 → 备份目录」可以定时打包；SQLite 处于 WAL 模式，低峰期直接打包目录即可，要求完全一致时先在编排里停止容器再打包。

### 3.7 升级

1. 用新标签构建 / 导入新镜像（第 2 节）。
2. 编排里把 `image` 改为新标签，点「更新」或「重建」。
3. 数据目录不动。启动时自动执行数据库迁移；计量聚合口径变化时会一次性按明细重建保留期内的小时聚合（见 `docs/api.md` 计量维护一节），日志里能看到 `usage rollup upgraded`。

回滚：把 `image` 改回旧标签重建。数据库迁移只增不删，旧版本仍能读取。

### 3.8 流式延迟定位（可选）

发布包的 `tools/` 里带了固定节奏的 mock 上游和时间线探测脚本，用来判断流式延迟出在网关、反向代理还是上游：

```bash
# 1. 在宿主机启动 mock 上游（每 50 ms 一个 chunk），容器通过 docker0 网卡访问它
cd /opt/1panel/apps/yzapi/yzapi/tools && nohup ./mockupstream -addr 0.0.0.0:19911 -delay 50ms >/tmp/mock.log 2>&1 &
ip -4 addr show docker0 | grep inet        # 一般是 172.17.0.1
# 2. 后台「账号池」新增账号：自定义，地址 http://172.17.0.1:19911/v1，Key 任意，映射 mini -> mock-mini
# 3. 用一个 API Key 分别打服务端口和域名
python3 stream-timeline.py http://127.0.0.1:8089/v1 sk-你的Key mini
python3 stream-timeline.py https://你的域名/v1 sk-你的Key mini
```

读法：mock 的节奏是 50 ms，`gap p50` 接近 50 且 `bursty` 为 0 说明这一段没有攒块；打端口正常、打域名成批到达，就是反向代理缓冲；两者都正常而真实上游慢，就是上游。后台调用日志里的"客户端写入阻塞"偏大，同样指向反向代理或客户端接收慢。

### 3.9 常用运维

- 查看日志：编排详情 → 容器 → 日志；或 `docker logs -f yzapi`。
- 忘记管理员密码：在编排里停止容器后执行 `docker run --rm -v /opt/1panel/apps/yzapi/data:/opt/yzapi yzapi/gateway:1.0.0 -reset-password admin -password 'NewStrongPass123'`，再启动容器（SQLite 单写者，主进程运行时不要重置）。
- 指标：`https://<域名>/metrics`（建议设 `YZAPI_METRICS_TOKEN` 并只在内网抓取）。
- 计量状态：登录后 `GET /api/admin/usage/metering`，关注 `overflow_records`、`dirty`、`sync_failures`。

## 4. Docker / Docker Compose（无 1Panel）

```bash
docker run -d --name yzapi --restart unless-stopped \
  -p 8080:8080 -v /opt/yzapi:/opt/yzapi \
  -e YZAPI_INITIAL_ADMIN_PASSWORD='ChangeMe12345!' \
  yzapi/gateway:1.0.0
```

或在仓库根目录 `docker compose up -d`（[docker-compose.yml](../docker-compose.yml) 会本地构建镜像，数据在 `./data`）。反向代理配置与 3.3 相同，任何 Nginx / Caddy 都可以。

## 5. 二进制 + systemd

```bash
# 构建（任意平台交叉编译，静态链接）
cd web && pnpm install && pnpm build && cd ..
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$(git describe --always)" -o yzapi ./cmd/yzapi

# 服务器
useradd -r -s /usr/sbin/nologin yzapi
install -m 0755 yzapi /usr/local/bin/yzapi
mkdir -p /var/lib/yzapi && chown yzapi:yzapi /var/lib/yzapi
install -m 0644 deploy/systemd/yzapi.service /etc/systemd/system/yzapi.service
systemctl daemon-reload && systemctl enable --now yzapi
journalctl -u yzapi -n 50
```

单元文件见 [deploy/systemd/yzapi.service](../deploy/systemd/yzapi.service)，数据目录 `/var/lib/yzapi`，只监听 `127.0.0.1:8080`，前面同样放反向代理。

## 6. 多实例 + PostgreSQL

```yaml
environment:
  YZAPI_DB_DRIVER: postgres
  YZAPI_DB_DSN: "host=pg user=yzapi password=... dbname=yzapi port=5432 sslmode=disable TimeZone=Asia/Shanghai"
  YZAPI_JWT_SECRET: "<所有实例相同>"
```

所有实例共享同一个 `data/security/credential.key`（先启动一个实例生成，再复制到其余实例的数据目录），前置负载均衡开启会话无关的轮询即可。限制：并发计数、维护互斥（明细清理与聚合重建）都是实例内的，多实例时按实例数折算并发上限，并避免在多个实例同时做用量重建。本轮计量验收范围为单实例 SQLite。

## 7. 环境变量

见 [README](../README.md#配置)。部署最常用的：`YZAPI_INITIAL_ADMIN_PASSWORD`、`YZAPI_HTTP_PROXY`、`YZAPI_METRICS_TOKEN`、`YZAPI_JOURNAL_FSYNC`、`YZAPI_LOG_LEVEL`。
