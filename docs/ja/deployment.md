# デプロイ

Language: [English](../deployment.md) | [简体中文](../zh-CN/deployment.md) | 日本語

TokenHub は、Go バックエンド、Next.js 管理コンソール、SQLite 永続化で構成されるプライベートデプロイ向けのサービスです。

## データベースの選択

TokenHub は 2 種類のデータベースバックエンドをサポートしています。

### SQLite（デフォルト）

**利点：**
- 設定不要で、別途データベースサービスが不要
- 中小規模のデプロイに適する
- バックアップが簡単（ファイルを直接コピー）

**ユースケース：**
- 開発およびテスト環境
- 1000 ユーザー未満のデプロイ
- 単一サーバーのデプロイ

**デプロイ：**

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml up -d
```

### PostgreSQL（本番環境推奨）

**利点：**
- 高並行シナリオに適したエンタープライズ級データベース
- より優れたトランザクションサポートとデータ整合性
- レプリケーションと高可用性をサポート

**ユースケース：**
- 本番環境
- 1000 ユーザーを超えるデプロイ
- 高可用性要件

**デプロイ：**

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.postgres.yml up -d
```

PostgreSQL の詳細な設定については、[PostgreSQL セットアップガイド](../postgresql-setup.md)を参照してください。

### リモート PostgreSQL を使用するマルチインスタンス構成

データベースを Compose プロジェクト外で管理する場合は `deploy/docker-compose.remote-postgres.yml` を使用します。この構成はスケール可能なバックエンドとフロントエンドの前に Nginx ゲートウェイを配置し、ローカルデータベースを起動しません。

リモート `TOKENHUB_DATABASE_URL`、公開ゲートウェイ URL、本番用シークレット、信頼するプロキシ CIDR を設定して実行します。

```bash
docker compose --env-file deploy/.env \
  -f deploy/docker-compose.remote-postgres.yml up -d \
  --scale tokenhub-backend=3 \
  --scale tokenhub-frontend=2
```

すべてのレプリカで同じ `TOKENHUB_SECRET_KEY` を使用してください。`TOKENHUB_DB_MAX_OPEN_CONNS` はレプリカ単位なので、合計接続数が PostgreSQL の上限を下回るように設定します。SQLite ファイルを複数のバックエンドで共有してはいけません。

`./deploy/test-multi-instance.sh` で実際の 2 インスタンス PostgreSQL E2E テストを実行できます。

## Docker Compose

デプロイ用の環境変数ファイルを作成します。

```bash
cp deploy/.env.example deploy/.env
```

起動前に `deploy/.env` を編集してください。

- `TOKENHUB_ADMIN_TOKEN`: Admin API の初期 Token。32 バイト以上のランダム値を使用してください。
- `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`: 初期 `admin` ユーザーの作成時にのみ使用するパスワード。12 バイト以上にしてください。
- `TOKENHUB_SECRET_KEY`: バックエンド秘密鍵。32 バイト以上のランダム値を使用し、安定して保持してください。
- `TOKENHUB_PUBLIC_BASE_URL`: ユーザーに表示する TokenHub ゲートウェイ URL。
- `TOKENHUB_GATEWAY_PORT`: Caddy ゲートウェイのホスト側ポート。デフォルトは `3000`。
- `NEXT_PUBLIC_API_BASE_URL`: 空のままにして、ブラウザの管理コンソールから Caddy 経由の同一オリジン API パスを使用します。

リポジトリルートから起動します。

```bash
./deploy/install.sh
```

スクリプトはビルド前に Compose の環境変数を検証し、秘密値を表示せずに安全でない変数を個別に報告します。Compose が失敗し、その試行で作成または再起動したバックエンドコンテナが exited、restarting、dead、unhealthy のいずれかである場合、その試行のバックエンドログを最大 100 行表示します。バックエンド以外の障害では、無関係なバックエンドログを出力しません。

コンテナをビルドまたは起動せず、設定だけを検証するには次を実行します。

