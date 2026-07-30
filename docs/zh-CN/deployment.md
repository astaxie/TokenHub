# 部署

Language: [English](../deployment.md) | 简体中文 | [日本語](../ja/deployment.md)

TokenHub 面向私有化部署，由 Go 后端、Next.js 管理后台和 SQLite 持久化组成。

## 数据库选择

TokenHub 支持两种数据库后端：

### SQLite（默认）

**优点：**
- 零配置，无需单独的数据库服务
- 适合中小规模部署
- 备份简单（直接复制文件）

**适用场景：**
- 开发和测试环境
- 少于 1000 用户的部署
- 单机部署

**部署：**

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml up -d --remove-orphans
```

### PostgreSQL（推荐用于生产）

**优点：**
- 企业级数据库，适合高并发场景
- 更好的事务支持和数据完整性
- 支持复制和高可用

**适用场景：**
- 生产环境
- 超过 1000 用户的部署
- 高可用需求

**部署：**

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.postgres.yml up -d --remove-orphans
```

PostgreSQL 的详细配置见 [PostgreSQL 设置指南](../postgresql-setup.md)。

### 使用远端 PostgreSQL 的多实例部署

默认安装使用 SQLite 启动一个前端实例和一个后端实例。需要横向扩容且数据库由 Compose 项目之外的平台托管时，使用 `deploy/docker-compose.remote-postgres.yml`。该配置在可扩容的前后端服务前提供 Nginx 网关，并且不会启动本地数据库。

```mermaid
flowchart TB
    clients["客户端<br/>管理后台 · OpenAI SDK"] --> nginx["Nginx 网关<br/>负载均衡 · 健康检查"]
    nginx --> frontend["前端副本 × N"]
    frontend --> backend["后端副本 × N"]
    backend <--> providers["模型 Provider"]

    local["data/model-catalog.yaml<br/>候选模型元数据"] -->|"启动时解析并写入候选模板<br/>集群租约串行化各副本"| backend
    providerCatalog["data/provider-catalog.json<br/>受版本控制的 Provider 模板与候选模型"] -->|"管理员新建或刷新 Provider"| backend
    backend <-->|"模型 · 路由 · Provider 目录快照<br/>共享状态 · 数据库锁"| postgres[("共享 PostgreSQL")]

    backend -->|"创建 Provider"| rule["路由创建规则<br/>显式开启候选模型 → 写入 Model → Route<br/>自动候选模型 ∩ 本地 Model → Route"]
    local -.-> rule
    providerCatalog -.-> rule
    rule -->|"创建匹配的 Route"| postgres
```

多实例模式下：

- Nginx 将管理后台、API 和健康检查流量负载均衡到健康副本。
- 后端副本将持久化配置、OAuth 会话、配额计数、审计数据、集群锁和请求并发租约统一存储在 PostgreSQL 中。
- 租约过期和归属判断使用 PostgreSQL 时钟，避免不同宿主机的时钟偏差导致租约被提前接管；失去租约后，心跳会取消对应任务或请求。
- 每个后端启动时都会同步当前配置目录中的候选模型元数据，并通过集群租约串行执行幂等同步。
- Provider 模板和候选模型从仓库中受版本控制的本地目录读取，运行时不依赖远端目录服务。
- 后端会将本地 Provider 目录快照持久化到 PostgreSQL，使各副本使用同一目录；本地文件缺失时则回退至已写入数据库的内置模板。
- 数据库协调故障只释放 Provider 容量，不会把健康的模型 Provider 错误计为失败。

在 `deploy/.env` 中配置远端 `TOKENHUB_DATABASE_URL`、公网网关地址、生产密钥、可信代理 CIDR，以及所需的 `TOKENHUB_BACKEND_REPLICAS` 和 `TOKENHUB_FRONTEND_REPLICAS` 后运行：

```bash
docker compose --env-file deploy/.env \
  -f deploy/docker-compose.remote-postgres.yml up -d
```

所有实例必须使用相同的 `TOKENHUB_SECRET_KEY`。`TOKENHUB_DB_MAX_OPEN_CONNS` 是单实例连接数，需要确保所有实例的连接池总和低于 PostgreSQL 限制。不得让多个后端实例共享 SQLite 文件。

使用 `./deploy/test-multi-instance.sh` 运行真实的双实例 PostgreSQL E2E 测试。

## 原生 Release + systemd

