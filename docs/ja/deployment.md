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

デフォルトのインストールでは、SQLite を使用するフロントエンド 1 台とバックエンド 1 台を起動します。水平スケールが必要で、データベースを Compose プロジェクト外で管理する場合は `deploy/docker-compose.remote-postgres.yml` を使用します。この構成はスケール可能なバックエンドとフロントエンドの前に Nginx ゲートウェイを配置し、ローカルデータベースを起動しません。

```mermaid
flowchart TB
    clients["クライアント<br/>管理コンソール · OpenAI SDK"] --> nginx["Nginx ゲートウェイ<br/>負荷分散 · ヘルスチェック"]
    nginx --> frontend["フロントエンドレプリカ × N"]
    frontend --> backend["バックエンドレプリカ × N"]
    backend <--> providers["モデル Provider"]

    local["data/model-catalog.yaml<br/>モデルのマスターデータ"] -->|"起動時に解析して upsert<br/>クラスタリースでレプリカを直列化"| backend
    providerCatalog["data/provider-catalog.json<br/>バージョン管理された Provider テンプレートと候補モデル"] -->|"管理者による Provider の作成・更新"| backend
    backend <-->|"モデル · ルート · Provider カタログスナップショット<br/>共有状態 · データベースロック"| postgres[("共有 PostgreSQL")]

    backend -->|"Provider を作成"| rule["ルート作成ルール<br/>Provider 候補モデル ∩ ローカル Model → Route"]
    local -.-> rule
    providerCatalog -.-> rule
    rule -->|"一致する Route を作成"| postgres
```

マルチインスタンスモードでは：

- Nginx が管理コンソール、API、ヘルスチェックのトラフィックを正常なレプリカへ分散します。
- バックエンドレプリカは、永続設定、OAuth セッション、クォータカウンター、監査データ、クラスターロック、実行中リクエストの並行数リースを PostgreSQL で共有します。
- リースの期限と所有権は PostgreSQL のクロックで判定し、ホスト間の時刻ずれによる早期引き継ぎを防ぎます。所有権を失った処理はハートビートによってキャンセルされます。
- 設定されたモデルカタログはバックエンドの起動ごとに同期され、冪等な同期処理はクラスターロックによって直列化されます。
- Provider テンプレートと候補モデルは、リポジトリでバージョン管理されたローカルカタログから読み込まれ、実行時にリモートカタログサービスへ依存しません。
- バックエンドはローカル Provider カタログのスナップショットを PostgreSQL に永続化するため、全レプリカで同じカタログを使用できます。ローカルファイルがない場合は、データベースへ保存された組み込みテンプレートにフォールバックします。
- データベースの調整障害では Provider の容量だけを解放し、正常なモデル Provider を誤って失敗扱いにしません。

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
- `TOKENHUB_IMAGE_TAG`: バックエンドとフロントエンドで共通のイメージタグ。デフォルトは `latest`。
- `TOKENHUB_PUBLIC_BASE_URL`: ユーザーに表示するバックエンド URL。
- `TOKENHUB_API_BASE_URL`: ブラウザの管理コンソールが使用するバックエンド URL。フロントエンドサーバーが実行時に読み取ります。非推奨の `NEXT_PUBLIC_API_BASE_URL` は、1 回の互換期間に限りフォールバックとして残します。
- `TOKENHUB_BACKEND_PORT`: バックエンドのホスト側ポート。デフォルトは `8080`。
- `TOKENHUB_FRONTEND_PORT`: 管理コンソールのホスト側ポート。デフォルトは `3000`。

リポジトリルートから起動します。

```bash
./deploy/install.sh
```

スクリプトは Compose の環境変数を検証し、公開済みイメージを取得して、ローカルではビルドせずにコンテナを起動します。GHCR イメージの初回公開中に取得できない場合は、現在のチェックアウトからのビルドへ自動的に切り替えます。秘密値を表示せずに安全でない変数を個別に報告します。Compose が失敗し、その試行で作成または再起動したバックエンドコンテナが exited、restarting、dead、unhealthy のいずれかである場合、その試行のバックエンドログを最大 100 行表示します。バックエンド以外の障害では、無関係なバックエンドログを出力しません。

イメージを取得したりコンテナを起動したりせず、設定だけを検証するには次を実行します。

```bash
./deploy/install.sh --check-only
```

別の環境ファイルを使用する場合は、`./deploy/install.sh --env-file /path/to/deploy.env` を実行します。

### 公開イメージのバージョンルール

GitHub Actions は `linux/amd64` と `linux/arm64` 向けに `ghcr.io/astaxie/tokenhub-backend` と `ghcr.io/astaxie/tokenhub-frontend` を公開します。

