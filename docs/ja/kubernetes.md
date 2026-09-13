# Kubernetes デプロイ

Language: English | [简体中文](../zh-CN/kubernetes.md) | [日本語](kubernetes.md)

TokenHub は Kubernetes クラスター向けに `deploy/helm/tokenhub` の Helm チャートを同梱しています。このチャートは単一の TokenHub コンテナイメージを `all` ランモードでデプロイします。各 Pod はポート 8080 の Go API/ゲートウェイとポート 3000 の Next.js 管理コンソールを両方提供し、デフォルトの Docker Compose デプロイと同じプロセス構成です。

## 前提条件

- Kubernetes 1.25+ と Helm 3.8+。
- PostgreSQL:本番はマネージドサービス、クイックテストは同梱のサブチャート(下記)を利用できます。
- 推奨:管理コンソールと OpenAI 互換 API を同じホスト名で提供する Ingress コントローラー。デフォルトのアノテーションは ingress-nginx 向けです。

バックエンドは Pod 起動時にスキーマの AutoMigrate を実行します。イメージタグを更新する前に PostgreSQL をバックアップしてください。

## 組み込み PostgreSQL でのクイックテスト

使い捨てクラスターでの動作確認には、同梱の `bitnami/postgresql` サブチャートを有効化します:

```bash
helm dependency build deploy/helm/tokenhub
helm install tokenhub deploy/helm/tokenhub \
  --set postgresql.enabled=true \
  --set postgresql.auth.password=quick-test-password \
  --set secretEnv.TOKENHUB_SECRET_KEY="$(openssl rand -hex 32)"
```

チャートはサブチャートの Service からデータベース URL を組み立てます。Ingress がない場合はポートフォワードで Pod に到達できます(インストール時の注意書きを参照)。このモードは本番を想定したサイジングやチューニングはしていません。

## 本番:マネージド PostgreSQL

`database.url` を RDS、Cloud SQL、Azure Database などのマネージド PostgreSQL に向け、`postgresql.enabled=false` のまま Ingress を有効化します:

```bash
helm install tokenhub deploy/helm/tokenhub \
  --set ingress.enabled=true \
  --set ingress.host=tokenhub.example.com \
  --set secretEnv.TOKENHUB_SECRET_KEY="$(openssl rand -hex 32)" \
  --set database.url='postgresql://tokenhub:password@postgres.example.com:5432/tokenhub?sslmode=require' \
  --set 'extraEnv[0].name=TOKENHUB_TRUSTED_PROXY_CIDRS' \
  --set 'extraEnv[0].value=10.0.0.0/8,172.16.0.0/12,192.168.0.0/16'
```

`TOKENHUB_TRUSTED_PROXY_CIDRS` には Ingress コントローラーの送信元アドレス範囲を設定してください。設定しないと、レート制限や監査ログなどでのクライアント IP の特定がコントローラーの IP になってしまいます。プライベートな Pod/Service CIDR から始めて、実際のクラスターのセグメントに絞り込むのが合理的です。

初回ログインはユーザー名 `admin` と、チャートが生成したランダムなブートストラップパスワードを使用します:

```bash
kubectl get secret tokenhub-tokenhub-credentials -o jsonpath='{.data.TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD}' | base64 -d
```

初回ログイン後に管理コンソールでパスワードを変更してください。ブートストラップのシードは空のデータベースでのみ実行されます。自分で管理したい場合は `extraEnv` で `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD` を設定できます。

認証情報はインライン値の代わりに既存の Secret からも供給できます:

```bash
helm install tokenhub deploy/helm/tokenhub \
  --set ingress.enabled=true --set ingress.host=tokenhub.example.com \
  --set credentialsSecret=tokenhub-auth \
  --set database.existingSecret=tokenhub-database
```

参照する auth Secret には `TOKENHUB_SECRET_KEY` と `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD` が必須です。データベース用 Secret には `TOKENHUB_DATABASE_URL` が必要です。

## チャートがデプロイするリソース

| リソース | 用途 |
| --- | --- |
| Deployment | 各 Pod レプリカが `tokenhub-run` で両プロセスを実行。環境変数は Pod spec に直接レンダリング |
| Secret | `TOKENHUB_SECRET_KEY`、任意の管理者認証情報、データベース URL、`secretEnv` のエントリ(チャート管理時) |
| Service | `api` ポート(8080)と `console` ポート(3000) |
| Ingress | API パスは `api` ポートへ、それ以外は `console` ポートへ(デフォルト無効) |
| PodMonitor | `podMonitor.enabled` が true の場合のみ(下記) |
| ExternalSecret | `externalSecret.enabled` が true の場合のみ(下記) |

Ingress のルーティングは `deploy/nginx.multi-instance.conf` と同じです:

| パス | バックエンドポート |
| --- | --- |
| `/api`、`/v1`、`/v1beta`、`/docs`、`/openapi.json`、`/openapi.yaml`、`/healthz`、`/readyz`、`/livez` | `api`(8080) |
| その他すべて | `console`(3000) |

デフォルトの Ingress アノテーションはレスポンスバッファリングを無効化し、読み書きタイムアウトを 300 秒に引き上げてストリーミング応答を保護します。ボディサイズ上限は `TOKENHUB_MAX_MULTIMODAL_REQUEST_BYTES` に合わせて 32 MiB です。他の Ingress コントローラーや別の上限値が必要な場合は `ingress.annotations` を上書きしてください。ボディ上限はマルチモーダルリクエストの上限以上にしてください。

## ヘルスチェックとシャットダウン