单机 Linux 使用 systemd 时，可以选择原生 Release 安装方式。原生安装包支持 `linux/amd64` 和 `linux/arm64`，其中包含 Go 后端、独立运行的 Next.js 管理后台和匹配的 Node.js 运行时。

下载安装脚本并检查内容，然后安装最新稳定版：

```bash
curl -fsSL https://raw.githubusercontent.com/astaxie/TokenHub/main/deploy/native/install.sh \
  -o /tmp/tokenhub-install.sh
sudo bash /tmp/tokenhub-install.sh install
```

未设置 `TOKENHUB_PUBLIC_HOST` 时，安装器会请求 `https://ipinfo.io/json` 并使用其中通过校验的 IP；查询失败后依次回退到 `hostname -I` 返回的第一个地址和 `127.0.0.1`。如果服务器位于 NAT、代理或负载均衡器之后，检测到的出口 IP 可能不是用户访问的入口地址，此时应显式设置 `TOKENHUB_PUBLIC_HOST`。使用 IPv6 字面地址时，安装脚本会在生成 URL 时自动添加方括号：

```bash
sudo env TOKENHUB_PUBLIC_HOST=tokenhub.example.com \
  bash /tmp/tokenhub-install.sh install
```

安装器会把最终使用的主机地址写入 `/etc/tokenhub/tokenhub.env`，后续执行升级等操作时会继续使用该值，避免自动 IP 检测结果变化导致输出地址漂移。

如果希望首次启动就使用 PostgreSQL，而不是默认的 SQLite，请在第一次安装时传入数据库 URL：

```bash
sudo env \
  TOKENHUB_DATABASE_URL='postgres://user:password@db.example.com:5432/tokenhub?sslmode=require' \
  bash /tmp/tokenhub-install.sh install
```

安装器只会在首次创建配置时将该值写入 `/etc/tokenhub/tokenhub.env`。后续执行 install、upgrade 或 rollback 会保留已有配置；确需切换数据库时，请编辑该文件并重启 TokenHub。

首次安装会生成生产密钥和初始管理员密码，密码只会输出一次。运行文件分别保存在：

- Release 和 `current` 软链接：`/opt/tokenhub`
- 配置与密钥：`/etc/tokenhub/tokenhub.env`
- SQLite 数据库与备份：`/var/lib/tokenhub`
- 生成图片：`/var/lib/tokenhub/images`
- Linux systemd 单元：`/etc/systemd/system/tokenhub.service`

需要修改公网地址、CORS Origin、端口、数据库或密钥时，编辑 `/etc/tokenhub/tokenhub.env`，然后重启服务：

```bash
sudo systemctl restart tokenhub
sudo systemctl status tokenhub
sudo journalctl -u tokenhub -f
```

安装脚本会先使用 `checksums.txt` 校验 Release 压缩包，再激活版本；升级时会保留配置和数据：

```bash
sudo bash /tmp/tokenhub-install.sh upgrade
sudo bash /tmp/tokenhub-install.sh upgrade --version 0.3.3
sudo bash /tmp/tokenhub-install.sh rollback --version 0.3.2
sudo bash /tmp/tokenhub-install.sh uninstall
```

`upgrade` 会拒绝低于当前安装版本的目标；需要降级时必须显式使用 `rollback`。使用新版安装器升级旧安装时，如果尚未配置 `TOKENHUB_IMAGE_STORAGE_DIR`，安装器会自动将持久化图片目录补充为 `/var/lib/tokenhub/images`。

`uninstall` 会保留 `/etc/tokenhub` 和 `/var/lib/tokenhub`。只有确定要同时删除配置和应用数据时，才使用 `uninstall --purge`。
安装器会在应用、配置和状态目录中写入所有权标记。卸载时，如果目录没有标记或标记与当前配置不一致，安装器会拒绝递归删除；`/opt`、`/etc`、`/var/lib` 等系统级目录也不会被接受为托管目录。首次安装会拒绝相同的前后端端口；如果系统提供 `ss` 或 `lsof`，脚本还会在下载 Release 前拒绝已被占用的端口。安装或升级只有在 systemd 服务处于 active 状态、后端健康检查和管理后台都可访问后才会报告成功；就绪检查失败时会同时输出近期服务日志。

使用 fork 测试时，请下载该 fork 中的安装脚本，并指定它的公开 Release 仓库：

```bash
sudo env TOKENHUB_RELEASE_REPOSITORY=your-account/TokenHub \
  bash /tmp/tokenhub-install.sh install --version 0.3.3
```