- GitHub Release を公開すると、完全なセマンティックバージョンのタグを自動生成します。プレリリースでない場合は、メジャー・マイナータグと `latest` も更新します。
- `workflow_dispatch` では `edge` または分離された `manual-*` タグのみを公開でき、正式なリリースタグや `latest` は上書きできません。
- PR ではコンテナイメージをビルドまたは push しません。
- `main` へのマージではイメージを公開しません。

ワークフローは、まず実行ごとのステージングタグで各イメージを push し、両方のマルチプラットフォームイメージが存在することを確認してから最終タグを公開します。バックエンドとフロントエンドには同じ `TOKENHUB_IMAGE_TAG` を使用してください。本番環境では `latest` ではなく、完全なリリースタグを固定することを推奨します。

GHCR で初めて公開した Package はデフォルトで非公開です。匿名デプロイを有効にする前に、リポジトリ所有者が両方の Package を Public に変更する必要があります。それまでは、デフォルトの `latest` タグを使用するデプロイに限り、取得に失敗するとローカルのソースビルドへ自動的に切り替えます。明示した `TOKENHUB_IMAGE_TAG` を取得できない場合、現在のソースをそのバージョンとして扱わず、インストールスクリプトは終了します。

### 任意: ローカルビルド

現在のチェックアウトからイメージをビルドする場合は、次を実行します。

```bash
./deploy/install.sh --build
```

以下の高速化設定は、ローカルのソースビルドにのみ適用されます。

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

- バックエンド: `http://localhost:8080`
- フロントエンド: `http://localhost:3000`
- SQLite データ: Docker named volume `tokenhub-data`
- モデルカタログ: 選択したバックエンドイメージに含まれるバージョン

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
| `TOKENHUB_PUBLIC_BASE_URL` | `http://localhost:8080` | ユーザーに表示するバックエンド URL |
| `TOKENHUB_TRUSTED_PROXY_CIDRS` | 空 | `X-Forwarded-For` を提供できるプロキシ IP または CIDR（カンマ区切り） |
| `TOKENHUB_CORS_ALLOWED_ORIGINS` | 公開 URL | バックエンドを呼び出せるブラウザー Origin（カンマ区切り） |
| `TOKENHUB_ADMIN_TOKEN` | `change-me-tokenhub-admin-token` | Admin API 用の初期 Token |
| `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD` | `change-me-tokenhub-admin-password` | 初期 `admin` ユーザーのパスワード。本番起動前に変更が必要 |
| `TOKENHUB_SECRET_KEY` | `change-me-tokenhub-secret-key` | バックエンド秘密鍵 |
| `TOKENHUB_DATABASE_URL` | `sqlite:///app/data/tokenhub.db` | コンテナ内 SQLite データベースパス |
| `TOKENHUB_DB_HOST` | 空 | PostgreSQL ホスト。設定すると `TOKENHUB_DATABASE_URL` ではなく `TOKENHUB_DB_*` の各フィールドから DSN を組み立てるため、パスワードに `#`、`?`、`/`、`%` が含まれる場合の URL エンコードを回避できます。両方設定した場合は `TOKENHUB_DATABASE_URL` が優先されます |
| `TOKENHUB_DB_PORT` | `5432` | PostgreSQL ポート。`TOKENHUB_DB_HOST` を設定した場合にのみ使用されます |
| `TOKENHUB_DB_USER` | 空 | PostgreSQL ユーザー。`TOKENHUB_DB_HOST` を設定した場合にのみ使用されます |
| `TOKENHUB_DB_PASSWORD` | 空 | PostgreSQL パスワード。`TOKENHUB_DB_HOST` を設定した場合にのみ使用されます |
| `TOKENHUB_DB_NAME` | 空 | PostgreSQL データベース名。`TOKENHUB_DB_HOST` を設定した場合にのみ使用されます |
| `TOKENHUB_DB_SSLMODE` | `disable` | PostgreSQL sslmode。`TOKENHUB_DB_HOST` を設定した場合にのみ使用されます |
| `TOKENHUB_SQLITE_BACKUP_DIR` | `/app/data/backups` | バックアップ出力ディレクトリ |
| `TOKENHUB_MODEL_CATALOG_FILE` | `/app/catalog/model-catalog.yaml` | 標準モデルカタログファイル |
| `TOKENHUB_PROVIDER_CATALOG_FILE` | `/app/catalog/provider-catalog.json` | Provider テンプレートと候補モデルのカタログファイル |
| `TOKENHUB_SEED_DEMO` | `false` | デモデータを投入するか |
| `TOKENHUB_RESOURCE_FAILURE_THRESHOLD` | `3` | Provider リソースをクールダウンするまでの失敗しきい値 |
| `TOKENHUB_RESOURCE_COOLDOWN_SECONDS` | `300` | クールダウンした Provider リソースがハーフオープン再試行を得るまでの基本待機秒数 |
| `TOKENHUB_RESOURCE_COOLDOWN_MAX_SECONDS` | `3600` | 復旧失敗が続く場合の指数バックオフの上限秒数 |
| `TOKENHUB_METRICS_ENABLED` | `false` | Prometheus メトリクスを収集し `GET /metrics` を提供 |
| `TOKENHUB_METRICS_TOKEN` | 空 | `/metrics` の Bearer トークン。空の場合は管理者トークンにフォールバック |
| `TOKENHUB_METRICS_PROJECT_LABEL` | `false` | ゲートウェイメトリクスに `project_id` を追加。プロジェクト数だけ系列数が増加 |
| `TOKENHUB_IN_FLIGHT_LEASE_TTL_SECONDS` | `300` | クラスター全体の同時実行リースの期限と更新間隔の基準 |
| `TOKENHUB_CLUSTER_LOCK_TTL_SECONDS` | `180` | クラスター調整ロックの期限と更新間隔の基準 |
| `TOKENHUB_GRACEFUL_SHUTDOWN_SECONDS` | `150` | 停止時に処理中リクエストを待機する最大秒数 |
| `TOKENHUB_STOP_GRACE_PERIOD` | `180s` | Docker がバックエンドを強制停止するまでの Compose 猶予時間 |
| `TOKENHUB_CACHE_AFFINITY_ENABLED` | `false` | 同一セッションを同一の上流アカウントに固定し、上流の prompt cache が継続的にヒットするようにします。ルーティング挙動を変えるため既定では無効 |
| `TOKENHUB_CACHE_AFFINITY_MODELS` | 空 | 段階的ロールアウト用のモデル許可リスト（カンマ区切り）。空の場合は全モデルが対象 |
| `TOKENHUB_CACHE_AFFINITY_ALLOW_USER_SCOPE` | `false` | ユーザー単位の識別子もアフィニティキーとして受け入れるか。同一ユーザーの並行セッションが同じ値を共有し単一アカウントに集中するため既定では無効 |

