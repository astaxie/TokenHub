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
# Chart.lock はサブチャートのバージョンを固定しますが、リポジトリ登録は行いません。
helm repo add bitnami https://charts.bitnami.com/bitnami
helm repo update
helm dependency build deploy/helm/tokenhub
helm install tokenhub deploy/helm/tokenhub \
  --set postgresql.enabled=true \
  --set postgresql.auth.password=quick-test-password \
  --set secretEnv.TOKENHUB_SECRET_KEY="$(openssl rand -hex 32)" \
  --set secretEnv.TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD="$(openssl rand -hex 16)"
```

チャートはサブチャートの Service からデータベース URL を組み立てます。Ingress がない場合はポートフォワードで Pod に到達できます(インストール時の注意書きを参照)。このモードは本番を想定したサイジングやチューニングはしていません。

## 本番:マネージド PostgreSQL

`database.url` を RDS、Cloud SQL、Azure Database などのマネージド PostgreSQL に向け、`postgresql.enabled=false` のまま Ingress を有効化します:

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

`TOKENHUB_TRUSTED_PROXY_CIDRS` には Ingress コントローラーの送信元アドレス範囲を設定してください。設定しないと、レート制限や監査ログなどでのクライアント IP の特定がコントローラーの IP になってしまいます。プライベートな Pod/Service CIDR から始めて、実際のクラスターのセグメントに絞り込むのが合理的です。同じ値の中のカンマは `\,` とエスケープしてください。Helm の `--set` パーサーは裸のカンマをリスト区切りとして扱います。values ファイルに置けばエスケープは不要です。

Ingress を有効化すると、チャートは Ingress のスキームとホストからコンソールのブラウザー側 API URL(`TOKENHUB_API_BASE_URL`)を導出し、ブラウザーのログインがアクセス元のマシンではなく API Service に届くようにします。ロードバランサーやリモートポートフォワードなど別の方法でコンソールを公開する場合は `apiBaseUrl` を明示的に設定してください。

初回ログインはユーザー名 `admin` と、`secretEnv.TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD` に設定したブートストラップパスワードを使用します:

```bash
kubectl get secret tokenhub-tokenhub-credentials -o jsonpath='{.data.TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD}' | base64 -d
```

チャートがパスワードを自動生成することはありません。`secretEnv` で安定した値を設定し、以降のアップグレードでも同じ値を維持してください。ブートストラップのシードは空のデータベースでのみ実行されます。`extraEnv` を使うとチャートがレンダリングする Secret 由来の環境変数と重複します。初回ログイン後に管理コンソールでパスワードを変更してください。

チャートはデフォルトで `TOKENHUB_ENV=prod` を設定します。本番モードの起動チェックは組み込みの開発用 admin トークンを拒否します。`TOKENHUB_ADMIN_TOKEN`(管理 API のマシン間呼び出し用)が必要な場合は `secretEnv` で指定してください。弱い値は起動時に拒否されます。

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
| Secrets | `<release>-credentials` に認証キーと任意の管理者認証情報、`<release>-database` に組み立てたデータベース URL(それぞれチャート管理時のみ作成) |
| Service | `api` ポート(8080)と `console` ポート(3000) |
| PersistentVolumeClaim | 生成画像のストレージ。`imageStorage.type` が `pvc` の場合のみ(下記) |
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

## 生成画像のストレージ

生成画像のバイト列はディスクに保存され、PostgreSQL はメタデータのみを持ちます。そのため、すべてのレプリカが同じディレクトリを参照できなければなりません。`imageStorage` でその場所を制御します:

- `ephemeral`(デフォルト):コンテナのファイルシステム。単一のテストレプリカなら許容範囲です。Pod の入れ替えでバイト列は失われ、他のレプリカが書き込んだ画像を別レプリカが返すと 404 になります。`replicaCount` が 1 を超える場合、インストール時の注意書きで警告されます。
- `pvc`:チャートが `ReadWriteMany` の PersistentVolumeClaim をレンダリングし(`imageStorage.size` で容量、`imageStorage.storageClass` でストレージクラス)、全レプリカにマウントします。マルチアクセス対応のストレージ(NFS、EFS、CephFS など)が必要です。
- `existingClaim`:自分で管理する claim を `imageStorage.existingClaim` でマウントします。

ボリュームは `imageStorage.mountPath`(`/app/data/images`)にマウントされ、チャートはこれを `TOKENHUB_IMAGE_STORAGE_DIR` としてエクスポートします。

画像ジョブのリカバリはリクエストを受け付けたインスタンスに限定されます。再起動・アップグレード・スケールでも、稼働中レプリカが処理しているジョブは失敗しません。ジョブを保持していたインスタンスが停止した場合は、そのハートビートが失効した時点(約 90 秒)でジョブが失敗として記録され、クライアントは待ち続ける代わりに確定した失敗を受け取ります。
この保証には、修正を含むバックエンドイメージが必要です。公開済みの `0.8.0` イメージはインスタンス単位のリカバリより前のものであり、別のレプリカが起動・停止すると稼働中ピアのジョブを失敗させてしまいます。このイメージを使う場合は `replicaCount` を `1` に保つか、`image.tag` をこのチャート以降にリリースされたバージョンに固定してください。

## PodMonitor での監視

クラスターで [Prometheus Operator](https://prometheus-operator.dev/) が動いている場合、API ポートの `/metrics` をスクレイプする PodMonitor を有効化できます。metrics エンドポイントは先に `extraEnv` で有効化してください:

`extraEnv` はアップグレードのたびに丸ごと置き換えられます。`--reuse-values` はマージしません。そのため、既存のエントリ(上の本番インストールの trusted-proxy リスト)を新しいエントリと一緒に再指定してください:

```bash
helm upgrade tokenhub deploy/helm/tokenhub --reuse-values \
  --set 'extraEnv[0].name=TOKENHUB_TRUSTED_PROXY_CIDRS' \
  --set 'extraEnv[0].value=10.0.0.0/8\,172.16.0.0/12\,192.168.0.0/16' \
  --set 'extraEnv[1].name=TOKENHUB_METRICS_ENABLED' \
  --set-string 'extraEnv[1].value=true' \
  --set podMonitor.enabled=true \
  --set podMonitor.bearerTokenSecret.name=<トークン用Secret> \
  --set podMonitor.bearerTokenSecret.key=TOKENHUB_METRICS_TOKEN