原生 Release 安装会在版本面板中显示为「原生 Release」。管理员可以直接在页面下载并校验更新或回退版本，然后点击「立即重启」，由 systemd 激活目标版本。每个 GitHub Release 标签必须是以 `v` 开头的严格语义化版本，并包含 Linux 压缩包和 `checksums.txt`；发布 Release 后，`.github/workflows/native-release.yml` 会构建并附加 `linux/amd64` 和 `linux/arm64` 文件。
已经下载并校验通过的版本会保留在本地，即使 GitHub Releases API 暂时不可访问，也可以继续回退。

## Docker Compose

创建部署环境变量文件：

```bash
cp deploy/.env.example deploy/.env
```

启动前请编辑 `deploy/.env`：

- `TOKENHUB_ADMIN_TOKEN`：Admin API 启动 Token，请使用至少 32 字节的随机值。
- `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`：仅用于创建初始 `admin` 用户，请设置至少 12 字节的密码。
- `TOKENHUB_SECRET_KEY`：后端密钥，请使用至少 32 字节的随机值并保持稳定。
- `TOKENHUB_IMAGE_TAG`：托管 TokenHub 镜像标签，默认 `latest`。
- `TOKENHUB_PUBLIC_BASE_URL`：展示给用户的后端访问地址。
- `TOKENHUB_API_BASE_URL`：浏览器管理后台访问后端的地址，由前端服务在运行时读取。旧变量 `NEXT_PUBLIC_API_BASE_URL` 保留一个兼容周期，作为回退配置。
- `TOKENHUB_BACKEND_PORT`：后端宿主机端口，默认 `8080`。
- `TOKENHUB_FRONTEND_PORT`：管理后台宿主机端口，默认 `3000`。
- `TOKENHUB_BACKEND_REPLICAS`：远端 PostgreSQL Compose 的后端副本数，默认 `2`。
- `TOKENHUB_FRONTEND_REPLICAS`：远端 PostgreSQL Compose 的前端副本数，默认 `2`。

在仓库根目录启动：

```bash
./deploy/install.sh
```

脚本会先校验 Compose 环境变量，再拉取已发布镜像并启动托管应用容器，不在部署服务器构建镜像；只有在最多等待 180 秒且 Compose 健康检查通过后才报告成功。从旧的双容器结构升级时，脚本会移除已废弃的独立前端容器，但保留 `tokenhub-data` 数据卷。首次发布 GHCR 镜像期间，如果镜像无法拉取，脚本会自动改为从当前代码构建。校验失败时会列出不安全的变量，但不会输出敏感值。新后端启动失败或未能进入健康状态时，脚本会打印本次启动产生的最多 100 行日志。

只校验配置，不拉取镜像或启动容器：

```bash
./deploy/install.sh --check-only
```

使用其他环境文件时，可执行 `./deploy/install.sh --env-file /path/to/deploy.env`。

### 已发布镜像的版本规则

GitHub Actions 为 `linux/amd64` 和 `linux/arm64` 发布完整的 `ghcr.io/astaxie/tokenhub-backend` 镜像。镜像名称为兼容旧部署而保留，其中实际包含后端、独立 Next.js 管理后台、Node.js 运行时和容器监督进程。

- 发布带有严格 `v` 前缀语义化标签的 GitHub Release 后，自动构建对应的纯数字镜像标签；非预发布版本同时更新主次版本标签和 `latest`。
- `workflow_dispatch` 仅允许发布 `edge` 或独立的 `manual-*` 标签，不能覆盖正式版本标签或 `latest`。
- PR 不构建或推送容器镜像。
- 合并到 `main` 不发布镜像。

工作流先使用本次运行专用的暂存标签推送并验证多平台镜像，再发布最终标签。生产部署建议固定完整版本标签，不依赖持续变化的 `latest`。

GHCR 首次发布产生的 Package 默认为私有。开放匿名部署前，仓库所有者需要将该 Package 调整为 Public。在此之前，使用默认 `latest` 标签的安装会在拉取失败后自动改为从本地源码构建。如果显式配置的 `TOKENHUB_IMAGE_TAG` 无法拉取，安装脚本会直接退出，不会把当前源码标记成该版本。

### Docker 版本状态与回退

平台管理员可以点击 TokenHub 标志下方的版本胶囊，查看当前运行版本、检查最新的 GitHub 正式 Release，并列出最多 3 个更早的稳定版本。正式镜像构建会从发布工作流获得精确版本号；本地源码构建使用项目包版本，并明确标记为源码构建。托管更新、回退和重启请求都会写入管理员审计日志。

