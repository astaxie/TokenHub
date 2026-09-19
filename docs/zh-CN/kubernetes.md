# Kubernetes 部署

Language: English | [简体中文](kubernetes.md) | [日本語](../ja/kubernetes.md)

TokenHub 在 `deploy/helm/tokenhub` 提供 Helm chart,用于 Kubernetes 集群部署。它以 `all` 运行模式部署单个 TokenHub 容器镜像:每个 Pod 同时承载 8080 端口上的 Go API/网关和 3000 端口上的 Next.js 管理后台,与默认的 Docker Compose 部署进程布局一致。

## 前置条件

- Kubernetes 1.25+ 与 Helm 3.8+。
- PostgreSQL:生产使用托管服务,快速测试可用内置子 chart(见下文)。
- 建议配置 Ingress 控制器,让管理后台和 OpenAI 兼容 API 共用一个主机名。默认注解面向 ingress-nginx。

后端在 Pod 启动时自动执行 schema AutoMigrate。升级镜像 tag 前请先备份 PostgreSQL。

## 用内置 PostgreSQL 快速测试

对于一次性集群和冒烟测试,启用捆绑的 `bitnami/postgresql` 子 chart:

```bash
# Chart.lock 固定了子 chart 版本,但不会注册它的仓库。
helm repo add bitnami https://charts.bitnami.com/bitnami
helm repo update
helm dependency build deploy/helm/tokenhub
helm install tokenhub deploy/helm/tokenhub \
  --set postgresql.enabled=true \
  --set postgresql.auth.password=quick-test-password \
  --set secretEnv.TOKENHUB_SECRET_KEY="$(openssl rand -hex 32)" \
  --set secretEnv.TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD="$(openssl rand -hex 16)"
```

chart 会根据子 chart 的 Service 组合出数据库 URL。没有 Ingress 时通过端口转发访问 Pod(见安装提示)。此模式未针对生产做容量和调优。

## 生产使用托管 PostgreSQL

把 `database.url` 指向托管 PostgreSQL 服务(RDS、Cloud SQL、Azure Database 等),保持 `postgresql.enabled=false`,并启用 Ingress:

```bash
helm install tokenhub deploy/helm/tokenhub \
  --set ingress.enabled=true \
  --set ingress.host=tokenhub.example.com \
  --set secretEnv.TOKENHUB_SECRET_KEY="$(openssl rand -hex 32)" \
  --set secretEnv.TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD="<stable-bootstrap-password>" \
  --set database.url='postgresql://tokenhub:password@postgres.example.com:5432/tokenhub?sslmode=require' \
  --set imageStorage.type=pvc \
  --set 'extraEnv[0].name=TOKENHUB_TRUSTED_PROXY_CIDRS' \
  --set 'extraEnv[0].value=10.0.0.0/8\,172.16.0.0/12\,192.168.0.0/16'
```

`TOKENHUB_TRUSTED_PROXY_CIDRS` 必须覆盖 Ingress 控制器的来源地址,这样限流、审计等客户端 IP 归属才能拿到真实客户端地址而不是控制器 IP。私有 Pod/Service CIDR 是合理的起点,之后再收紧到你的集群实际网段。同一个值内部的逗号要写成 `\,`,因为 Helm 的 `--set` 解析器会把裸逗号当作列表分隔符;放进 values 文件则完全不需要转义。

启用 Ingress 时,chart 会从 Ingress 的协议和主机名推导控制台浏览器侧的 API 地址(`TOKENHUB_API_BASE_URL`),浏览器登录会直接打到 API Service 而不是访问者自己的机器。如果用其他方式发布控制台(负载均衡、远程端口转发),需要显式设置 `apiBaseUrl`。

首次登录使用用户名 `admin` 和你在 `secretEnv.TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD` 中设置的 bootstrap 密码:

```bash
kubectl get secret tokenhub-tokenhub-credentials -o jsonpath='{.data.TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD}' | base64 -d
```