```

同じ理由から、アップグレードを重ねるなら values ファイル(`-f monitoring-values.yaml` に `extraEnv` の全リストを記載)の方が管理しやすくなります。

metrics エンドポイントは Bearer 認証が必要です。`secretEnv` で専用の `TOKENHUB_METRICS_TOKEN` を設定し、その Secret を `podMonitor.bearerTokenSecret` で参照してください。`podMonitor.additionalLabels` で Prometheus 側の選択条件に合わせます。

## ExternalSecret での認証情報管理

クラスターで [External Secrets Operator](https://external-secrets.io/) が動いている場合、チャート自身が Secret を生成する代わりに、Vault や AWS Secrets Manager などの外部シークレットマネージャーから認証情報を同期できます:

既存のリリースを移行する場合は、先に設定済みの認証情報とデータベース値を削除する必要があります。`--reuse-values` がそれらを保持し、このモードと競合するためです:

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

空文字列での代入には意味があります。Helm は `null` を代入された map のキーを削除します。キーが削除されると、ESO が Secret を同期した後でも Pod の環境変数参照はレンダリングされず、コンテナは起動に失敗します。`postgresql.enabled` も同様に `null` ではなく `false` を使います。`null` はサブチャートの condition が依存する真偽値を取り除き、同梱 PostgreSQL がレンダリングされたままになります。`database.url=null` は安全です。Pod はデータベース URL を Secret 経由でのみ読むためです。

以前の値がない新規インストールでは、同じコマンドからこれらの上書きを除いてそのまま使えます。移行中、チャートは認証情報 Secret の管理をやめ、初回同期後にオペレーターが所有権を引き継ぎます。

AWS Secrets Manager を例にすると、3 つの値の対応は次のとおりです:

1. 認証情報を `TOKENHUB_*` のキー名で 1 つの AWS シークレットに保存します:

   ```bash
   aws secretsmanager create-secret --name tokenhub/prod --secret-string '{
     "TOKENHUB_SECRET_KEY": "0123456789abcdef0123456789abcdef",
     "TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD": "a-stable-bootstrap-password",
     "TOKENHUB_DATABASE_URL": "postgresql://tokenhub:password@tokenhub.rds.amazonaws.com:5432/tokenhub?sslmode=require"
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

3. `externalSecret.refreshInterval` はオペレーターが再取得する間隔です(デフォルト 5 分)。AWS 側で値をローテーションすると、次の同期で Kubernetes Secret が更新されます。Pod は環境変数として値を読むため、その後に deployment へ `kubectl rollout restart` を実行して反映してください。

リモートシークレットはチャートの認証情報 Secret に 1:1 で展開され、`TOKENHUB_SECRET_KEY`、`TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`、`TOKENHUB_DATABASE_URL` が必須です。Pod はこの 3 つを必須の環境変数参照として作成するため、初回同期後にキーが欠けるとコンテナが `CreateContainerConfigError` になります。`TOKENHUB_METRICS_TOKEN` などの任意のキーも同じ方法で取り込まれ、名前で消費されます。追加のキーを Pod の環境変数として見せるには、`secretEnv` に空の値で列挙します。このモードは `external-secrets.io/v1beta1` API を使用し、`postgresql.enabled`、`secretEnv` の値と相互に排他です。

## Values

注釈付きの [values.yaml](../deploy/helm/tokenhub/values.yaml) に全項目があります。[デプロイドキュメント](deployment.md#バックエンド環境変数)に記載された任意の `TOKENHUB_*` 変数は標準の `extraEnv` リストで追加でき、Pod の環境変数に直接レンダリングされます。機密値は `secretEnv` を使います。

チャート開発の注意点は [chart README](../deploy/helm/tokenhub/README.md) を参照してください。