版本检查会通过限时的出站 HTTPS 请求访问公开的 GitHub Releases API，并将成功结果缓存 20 分钟。默认检查 `astaxie/TokenHub`；维护者可以将 `TOKENHUB_RELEASE_REPOSITORY` 设置为其他可信的公开 `owner/repository`，用于 fork 发布验证。GitHub 故障或仓库尚无 Release 不会影响网关流量；面板会展示不可用状态，同时保留当前版本信息。

例如，在源码部署中检查 fork 的 Release：

```bash
TOKENHUB_RELEASE_REPOSITORY=your-account/TokenHub ./start.sh
```

默认 SQLite 和本地 PostgreSQL Compose 使用一个托管应用容器。管理员可以点击「立即更新」，等待系统下载、校验并将当前平台的完整 Release 包安装到 `tokenhub-releases` 卷，然后点击「立即重启」。接口返回成功后进程主动退出，Docker 的 `restart: unless-stopped` 会同时以目标版本重新启动后端和前端。容器不会挂载 Docker Socket，也不会控制宿主机 Docker daemon。

新拉取的镜像首次使用该卷时，镜像版本和内容指纹共同构成基线。页面安装的版本、`current` 链接和历史 Release 都保存在 `tokenhub-releases`，因此使用同一镜像进行普通重启或重建容器不会丢失更新结果；拉取其他镜像，或者在相同版本下重新构建了不同源码时，会激活新的镜像内容。远端 PostgreSQL 多实例 Compose 禁用原地更新，因为只更新收到管理员请求的单个副本会造成集群版本分裂；该模式提示管理员使用原来的 Compose 文件和环境配置手工更新，以保留已配置的副本数。源码部署仍提示手工更新。回退前必须完成数据库备份，并确认目标版本支持当前数据库结构。

### 可选：本地构建

需要从当前代码构建镜像时执行：

```bash
./deploy/install.sh --build
```

以下加速配置仅适用于本地源码构建。

项目 Dockerfile 不写死区域性的包镜像源。如果服务器访问 Docker Hub、npm 或 Go Module 源较慢，请优先在部署服务器上配置加速，而不是修改 Dockerfile。

对于基础镜像拉取，可在服务器 Docker daemon 中配置镜像加速，例如 `/etc/docker/daemon.json`，然后重启 Docker：

```json
{
	"registry-mirrors": [
		"https://<your-docker-registry-mirror>"
	]
}
```

对于镜像构建阶段的依赖下载，建议在服务器上为 Docker 或 BuildKit 配置 HTTP/HTTPS 出口代理。这样可以保持构建可移植，避免把特定环境的 npm 或 Go 代理配置提交到仓库。

如果部署环境直接访问上游源较慢，可以参考以下服务器侧配置示例：

```bash
# Go Module 下载
go env -w GOPROXY=https://goproxy.cn,direct

# npm 包下载
npm config set registry https://registry.npmmirror.com
```

这些命令用于配置服务器或构建环境。除非明确维护特定环境的分支，否则不要直接写入项目 Dockerfile。

Compose 会启动：

- 后端：`http://localhost:8080`
- 前端：`http://localhost:3000`
- SQLite 数据：保存在 Docker named volume `tokenhub-data`
- 模型目录：使用所选后端镜像中内置的版本

查看状态：

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml ps
```

首次登录后台：

- 用户名：`admin`
- 密码：配置的 `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`

在 `prod`、`production`、预发布等非开发环境中，服务会拒绝占位值、少于 32 字节的 Admin Token 或后端密钥，以及少于 12 字节的初始密码。

手动查看或持续跟踪日志：

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml logs -f
```

停止服务：

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml down
```

停止并删除 SQLite 数据卷：

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml down -v
```

仅在明确需要删除本地数据时使用 `down -v`。