chart 不会自动生成该密码:请通过 `secretEnv` 设置一个稳定的值,并在后续升级中保持不变,bootstrap 种子只在空数据库上运行。改走 `extraEnv` 会与 chart 已渲染的 Secret 环境项重复。首次登录后在管理后台修改密码。

chart 默认设置 `TOKENHUB_ENV=prod`。生产模式下启动会拒绝内置的开发用 admin token;如需 `TOKENHUB_ADMIN_TOKEN`(供机器间调用管理 API),通过 `secretEnv` 提供,弱值会被启动检查拒绝。

凭据也可以来自已有的 Secret 而不是内联值:

```bash
helm install tokenhub deploy/helm/tokenhub \
  --set ingress.enabled=true --set ingress.host=tokenhub.example.com \
  --set credentialsSecret=tokenhub-auth \
  --set database.existingSecret=tokenhub-database
```

被引用的 auth Secret 必须提供 `TOKENHUB_SECRET_KEY` 和 `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`;数据库 Secret 必须提供 `TOKENHUB_DATABASE_URL`。

## Chart 部署了什么

| 资源 | 用途 |
| --- | --- |
| Deployment | 每个 Pod 副本通过 `tokenhub-run` 同时运行两个进程;环境变量直接渲染进 Pod spec |
| Secrets | `<release>-credentials` 存放鉴权密钥和可选管理凭据,`<release>-database` 存放组合出的数据库 URL(各自仅在 chart 托管时创建) |
| Service | `api` 端口(8080)和 `console` 端口(3000) |
| PersistentVolumeClaim | 生成图片存储,仅在 `imageStorage.type` 为 `pvc` 时创建(见下文) |
| Ingress | API 路径指向 `api` 端口,其余路径指向 `console` 端口(默认关闭) |
| PodMonitor | 仅在 `podMonitor.enabled` 为 true 时创建(见下文) |
| ExternalSecret | 仅在 `externalSecret.enabled` 为 true 时创建(见下文) |

Ingress 路由与 `deploy/nginx.multi-instance.conf` 一致:

| 路径 | 后端端口 |
| --- | --- |
| `/api`、`/v1`、`/v1beta`、`/docs`、`/openapi.json`、`/openapi.yaml`、`/healthz`、`/readyz`、`/livez` | `api`(8080) |
| 其余所有路径 | `console`(3000) |

默认 Ingress 注解关闭了响应缓冲,把读写超时提高到 300 秒以保证流式响应不被中断,并把请求体上限设为 32 MiB,与 `TOKENHUB_MAX_MULTIMODAL_REQUEST_BYTES` 一致。其他 Ingress 控制器或不同限制可以覆盖 `ingress.annotations`;请求体上限不要低于多模态请求限制。

## 健康检查与停机

`tokenhub-run` 会监管两个进程,任一进程退出都会让容器退出,因此 Kubernetes 会在进程故障时重启 Pod。startup 和 readiness 探针通过 HTTP 检查 API 的 `/readyz`,数据库不可达时 Pod 会停止接收流量;liveness 探针检查 API 的 `/livez`。默认 `terminationGracePeriodSeconds` 为 180 秒,覆盖后端默认 150 秒的优雅停机窗口,进行中的流式请求可以在 Pod 移除前排空。滚动更新配置为 `maxUnavailable: 0`,升级期间不会损失容量。

## 默认无状态

Chart 默认是无状态的:

- **插件:** 运行时安装的插件包(管理台上传和 marketplace 安装)存放在 `emptyDir` 中,Pod 被替换后会丢失。内置 provider 插件随镜像分发,始终存在。要让安装的插件跨 Pod 重启保留,把插件包打进自定义镜像即可。
- **Release:** 容器每次启动都会把镜像内的 release bundle 物化到临时目录。模型目录和 provider 目录随镜像分发,并在首次启动时种子进数据库;管理后台里做的目录变更存放在 PostgreSQL 中,pod 重建不丢失。应用内自更新被禁用(`TOKENHUB_MANAGED_UPDATES=false`);升级方式是修改 `image.tag`:

```bash
helm upgrade tokenhub deploy/helm/tokenhub --reuse-values --set image.tag=<new-tag>
```

