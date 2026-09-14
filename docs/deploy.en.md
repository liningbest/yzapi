> English version of [deploy.md](deploy.md). If the two differ, the Chinese file is authoritative.

# Deployment Guide

yzapi is a single binary (the frontend is embedded) and only depends on one data directory at runtime. Four deployment methods are supported, in recommended order:

| Method | Suitable for | Notes |
|---|---|---|
| 1Panel Compose (Docker Compose) | Servers that already run 1Panel | The main path of this guide, see section 3 |
| Docker / Docker Compose | Any Linux host | `docker compose up -d`, see section 4 |
| Binary + systemd | No Docker wanted | Statically compiled, one file, see section 5 |
| Multi-instance + PostgreSQL | High availability | See section 6; this acceptance round did not cover multi-instance metering |

## 1. Before deploying: data and a "clean deployment"

**The repository contains no runtime data, so there is nothing to clean up.** Development and demo data live outside the repository (`/tmp/yzapi-demo`, the smoke scripts' temporary directories); `data/`, `bin/`, `web/dist/` and `dist/` are all excluded by `.gitignore` and `.dockerignore` and never enter the image.

A clean deployment depends only on **whether the data directory on the server is empty**. On first start the program automatically generates inside the data directory:

```
<data directory>/
  data/db/yzapi.db            SQLite database (with -wal / -shm)
  data/journal/calls.jsonl    metering journal and checkpoints
  data/security/credential.key encryption key for upstream API Keys (if lost, no upstream credential can be decrypted)
  data/security/jwt.secret    login token secret
```

It also creates the default user group and the `admin` administrator (the password comes from `YZAPI_INITIAL_ADMIN_PASSWORD`; if unset, one is generated randomly and printed to the log; a password change is forced on first login).

If the server has had a trial run before and you want to start over completely: stop the container, empty the data directory and start again, for example `rm -rf /opt/1panel/apps/yzapi/data/*`. Do not delete only the database while keeping `credential.key`, and not the other way round either: the two are either kept together or deleted together.

## 2. Building the image

The frontend is pre-built on the packaging machine (your computer or GitHub Actions); `docker build` only compiles Go, so the server needs no Node and you will not hit pnpm's compatibility problems on some Docker storage drivers. The final image is about 40 MB and runs as the non-root user `yzapi` (uid 10001). Three ways to obtain the image, pick one:

**A. Build on the server (least effort)**

Package on your local machine (this builds the frontend first, then bundles the source and `web/dist` into one archive):

```bash
./scripts/package-src.sh ~/Desktop/yzapi-src.tar.gz
```

Upload the archive to the server (for example to `/opt/1panel/apps/yzapi` via 1Panel "Files" (文件)), extract and build:

```bash
tar xzf yzapi-src.tar.gz && cd yzapi && docker build -t yzapi/gateway:1.0.0 .
```

You can also do this in 1Panel "Containers → Images → Build" (容器 → 镜像 → 构建): enter the name `yzapi/gateway:1.0.0` and pick the `Dockerfile` in the extracted directory; the result is the same. If you `git clone` directly onto the server and build there, you must first run `cd web && pnpm install && pnpm build` on the server.

**A2. Pre-compiled binary package (preferred for low-memory servers)**

`go build` needs more than 1.5 GB of memory and will freeze a 1 to 2 GB cloud host. Instead cross-compile locally; the server only does a few-second image packaging step:

```bash
./scripts/package-release.sh ~/Desktop/yzapi-release.tar.gz     # contains the static binary, Dockerfile.prebuilt and deploy/
```

On the server:

```bash
tar xzf yzapi-release.tar.gz && cd yzapi && docker build -f Dockerfile.prebuilt -t yzapi/gateway:1.0.0 .
```

The same package can also be used directly for the systemd deployment in section 5; the binary is the `yzapi` file inside it.

**B. Build locally and import** (mind the cross-architecture build on Apple Silicon)

```bash
./scripts/build-image.sh --save          # defaults to linux/amd64, writes dist/yzapi-<version>-amd64.tar.gz
```

Upload that file in 1Panel "Containers → Images → Import" (容器 → 镜像 → 导入).

**C. Automatic build on GitHub (recommended, easiest to upgrade)**

The repository ships [.github/workflows/docker.yml](../.github/workflows/docker.yml): after a push to `main` or a `v*` tag, GitHub Actions automatically builds amd64 / arm64 images and pushes them to `ghcr.io/<username>/<repository>`, tagged `latest`, the version number (e.g. `1.0.0`) and the commit hash. The server needs neither the source nor a compiler:

```bash
docker pull ghcr.io/<username>/<repository>:1.0.0
```

Images of a private repository require a login first: in GitHub "Settings → Developer settings → Personal access tokens" create a token with only the `read:packages` permission, then in 1Panel "Containers → Registries" (容器 → 仓库) add `ghcr.io` with your GitHub username as the user name and that token as the password; or run `docker login ghcr.io` on the server. In the compose file change `image` to the ghcr address; the remaining steps are unchanged.

**D. Push to another image registry**

```bash
IMAGE=registry.example.com/team/yzapi ./scripts/build-image.sh --push
```

Then configure that registry in 1Panel "Containers → Registries" and reference the full image name in the compose file.

## 3. Deploying to 1Panel

### 3.1 Prepare the data directory

```bash
mkdir -p /opt/1panel/apps/yzapi/data
chown -R 10001:10001 /opt/1panel/apps/yzapi
```

The container runs as uid 10001; if the directory is not owned by it, startup fails with `permission denied`.

### 3.2 Create the compose project

1Panel "Containers → Compose → Create Compose" (容器 → 编排 → 创建编排): name `yzapi`, source "Edit" (编辑), paste [deploy/1panel/docker-compose.yml](../deploy/1panel/docker-compose.yml). Adjust as needed:

- `image`: must match the tag from section 2.
- `YZAPI_INITIAL_ADMIN_PASSWORD`: set a strong password; the first login will ask you to change it once more.
- The port is bound to `127.0.0.1:8080` only; HTTPS is provided externally by the reverse proxy. If you do not use a reverse proxy for now, change it to `"8080:8080"` and open port 8080 in the 1Panel firewall.

After creation, check in the compose details that the container status is `healthy` (the health check hits `/health/ready`).

### 3.3 Reverse proxy and HTTPS

1Panel "Websites → Create Website → Reverse Proxy" (网站 → 创建网站 → 反向代理): enter your domain, proxy address `http://127.0.0.1:8080`. After creation open that website's "Config → Reverse Proxy → Edit" (配置 → 反向代理 → 编辑) and replace `location /` with the content of [deploy/1panel/proxy.conf](../deploy/1panel/proxy.conf). Three key points:

- `proxy_buffering off`: otherwise streamed answers are accumulated and only emitted as a whole at the end.
- `proxy_read_timeout 600s`: long answers are not cut off by the 60-second default timeout.
- `client_max_body_size 64m`: multimodal or long-context request bodies.

Then request a Let's Encrypt certificate under "Websites → Certificates" (网站 → 证书) and enable HTTPS with forced redirect on the site.

### 3.4 First login and settings that must be changed

Open `https://<domain>` in the browser, log in as `admin` and change the password. Then:

1. "Settings → Basic" (设置 → 基础): set the access address to `https://<domain>/v1` (the address shown to users in the user center and the model gallery); site name as desired.
2. "Account Pool" (账号池): add upstream provider accounts and test connectivity; "Model Mapping" (模型映射): configure the externally exposed model names.
3. "User Groups / Users" (用户组 / 用户): create business users; users create their own API Keys in the user center.
4. If the server needs a proxy to reach overseas providers, set `YZAPI_HTTP_PROXY` in the compose environment variables and recreate the container.

### 3.5 Verify

```bash
curl -s https://<域名>/health/ready
curl -s https://<域名>/v1/models -H "Authorization: Bearer sk-..."
curl -N https://<域名>/v1/chat/completions -H "Authorization: Bearer sk-..." -H "Content-Type: application/json" \
  -d '{"model":"<映射的模型名>","stream":true,"messages":[{"role":"user","content":"你好"}]}'
```

The third command should stream back token by token; if the whole answer arrives at once, check the buffering settings in 3.3.

### 3.6 Backup

Back up the whole data directory `/opt/1panel/apps/yzapi/data` (database, journal and the two keys are all required). 1Panel "Cron Jobs → Backup Directory" (计划任务 → 备份目录) can archive it on a schedule; SQLite runs in WAL mode, so archiving the directory directly during off-peak hours is fine; if you need an exactly consistent copy, stop the container in the compose project first and then archive.

### 3.7 Upgrade

1. Build / import the new image with a new tag (section 2).
2. In the compose project change `image` to the new tag and click "Update" (更新) or "Recreate" (重建).
3. Leave the data directory untouched. Database migrations run automatically at startup; when the metering aggregation rules change, the hourly aggregates within the retention period are rebuilt once from the detail records (see the metering maintenance section in `docs/api.md`); the log shows `usage rollup upgraded`.

Rollback: change `image` back to the old tag and recreate. Database migrations only add and never drop, so the old version can still read the data.

### 3.8 Locating streaming latency (optional)

The release package ships a fixed-cadence mock upstream and a timeline probe script in `tools/`, used to determine whether streaming latency comes from the gateway, the reverse proxy or the upstream:

```bash
# 1. 在宿主机启动 mock 上游（每 50 ms 一个 chunk），容器通过 docker0 网卡访问它
cd /opt/1panel/apps/yzapi/yzapi/tools && nohup ./mockupstream -addr 0.0.0.0:19911 -delay 50ms >/tmp/mock.log 2>&1 &
ip -4 addr show docker0 | grep inet        # 一般是 172.17.0.1
# 2. 后台「账号池」新增账号：自定义，地址 http://172.17.0.1:19911/v1，Key 任意，映射 mini -> mock-mini
# 3. 用一个 API Key 分别打服务端口和域名
python3 stream-timeline.py http://127.0.0.1:8089/v1 sk-你的Key mini
python3 stream-timeline.py https://你的域名/v1 sk-你的Key mini
```

(Steps in the block: 1. start the mock upstream on the host (one chunk every 50 ms); the container reaches it through the docker0 interface, usually `172.17.0.1`. 2. In the admin "Account Pool" add an account: custom, address `http://172.17.0.1:19911/v1`, any Key, mapping `mini -> mock-mini`. 3. With one API Key hit the service port and the domain separately.)

How to read it: the mock cadence is 50 ms; a `gap p50` close to 50 and `bursty` equal to 0 means no chunk accumulation on that segment. If hitting the port is normal but hitting the domain arrives in batches, it is the reverse proxy buffering; if both are normal and the real upstream is slow, it is the upstream. A high "client write blocked" (客户端写入阻塞) value in the admin call log also points to the reverse proxy or a slow receiving client.

### 3.9 Routine operations

- View logs: compose details → container → logs; or `docker logs -f yzapi`.
- Forgotten admin password: stop the container in the compose project, run `docker run --rm -v /opt/1panel/apps/yzapi/data:/opt/yzapi yzapi/gateway:1.0.0 -reset-password admin -password 'NewStrongPass123'`, then start the container again (SQLite has a single writer; do not reset while the main process is running).
- Metrics: `https://<domain>/metrics` (set `YZAPI_METRICS_TOKEN` and scrape only from the internal network).
- Metering status: after login `GET /api/admin/usage/metering`; watch `overflow_records`, `dirty` and `sync_failures`.

## 4. Docker / Docker Compose (without 1Panel)

```bash
docker run -d --name yzapi --restart unless-stopped \
  -p 8080:8080 -v /opt/yzapi:/opt/yzapi \
  -e YZAPI_INITIAL_ADMIN_PASSWORD='ChangeMe12345!' \
  yzapi/gateway:1.0.0
```

Or run `docker compose up -d` in the repository root ([docker-compose.yml](../docker-compose.yml) builds the image locally, data lives in `./data`). The reverse proxy configuration is the same as 3.3; any Nginx / Caddy works.

## 5. Binary + systemd

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

(The first block builds: cross-compiled on any platform, statically linked. The second block runs on the server.) The unit file is [deploy/systemd/yzapi.service](../deploy/systemd/yzapi.service); the data directory is `/var/lib/yzapi`, it listens on `127.0.0.1:8080` only, and a reverse proxy goes in front just as before.

## 6. Multi-instance + PostgreSQL

```yaml
environment:
  YZAPI_DB_DRIVER: postgres
  YZAPI_DB_DSN: "host=pg user=yzapi password=... dbname=yzapi port=5432 sslmode=disable TimeZone=Asia/Shanghai"
  YZAPI_JWT_SECRET: "<所有实例相同>"
```

(`YZAPI_JWT_SECRET` must be the same on all instances.) All instances share the same `data/security/credential.key` (start one instance first to generate it, then copy it into the data directories of the other instances); a session-independent round-robin on the load balancer in front is sufficient. Limitations: concurrency counting and maintenance mutual exclusion (detail cleanup and aggregate rebuild) are per instance; with multiple instances, divide the concurrency limits by the instance count and avoid running a usage rebuild on several instances at the same time. The metering acceptance scope of this round was single-instance SQLite.

## 7. Environment variables

See the [README](../README.md#配置). The most common ones for deployment: `YZAPI_INITIAL_ADMIN_PASSWORD`, `YZAPI_HTTP_PROXY`, `YZAPI_METRICS_TOKEN`, `YZAPI_JOURNAL_FSYNC`, `YZAPI_LOG_LEVEL`.