`tokenhub-run` は両プロセスを監視し、どちらかが終了するとコンテナごと終了するため、Kubernetes はプロセス障害時に Pod を再起動します。startup と readiness プローブは API の `/readyz` を HTTP で確認し、データベースに到達できない間は Pod がトラフィックを受けません。liveness プローブは API の `/livez` を確認します。デフォルトの `terminationGracePeriodSeconds` は 180 秒で、バックエンドのデフォルト 150 秒のグレースフルシャットダウン期間をカバーし、進行中のストリーミングリクエストは Pod の削除前に排水されます。ローリングアップデートは `maxUnavailable: 0` で、アップグレード中も処理能力は維持されます。

## デフォルトでステートレス

チャートはデフォルトでステートレスです:

- **プラグイン:** 管理コンソールのアップロードやマーケットプレイスからインストールしたプラグインパッケージは `emptyDir` に保存され、Pod の入れ替えで失われます。組み込みプロバイダープラグインはイメージに同梱されているため常に存在します。インストール済みパッケージを Pod の再起動をまたいで保持するには、パッケージをカスタムイメージに焼き込んでください。
- **リリース:** コンテナは起動のたびにイメージ内のリリースバンドルを一時ディレクトリへ展開します。モデルカタログとプロバイダーカタログはイメージに同梱され、初回起動時にデータベースへシードされます。管理コンソールでのカタログ変更は PostgreSQL に保存され、Pod の入れ替えでも失われません。アプリ内セルフアップデートは無効(`TOKENHUB_MANAGED_UPDATES=false`)です。アップグレードは `image.tag` の変更で行います:

```bash
helm upgrade tokenhub deploy/helm/tokenhub --reuse-values --set image.tag=<new-tag>
```

- **データ:** 永続状態はすべて PostgreSQL に保存されます。PostgreSQL のツールでバックアップしてください。詳細は [PostgreSQL セットアップ](postgresql-setup.md) を参照してください。

## PodMonitor での監視

クラスターで [Prometheus Operator](https://prometheus-operator.dev/) が動いている場合、API ポートの `/metrics` をスクレイプする PodMonitor を有効化できます。metrics エンドポイントは先に `extraEnv` で有効化してください:

```bash
helm upgrade tokenhub deploy/helm/tokenhub --reuse-values \
  --set 'extraEnv[0].name=TOKENHUB_METRICS_ENABLED' \
  --set 'extraEnv[0].value=true' \
  --set podMonitor.enabled=true \
  --set podMonitor.bearerTokenSecret.name=<トークン用Secret> \
  --set podMonitor.bearerTokenSecret.key=TOKENHUB_METRICS_TOKEN
```

metrics エンドポイントは Bearer 認証が必要です。`secretEnv` で専用の `TOKENHUB_METRICS_TOKEN` を設定し、その Secret を `podMonitor.bearerTokenSecret` で参照してください。`podMonitor.additionalLabels` で Prometheus 側の選択条件に合わせます。

## ExternalSecret での認証情報管理

クラスターで [External Secrets Operator](https://external-secrets.io/) が動いている場合、チャート自身が Secret を生成する代わりに、Vault や AWS Secrets Manager などの外部シークレットマネージャーから認証情報を同期できます:

```bash
helm upgrade tokenhub deploy/helm/tokenhub --reuse-values \
  --set externalSecret.enabled=true \
  --set externalSecret.secretStore=aws-secrets-manager \
  --set externalSecret.sourceSecretId=tokenhub/prod
```

AWS Secrets Manager を例にすると、3 つの値の対応は次のとおりです:

1. 認証情報を `TOKENHUB_*` のキー名で 1 つの AWS シークレットに保存します:

   ```bash
   aws secretsmanager create-secret --name tokenhub/prod --secret-string '{
     "TOKENHUB_SECRET_KEY": "0123456789abcdef0123456789abcdef",
     "TOKENHUB_DATABASE_URL": "postgresql://tokenhub:password@tokenhub.rds.amazonaws.com:5432/tokenhub?sslmode=require",
     "TOKENHUB_ADMIN_TOKEN": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
   }'
   ```

   `externalSecret.sourceSecretId` はこのシークレット名(`tokenhub/prod`)です。

2. `ClusterSecretStore` はクラスターごとに 1 回作成します。その metadata 名が `externalSecret.secretStore` です。IRSA 認証ではチャートの ServiceAccount を参照するため、そのアカウントにロールのアノテーションを付けます:

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
               name: tokenhub-tokenhub # チャートの ServiceAccount
               namespace: tokenhub
   ```

   このロールには `tokenhub/*` に対する `secretsmanager:GetSecretValue` を付与し、カスタマーマネージド KMS キーを使う場合は `kms:Decrypt` も必要です。

3. `externalSecret.refreshInterval` はオペレーターが再取得する間隔です(デフォルト 5 分)。AWS 側で値をローテーションすると、次の同期で自動的に反映され、クラスター側は触りません。

リモートシークレットはチャートの認証情報 Secret に 1:1 で展開され、`TOKENHUB_SECRET_KEY`、`TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`、`TOKENHUB_DATABASE_URL` が必須です。`TOKENHUB_METRICS_TOKEN` などの任意のキーも同じ方法で取り込まれます。このモードは `external-secrets.io/v1beta1` API を使用し、`postgresql.enabled`、`secretEnv` と相互に排他です。

## Values

注釈付きの [values.yaml](../deploy/helm/tokenhub/values.yaml) に全項目があります。[デプロイドキュメント](deployment.md#バックエンド環境変数)に記載された任意の `TOKENHUB_*` 変数は標準の `extraEnv` リストで追加でき、Pod の環境変数に直接レンダリングされます。機密値は `secretEnv` を使います。

チャート開発の注意点は [chart README](../deploy/helm/tokenhub/README.md) を参照してください。
