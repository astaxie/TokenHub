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
helm dependency build deploy/helm/tokenhub
helm install tokenhub deploy/helm/tokenhub \
  --set postgresql.enabled=true \
  --set postgresql.auth.password=quick-test-password \
  --set secretEnv.TOKENHUB_SECRET_KEY="$(openssl rand -hex 32)"
```

chart 会根据子 chart 的 Service 组合出数据库 URL。没有 Ingress 时通过端口转发访问 Pod(见安装提示)。此模式未针对生产做容量和调优。

## 生产使用托管 PostgreSQL

把 `database.url` 指向托管 PostgreSQL 服务(RDS、Cloud SQL、Azure Database 等),保持 `postgresql.enabled=false`,并启用 Ingress:

```bash
helm install tokenhub deploy/helm/tokenhub \
  --set ingress.enabled=true \
  --set ingress.host=tokenhub.example.com \
  --set secretEnv.TOKENHUB_SECRET_KEY="$(openssl rand -hex 32)" \
  --set database.url='postgresql://tokenhub:password@postgres.example.com:5432/tokenhub?sslmode=require' \
  --set 'extraEnv[0].name=TOKENHUB_TRUSTED_PROXY_CIDRS' \
  --set 'extraEnv[0].value=10.0.0.0/8,172.16.0.0/12,192.168.0.0/16'
```

`TOKENHUB_TRUSTED_PROXY_CIDRS` 必须覆盖 Ingress 控制器的来源地址,这样限流、审计等客户端 IP 归属才能拿到真实客户端地址而不是控制器 IP。私有 Pod/Service CIDR 是合理的起点,之后再收紧到你的集群实际网段。

首次登录使用用户名 `admin` 和 chart 生成的随机 bootstrap 密码:

```bash
kubectl get secret tokenhub-tokenhub-credentials -o jsonpath='{.data.TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD}' | base64 -d
```

首次登录后在管理后台修改密码;bootstrap 种子只在空数据库上运行。想自己控制可以通过 `extraEnv` 设置 `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`。

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
| Secret | `TOKENHUB_SECRET_KEY`、可选的管理凭据、数据库 URL、`secretEnv` 条目(chart 托管时) |
| Service | `api` 端口(8080)和 `console` 端口(3000) |
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

## 用 PodMonitor 监控

集群运行 [Prometheus Operator](https://prometheus-operator.dev/) 时,可以启用 PodMonitor 抓取 API 端口上的 `/metrics`。metrics 端点需要先通过 `extraEnv` 打开:

```bash
helm upgrade tokenhub deploy/helm/tokenhub --reuse-values \
  --set 'extraEnv[0].name=TOKENHUB_METRICS_ENABLED' \
  --set 'extraEnv[0].value=true' \
  --set podMonitor.enabled=true \
  --set podMonitor.bearerTokenSecret.name=<存放token的secret> \
  --set podMonitor.bearerTokenSecret.key=TOKENHUB_METRICS_TOKEN
```

metrics 端点需要 Bearer 鉴权:通过 `secretEnv` 设置专用的 `TOKENHUB_METRICS_TOKEN`,再用 `podMonitor.bearerTokenSecret` 引用对应的 Secret;用 `podMonitor.additionalLabels` 让你的 Prometheus 实例选中这个 monitor。

## 用 ExternalSecret 管理凭据

集群运行 [External Secrets Operator](https://external-secrets.io/) 时,chart 可以从 Vault、AWS Secrets Manager 等外部密钥管理系统同步凭据,而不是自己渲染 Secret:

```bash
helm upgrade tokenhub deploy/helm/tokenhub --reuse-values \
  --set externalSecret.enabled=true \
  --set externalSecret.secretStore=aws-secrets-manager \
  --set externalSecret.sourceSecretId=tokenhub/prod
```

以 AWS Secrets Manager 为例,三个值的对应关系如下:

1. 把凭据按键名为 `TOKENHUB_*` 的格式存进一条 AWS secret:

   ```bash
   aws secretsmanager create-secret --name tokenhub/prod --secret-string '{
     "TOKENHUB_SECRET_KEY": "0123456789abcdef0123456789abcdef",
     "TOKENHUB_DATABASE_URL": "postgresql://tokenhub:password@tokenhub.rds.amazonaws.com:5432/tokenhub?sslmode=require",
     "TOKENHUB_ADMIN_TOKEN": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
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

3. `externalSecret.refreshInterval` 控制 operator 重新提取的频率(默认 5 分钟)。在 AWS 侧轮换值后,下一个刷新周期自动生效,不需要改动集群。

远端 secret 会 1:1 提取进 chart 的凭证 secret,必须包含 `TOKENHUB_SECRET_KEY`、`TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD` 和 `TOKENHUB_DATABASE_URL`;`TOKENHUB_METRICS_TOKEN` 等可选键按同样方式带上即可。此模式使用 `external-secrets.io/v1beta1` API,与 `postgresql.enabled`、`secretEnv` 互斥。

## Values

完整列表参见带注释的 [values.yaml](../deploy/helm/tokenhub/values.yaml)。任何[部署文档](deployment.md#后端环境变量)中记载的 `TOKENHUB_*` 变量都可以通过标准的 `extraEnv` 列表添加,并直接渲染进 Pod 环境;敏感值走 `secretEnv`。

Chart 开发说明见 [chart README](../deploy/helm/tokenhub/README.md)。