## フロントエンド環境変数

| 変数 | デフォルト | 説明 |
| --- | --- | --- |
| `TOKENHUB_API_BASE_URL` | `http://localhost:8080` | フロントエンドサーバーが実行時に読み取るバックエンド Admin API URL |
| `NEXT_PUBLIC_API_BASE_URL` | 空 | 非推奨の互換フォールバック。`TOKENHUB_API_BASE_URL` へ移行してください |

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

## カタログファイル

公開済みバックエンドイメージには、対応するバージョンの `data/model-catalog.yaml` と `data/provider-catalog.json` が `/app/catalog/` に含まれます。デフォルトのデプロイではこれらのファイルを使用し、バックエンドプログラムと両方のカタログを同じイメージバージョンにそろえます。Provider カタログは PublicProviderConf のデータをリポジトリへ取り込んで管理しており、TokenHub は実行時にリモートカタログを取得しません。

カスタムモデルカタログを使用する場合は、マウントするファイルを明示します。

```bash
./deploy/install.sh --model-catalog /absolute/path/to/model-catalog.yaml
```

カスタムファイルはイメージ内のモデルカタログを上書きするため、そのバージョンは `TOKENHUB_IMAGE_TAG` とは別に管理します。ファイルを更新した後、バックエンドコンテナを再起動し、管理コンソールの `Model Catalog` で内容を確認します。

設定済みカタログファイルを更新した後は、バックエンドを再起動するか、管理コンソールの Model Catalog で「出荷時カタログに復元」を実行して現在のファイルを再インポートできます。手動で追加したその他のモデルは保持されます。

`data/model-catalog.yaml` はモデルのマスターデータおよびルートの許可リストです。`data/provider-catalog.json` は Provider テンプレートと候補モデルを提供し、候補モデルがモデルカタログにも存在する場合だけルートが作成されます。カスタム Provider カタログを使うには、同じ `providers` 構造を持つローカル JSON ファイルを `TOKENHUB_PROVIDER_CATALOG_FILE` に指定します。

## リバースプロキシ

本番環境では HTTPS の背後に置き、次のように転送してください。

- 管理コンソールのトラフィックはフロントエンドサービスへ。
- `/v1/*` と `/api/admin/*` はバックエンドサービスへ。

長いモデル応答に備えて、リクエストボディサイズとストリーミングタイムアウトを十分に設定してください。

Liveness には `/livez`、Readiness には `/readyz` を使用します。データベースが利用できない場合、`/readyz` と後方互換の `/healthz` は `503` を返します。