- **数据:** 所有持久状态都在 PostgreSQL 中。使用 PostgreSQL 工具备份,参见 [PostgreSQL 配置](postgresql-setup.md)。

## 生成图片的存储

生成的图片字节存放在磁盘上,PostgreSQL 只保存元数据,所以每个副本必须看到同一个目录。`imageStorage` 控制这个目录的位置:

- `ephemeral`(默认):容器文件系统。单个测试副本可以接受;Pod 被替换后字节丢失,其他副本读到别的副本写入的图片会返回 404。`replicaCount` 大于 1 时安装提示会给出警告。
- `pvc`:chart 渲染一个 `ReadWriteMany` 的 PersistentVolumeClaim(容量取 `imageStorage.size`,存储类取 `imageStorage.storageClass`)并挂载进每个副本。要求存储后端支持多节点读写(NFS、EFS、CephFS 等)。
- `existingClaim`:挂载自行管理的 claim,通过 `imageStorage.existingClaim` 指定。

卷挂载在 `imageStorage.mountPath`(`/app/data/images`),chart 会把它导出为 `TOKENHUB_IMAGE_STORAGE_DIR`。

图片任务的恢复只作用于接受该请求的实例:重启、升级或扩容都不会让仍在运行的副本上的任务失败。持有任务的实例死亡后,其心跳过期(约 90 秒)时任务会被标记失败,客户端拿到确定的失败结果而不是一直等待。
这一保证需要包含对应修复的后端镜像。已发布的 `0.8.0` 镜像早于按实例隔离的恢复逻辑,当另一个副本启动或停止时,仍会把运行中的任务标记为失败:使用该镜像时请保持 `replicaCount` 为 `1`,或将 `image.tag` 固定到本 chart 之后发布的版本。

## 用 PodMonitor 监控