```bash
./deploy/install.sh --check-only
```

別の環境ファイルを使用する場合は、`./deploy/install.sh --env-file /path/to/deploy.env` を実行します。

### 任意: サーバー側のビルド高速化

このプロジェクトの Dockerfile には、地域依存のパッケージミラーをハードコードしません。サーバーから Docker Hub、npm、Go Module ソースへのアクセスが遅い場合は、Dockerfile を編集せず、デプロイ先サーバー側で高速化を設定してください。

ベースイメージの取得には、サーバーの Docker daemon にレジストリミラーを設定できます。例として `/etc/docker/daemon.json` を編集し、Docker を再起動します。

```json
{
	"registry-mirrors": [
		"https://<your-docker-registry-mirror>"
	]
}
```

イメージビルド中の依存関係ダウンロードについては、サーバーで Docker または BuildKit 向けの HTTP/HTTPS アウトバウンドプロキシを設定することを推奨します。これによりビルドの移植性を保ち、環境固有の npm や Go proxy 設定をリポジトリにコミットせずに済みます。

デプロイ環境から上流レジストリへの直接アクセスが遅い場合は、次のサーバー側設定例を参考にできます。

```bash
# Go Module のダウンロード
go env -w GOPROXY=https://goproxy.cn,direct

# npm パッケージのダウンロード
npm config set registry https://registry.npmmirror.com
```

これらのコマンドはサーバーまたはビルド環境を設定するためのものです。環境固有の fork を意図的に保守する場合を除き、プロジェクトの Dockerfile には直接追加しないでください。

Compose は次を起動します。

- Caddy ゲートウェイ: `http://localhost:3000`（別のポートは `TOKENHUB_GATEWAY_PORT` で設定）
- TokenHub フロントエンドとバックエンド: 内部 Docker ネットワークからのみアクセス可能
- SQLite データ: Docker named volume `tokenhub-data`
- モデルカタログ: `data/model-catalog.yaml` からマウント

社内ネットワークからアクセスする場合は、`TOKENHUB_PUBLIC_BASE_URL` を `http://172.25.88.170:4003` のようなゲートウェイ URL に設定し、`TOKENHUB_GATEWAY_PORT=4003` を設定して、ホストファイアウォールではこの TCP ポートのみ許可します。`NEXT_PUBLIC_API_BASE_URL` は設定しないでください。管理コンソールは Caddy 経由の同一オリジン `/api` リクエストを使用し、Caddy は `/v1` リクエストもバックエンドへプロキシします。

状態を確認します。

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml ps
```

初回管理者ログイン:

- ユーザー名: `admin`
- パスワード: 設定した `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`

`prod`、`production`、ステージングなどの非開発環境では、プレースホルダー値、32 バイト未満の Admin Token または秘密鍵、12 バイト未満の初期パスワードを拒否します。

ログを手動で確認または追跡します。

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml logs -f
```

停止します。

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml down
```

停止して SQLite データボリュームも削除します。

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml down -v
```

`down -v` は、ローカルデータを削除したい場合にのみ使用してください。

## バックエンド環境変数