## 后端环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `TOKENHUB_ENV` | `prod` | 运行环境标识 |
| `TOKENHUB_HTTP_ADDR` | `:8080` | 后端监听地址 |
| `TOKENHUB_PUBLIC_BASE_URL` | `http://localhost:8080` | 展示给用户的后端地址 |
| `TOKENHUB_RELEASE_REPOSITORY` | `astaxie/TokenHub` | 版本检查使用的可信公开 GitHub 仓库，格式为 `owner/repository` |
| `TOKENHUB_DEPLOYMENT_TYPE` | 编译期取值 | 覆盖二进制中编译的部署类型：`source`、`container` 或 `native`。Compose 文件设置为 `container` |
| `TOKENHUB_MANAGED_UPDATES` | `false` | 允许容器部署执行在线更新与回退；原生部署始终允许 |
| `TOKENHUB_INSTALL_ROOT` | `/opt/tokenhub` | 托管 Release 在线更新与回退使用的安装根目录 |
| `TOKENHUB_TRUSTED_PROXY_CIDRS` | 空 | 允许提供 `X-Forwarded-For` 的代理 IP 或 CIDR，逗号分隔 |
| `TOKENHUB_CORS_ALLOWED_ORIGINS` | 公网地址 | 允许调用后端的浏览器 Origin，逗号分隔 |
| `TOKENHUB_ADMIN_TOKEN` | `change-me-tokenhub-admin-token` | Admin API 启动访问 Token |
| `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD` | `change-me-tokenhub-admin-password` | 初始 `admin` 用户密码；生产启动前必须修改 |
| `TOKENHUB_SECRET_KEY` | `change-me-tokenhub-secret-key` | 后端密钥 |
| `TOKENHUB_DATABASE_URL` | `sqlite:///app/data/tokenhub.db` | 容器内 SQLite 数据库路径 |
| `TOKENHUB_DB_HOST` | 空 | PostgreSQL 主机。设置后改用 `TOKENHUB_DB_*` 各字段拼装 DSN，而不是 `TOKENHUB_DATABASE_URL`，可避免密码含 `#`、`?`、`/`、`%` 时的 URL 编码问题。两者同时设置时仍以 `TOKENHUB_DATABASE_URL` 优先 |
| `TOKENHUB_DB_PORT` | `5432` | PostgreSQL 端口；仅在设置了 `TOKENHUB_DB_HOST` 时生效 |
| `TOKENHUB_DB_USER` | 空 | PostgreSQL 用户名；仅在设置了 `TOKENHUB_DB_HOST` 时生效 |
| `TOKENHUB_DB_PASSWORD` | 空 | PostgreSQL 密码；仅在设置了 `TOKENHUB_DB_HOST` 时生效 |
| `TOKENHUB_DB_NAME` | 空 | PostgreSQL 数据库名；仅在设置了 `TOKENHUB_DB_HOST` 时生效 |
| `TOKENHUB_DB_SSLMODE` | `disable` | PostgreSQL sslmode；仅在设置了 `TOKENHUB_DB_HOST` 时生效 |
| `TOKENHUB_SQLITE_BACKUP_DIR` | `/app/data/backups` | 备份目录 |
| `TOKENHUB_MODEL_CATALOG_FILE` | `/opt/tokenhub/current/catalog/model-catalog.yaml` | 托管部署中的标准模型目录文件 |
| `TOKENHUB_PROVIDER_CATALOG_FILE` | `/opt/tokenhub/current/catalog/provider-catalog.json` | 托管部署中的 Provider 模板与候选模型目录文件 |
| `TOKENHUB_SEED_DEMO` | `false` | 是否写入演示数据 |
| `TOKENHUB_RESOURCE_FAILURE_THRESHOLD` | `3` | Provider 资源进入冷却前的失败阈值 |
| `TOKENHUB_RESOURCE_COOLDOWN_SECONDS` | `300` | Provider 资源进入冷却后获得半开重试前的基础等待秒数 |
| `TOKENHUB_RESOURCE_COOLDOWN_MAX_SECONDS` | `3600` | 反复恢复失败时指数退避的上限秒数 |
| `TOKENHUB_METRICS_ENABLED` | `false` | 采集 Prometheus 指标并提供 `GET /metrics` |
| `TOKENHUB_METRICS_TOKEN` | 空 | `/metrics` 的 Bearer 令牌；留空时回落到管理员令牌 |
| `TOKENHUB_METRICS_PROJECT_LABEL` | `false` | 为网关指标添加 `project_id` 标签，会按项目数放大时间序列数量 |
| `TOKENHUB_IN_FLIGHT_LEASE_TTL_SECONDS` | `300` | 集群并发租约的过期时间及续租周期基准 |
| `TOKENHUB_CLUSTER_LOCK_TTL_SECONDS` | `180` | 集群协调锁的过期时间及续租周期基准 |
| `TOKENHUB_GRACEFUL_SHUTDOWN_SECONDS` | `150` | 停机时等待在途请求完成的最长秒数 |
| `TOKENHUB_STOP_GRACE_PERIOD` | `180s` | Docker 强制停止后端前的 Compose 宽限时间 |
| `TOKENHUB_CACHE_AFFINITY_ENABLED` | `false` | 将同一会话固定到同一个上游账号，使上游 prompt cache 持续命中。默认关闭，因为它会改变路由行为 |
| `TOKENHUB_CACHE_AFFINITY_MODELS` | 空 | 逗号分隔的模型灰度名单；留空表示对全部模型生效 |
| `TOKENHUB_CACHE_AFFINITY_ALLOW_USER_SCOPE` | `false` | 是否接受用户级标识作为亲和键。默认关闭，因为同一用户的并发会话会共享取值、全部落到同一个账号 |
| `TOKENHUB_IMAGE_STORAGE_DIR` | `data/images` | 生成图片资产的存放目录 |
| `TOKENHUB_IMAGE_WORKER_CONCURRENCY` | `2` | 消费图片生成队列的工作协程数量 |
| `TOKENHUB_IMAGE_QUEUE_CAPACITY` | `64` | 队列中允许排队的图片任务上限 |
| `TOKENHUB_IMAGE_JOB_TIMEOUT_SECONDS` | `300` | 单个图片生成任务的超时时间，超时判定为失败 |
| `TOKENHUB_IMAGE_CAPABILITY_RETRY_SECONDS` | `86400` | 被标记为不支持图片生成的供应商资源，隔多久重新探测一次 |