集群运行 [Prometheus Operator](https://prometheus-operator.dev/) 时,可以启用 PodMonitor 抓取 API 端口上的 `/metrics`。metrics 端点需要先通过 `extraEnv` 打开:

`extraEnv` 在每次升级时都会被整体替换——`--reuse-values` 不会合并它——所以要把已设置的条目(上面生产安装中的 trusted-proxy 列表)和新条目一起重新声明:

```bash
helm upgrade tokenhub deploy/helm/tokenhub --reuse-values \
  --set 'extraEnv[0].name=TOKENHUB_TRUSTED_PROXY_CIDRS' \
  --set 'extraEnv[0].value=10.0.0.0/8\,172.16.0.0/12\,192.168.0.0/16' \
  --set 'extraEnv[1].name=TOKENHUB_METRICS_ENABLED' \
  --set-string 'extraEnv[1].value=true' \
  --set podMonitor.enabled=true \
  --set podMonitor.bearerTokenSecret.name=<存放token的secret> \
  --set podMonitor.bearerTokenSecret.key=TOKENHUB_METRICS_TOKEN
```

同样原因,如果后续还要多次升级,用 values 文件(`-f monitoring-values.yaml`,里面写完整的 `extraEnv` 列表)更好维护。

metrics 端点需要 Bearer 鉴权:通过 `secretEnv` 设置专用的 `TOKENHUB_METRICS_TOKEN`,再用 `podMonitor.bearerTokenSecret` 引用对应的 Secret;用 `podMonitor.additionalLabels` 让你的 Prometheus 实例选中这个 monitor。

## 用 ExternalSecret 管理凭据

集群运行 [External Secrets Operator](https://external-secrets.io/) 时,chart 可以从 Vault、AWS Secrets Manager 等外部密钥管理系统同步凭据,而不是自己渲染 Secret:

迁移已有 release 时要先清掉之前配置的凭据和数据库值,否则 `--reuse-values` 会保留它们并与该模式冲突:

```bash
helm upgrade tokenhub deploy/helm/tokenhub --reuse-values \
  --set externalSecret.enabled=true \
  --set externalSecret.secretStore=aws-secrets-manager \
  --set externalSecret.sourceSecretId=tokenhub/prod \
  --set database.url=null \
  --set postgresql.enabled=false \
  --set-string 'secretEnv.TOKENHUB_SECRET_KEY=' \
  --set-string 'secretEnv.TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD='
```

用空字符串赋值是有意为之:Helm 会把赋值为 `null` 的 map 键整个删掉,键被删掉后,即使 ESO 已把值同步进 Secret,Pod 的环境变量引用也不再渲染,容器会启动失败。`postgresql.enabled` 同理要用 `false` 而不是 `null`:`null` 会去掉子 chart condition 依赖的布尔值,导致内置 PostgreSQL 仍被渲染。`database.url=null` 则是安全的,因为 Pod 只通过 Secret 读取数据库 URL。

全新安装没有旧值时,同样的命令不需要这些覆盖参数。切换过程中 chart 不再管理凭证 secret,首次同步后由 operator 接管其所有权。

以 AWS Secrets Manager 为例,三个值的对应关系如下:

1. 把凭据按键名为 `TOKENHUB_*` 的格式存进一条 AWS secret:

   ```bash
   aws secretsmanager create-secret --name tokenhub/prod --secret-string '{
     "TOKENHUB_SECRET_KEY": "0123456789abcdef0123456789abcdef",
     "TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD": "a-stable-bootstrap-password",
     "TOKENHUB_DATABASE_URL": "postgresql://tokenhub:password@tokenhub.rds.amazonaws.com:5432/tokenhub?sslmode=require"
   }'
   ```

   `externalSecret.sourceSecretId` 就是这条 secret 的名称(`tokenhub/prod`)。

2. 每个集群创建一次 `ClusterSecretStore`,它的 metadata 名称就是 `externalSecret.secretStore`。使用 IRSA 认证时它引用 chart 的 ServiceAccount,所以要给该账号标注 role:

   ```bash
   helm upgrade tokenhub deploy/helm/tokenhub --reuse-values \
     --set serviceAccount.annotations."eks\.amazonaws\.com/role-arn"=arn:aws:iam::123456789012:role/tokenhub-external-secrets
   ```

   ```yaml
   apiVersion: external-secrets.io/v1beta1
   kind: ClusterSecretStore
   metadata:
     name: aws-secrets-manager # externalSecret.secretStore
   spec:
     provider:
       aws:
         service: SecretsManager
         region: ap-northeast-1
         auth:
           jwt:
             serviceAccountRef:
               name: tokenhub-tokenhub # chart 的 ServiceAccount
               namespace: tokenhub
   ```

   给该 role 授权 `tokenhub/*` 的 `secretsmanager:GetSecretValue`;使用自定义 KMS 密钥时还需要 `kms:Decrypt`。

3. `externalSecret.refreshInterval` 控制 operator 重新提取的频率(默认 5 分钟)。在 AWS 侧轮换值后,下一个刷新周期会更新 Kubernetes Secret;由于 Pod 以环境变量形式读取这些值,之后还要对 deployment 执行 `kubectl rollout restart` 才会生效。

远端 secret 会 1:1 提取进 chart 的凭证 secret,必须包含 `TOKENHUB_SECRET_KEY`、`TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD` 和 `TOKENHUB_DATABASE_URL`——Pod 会为三者创建非可选的环境变量引用,首次同步后缺少任一键都会让容器进入 `CreateContainerConfigError`。`TOKENHUB_METRICS_TOKEN` 等可选键按同样方式带上并按名称消费;想把额外的键暴露为 Pod 环境变量,把它以空值列入 `secretEnv` 即可。此模式使用 `external-secrets.io/v1beta1` API,与 `postgresql.enabled`、`secretEnv` 值互斥。

## Values

完整列表参见带注释的 [values.yaml](../deploy/helm/tokenhub/values.yaml)。任何[部署文档](deployment.md#后端环境变量)中记载的 `TOKENHUB_*` 变量都可以通过标准的 `extraEnv` 列表添加,并直接渲染进 Pod 环境;敏感值走 `secretEnv`。

Chart 开发说明见 [chart README](../deploy/helm/tokenhub/README.md)。