| 変数 | デフォルト | 説明 |
| --- | --- | --- |
| `TOKENHUB_ENV` | `prod` | ランタイム環境名 |
| `TOKENHUB_HTTP_ADDR` | `:8080` | バックエンド待受アドレス |
| `TOKENHUB_PUBLIC_BASE_URL` | `http://localhost:3000` | ユーザーに表示する TokenHub ゲートウェイ URL |
| `TOKENHUB_TRUSTED_PROXY_CIDRS` | 空 | `X-Forwarded-For` を提供できるプロキシ IP または CIDR（カンマ区切り） |
| `TOKENHUB_CORS_ALLOWED_ORIGINS` | 公開 URL | バックエンドを呼び出せるブラウザー Origin（カンマ区切り） |
| `TOKENHUB_ADMIN_TOKEN` | `change-me-tokenhub-admin-token` | Admin API 用の初期 Token |
| `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD` | `change-me-tokenhub-admin-password` | 初期 `admin` ユーザーのパスワード。本番起動前に変更が必要 |
| `TOKENHUB_SECRET_KEY` | `change-me-tokenhub-secret-key` | バックエンド秘密鍵 |
| `TOKENHUB_DATABASE_URL` | `sqlite:///app/data/tokenhub.db` | コンテナ内 SQLite データベースパス |
| `TOKENHUB_SQLITE_BACKUP_DIR` | `/app/data/backups` | バックアップ出力ディレクトリ |
| `TOKENHUB_MODEL_CATALOG_FILE` | `/app/catalog/model-catalog.yaml` | 標準モデルカタログファイル |
| `TOKENHUB_SEED_DEMO` | `false` | デモデータを投入するか |
| `TOKENHUB_LOG_LEVEL` | `info` | ログレベル |
| `TOKENHUB_RESOURCE_FAILURE_THRESHOLD` | `3` | Provider リソースをクールダウンするまでの失敗しきい値 |
| `TOKENHUB_RESOURCE_COOLDOWN_SECONDS` | `300` | Provider リソースのクールダウン秒数 |
| `TOKENHUB_IN_FLIGHT_LEASE_TTL_SECONDS` | `300` | クラスター全体の同時実行リースの期限と更新間隔の基準 |
| `TOKENHUB_CLUSTER_LOCK_TTL_SECONDS` | `180` | クラスター調整ロックの期限と更新間隔の基準 |
| `TOKENHUB_GRACEFUL_SHUTDOWN_SECONDS` | `150` | 停止時に処理中リクエストを待機する最大秒数 |
| `TOKENHUB_STOP_GRACE_PERIOD` | `180s` | Docker がバックエンドを強制停止するまでの Compose 猶予時間 |

## フロントエンド環境変数

| 変数 | デフォルト | 説明 |
| --- | --- | --- |
| `NEXT_PUBLIC_API_BASE_URL` | 空 | Caddy 経由の同一オリジン API パスを使うため空のままにする |
| `NEXT_PUBLIC_APP_NAME` | `TokenHub` | 表示名 |

## データとバックアップ

SQLite は、プロジェクト、Key、Provider、ルート、ユーザー、リクエストログ、利用量、アラート、承認、セッション、バックアップ記録の永続化元です。

ワンコマンド compose デプロイでは次を使用します。

- コンテナ内データベースパス: `/app/data/tokenhub.db`
- コンテナ内バックアップパス: `/app/data/backups`
- Docker volume 名: `tokenhub-data`

本番環境の推奨:

- SQLite データベースを永続ディスクに保存します。
- バックアップをアプリケーションコンテナ外に保存します。
- 保持ポリシーに従って古いバックアップを削除します。
- Provider 認証情報と Admin Token はシークレット管理または保護された環境変数で扱います。

## モデルカタログ

デプロイファイルは、リポジトリ内の `data/model-catalog.yaml` をバックエンドコンテナの `/app/catalog/model-catalog.yaml` にマウントします。

標準モデルカタログを更新する手順:

1. `data/model-catalog.yaml` を編集します。
2. バックエンドコンテナを再起動します。
3. 管理コンソールの `Model Catalog` で結果を確認します。

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.yml restart tokenhub-backend
```

## Caddy ゲートウェイ

デフォルトの Compose デプロイでは、Caddy がホスト側ポートを公開する唯一のサービスです。管理コンソールを提供し、パスを書き換えずに `/api/*` と `/v1/*` を内部 TokenHub バックエンドへプロキシします。付属設定は信頼できる社内ネットワーク向けの HTTP のみです。必要に応じて Caddy に TLS とアクセス制限を追加してください。

長いモデル応答に備えて、リクエストボディサイズとストリーミングタイムアウトを十分に設定してください。

Liveness には `/livez`、Readiness には `/readyz` を使用します。データベースが利用できない場合、`/readyz` と後方互換の `/healthz` は `503` を返します。