## 前端环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `TOKENHUB_API_BASE_URL` | `http://localhost:8080` | 前端服务在运行时读取的后端 Admin API 地址 |
| `NEXT_PUBLIC_API_BASE_URL` | 空 | 已弃用的兼容回退配置，需要迁移到 `TOKENHUB_API_BASE_URL` |

## 数据和备份

SQLite 是项目、Key、Provider、路由、用户、请求日志、用量、告警、审批、会话和备份记录的持久化来源。

在一键 compose 部署中：

- 容器内数据库路径：`/app/data/tokenhub.db`
- 容器内备份路径：`/app/data/backups`
- Docker volume 名称：`tokenhub-data`

生产建议：

- 将 SQLite 数据库放在持久化磁盘上。
- 将备份保存到应用容器外部。
- 按保留策略清理旧备份。
- 将 Provider 凭证和 Admin Token 放在密钥管理系统或受保护的环境变量中。

## 目录文件

发布的托管镜像和原生安装包都包含对应版本的 `data/model-catalog.yaml` 与 `data/provider-catalog.json`。它们会随其余 Release 一起激活到 `/opt/tokenhub/current/catalog/`，确保后端程序和两类目录来自同一版本。Provider 目录由 PublicProviderConf 数据迁入并随仓库维护，TokenHub 运行时不会拉取远端目录数据。

需要使用自定义模型目录时，显式指定挂载文件：

```bash
./deploy/install.sh --model-catalog /absolute/path/to/model-catalog.yaml
```

自定义文件会覆盖镜像内的候选模板目录，其版本需要与 `TOKENHUB_IMAGE_TAG` 分别管理。更新文件后，重启后端容器，并在管理后台「模型目录」的「候选模板库」页签确认结果。

更新当前配置的目录文件后，可以重启后端，也可以在管理后台「模型目录」的「候选模板库」页签点击「恢复候选模板」。该操作会刷新参考元数据、保留自定义对外模型，但不会发布任何模板。

`data/model-catalog.yaml` 提供候选模板的参考元数据，它不是路由准入清单，也不会发布模型。`data/provider-catalog.json` 提供 Provider 模板，以及在 Provider 配置中可选择的候选上游模型。引入选中项只会创建持久化的 Provider 模型库存；选择发布时，还会创建或复用对外模型并添加启用映射。`GET /v1/models` 只返回启用且至少存在一条启用路由的对外模型；配置 API Key 模型白名单时还会进一步过滤。如需使用自定义 Provider 目录，将 `TOKENHUB_PROVIDER_CATALOG_FILE` 指向具有相同 `providers` 结构的本地 JSON 文件。

## 反向代理

生产环境建议使用 HTTPS，并转发：

- 管理后台流量到前端服务。
- `/v1/*` 和 `/api/admin/*` 流量到后端服务。

长文本生成和流式响应可能耗时较长，请合理设置请求体大小和超时时间。

存活探针使用 `/livez`，就绪探针使用 `/readyz`。数据库不可用时，`/readyz` 和向后兼容的 `/healthz` 会返回 `503`。
