# デプロイ

Language: [English](../deployment.md) | [简体中文](../zh-CN/deployment.md) | 日本語

TokenHub は、Go バックエンド、Next.js 管理コンソール、SQLite 永続化で構成されるプライベートデプロイ向けのサービスです。

## データベースの選択

TokenHub は 2 種類のデータベースバックエンドをサポートしています。

以下のコマンドは Docker Compose を使用します。どちらのバックエンドも Docker なしで同様にサポートされます。[ネイティブ Release + systemd](#ネイティブ-release--systemd)を参照してください。

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
docker compose --env-file deploy/.env -f deploy/docker-compose.yml up -d --remove-orphans
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
docker compose --env-file deploy/.env -f deploy/docker-compose.postgres.yml up -d --remove-orphans
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

    local["data/model-catalog.yaml<br/>候補モデルのメタデータ"] -->|"起動時に解析して候補テンプレートを upsert<br/>クラスタリースでレプリカを直列化"| backend
    providerCatalog["data/provider-catalog.json<br/>バージョン管理された Provider テンプレートと候補モデル"] -->|"管理者による Provider の作成・更新"| backend
    backend <-->|"モデル · ルート · Provider カタログスナップショット<br/>共有状態 · データベースロック"| postgres[("共有 PostgreSQL")]

    backend -->|"Provider を作成"| rule["ルート作成ルール<br/>明示的に有効化した候補 → Model に追加 → Route<br/>自動候補 ∩ ローカル Model → Route"]
    local -.-> rule
    providerCatalog -.-> rule
    rule -->|"一致する Route を作成"| postgres
```

マルチインスタンスモードでは：

- Nginx が管理コンソール、API、ヘルスチェックのトラフィックを正常なレプリカへ分散します。
- バックエンドレプリカは、永続設定、OAuth セッション、クォータカウンター、監査データ、クラスターロック、実行中リクエストの並行数リースを PostgreSQL で共有します。
- リースの期限と所有権は PostgreSQL のクロックで判定し、ホスト間の時刻ずれによる早期引き継ぎを防ぎます。所有権を失った処理はハートビートによってキャンセルされます。
- 設定されたモデルカタログの候補モデルメタデータはバックエンドの起動ごとに同期され、冪等な同期処理はクラスターロックによって直列化されます。
- Provider テンプレートと候補モデルは、リポジトリでバージョン管理されたローカルカタログから読み込まれ、実行時にリモートカタログサービスへ依存しません。
- バックエンドはローカル Provider カタログのスナップショットを PostgreSQL に永続化するため、全レプリカで同じカタログを使用できます。ローカルファイルがない場合は、データベースへ保存された組み込みテンプレートにフォールバックします。
- データベースの調整障害では Provider の容量だけを解放し、正常なモデル Provider を誤って失敗扱いにしません。

`deploy/.env` にリモート `TOKENHUB_DATABASE_URL`、公開ゲートウェイ URL、本番用シークレット、信頼するプロキシ CIDR、および必要な `TOKENHUB_BACKEND_REPLICAS` と `TOKENHUB_FRONTEND_REPLICAS` を設定して実行します。

```bash
docker compose --env-file deploy/.env \
  -f deploy/docker-compose.remote-postgres.yml up -d
```

すべてのレプリカで同じ `TOKENHUB_SECRET_KEY` を使用してください。`TOKENHUB_DB_MAX_OPEN_CONNS` はレプリカ単位なので、合計接続数が PostgreSQL の上限を下回るように設定します。SQLite ファイルを複数のバックエンドで共有してはいけません。

`./deploy/test-multi-instance.sh` で実際の 2 インスタンス PostgreSQL E2E テストを実行できます。

## ネイティブ Release + systemd

systemd を使用する単一 Linux ホストでは、ネイティブ Release インストールを利用できます。ネイティブパッケージは `linux/amd64` と `linux/arm64` に対応し、Go バックエンド、スタンドアロン Next.js コンソール、対応する Node.js ランタイムを含みます。

インストーラーをダウンロードして内容を確認し、最新の安定版 Release をインストールします。

```bash
curl -fsSL https://raw.githubusercontent.com/astaxie/TokenHub/main/deploy/native/install.sh \
  -o /tmp/tokenhub-install.sh
sudo bash /tmp/tokenhub-install.sh install
```

`TOKENHUB_PUBLIC_HOST` が未設定の場合、インストーラーは `https://ipinfo.io/json` に問い合わせ、検証済みの IP を使用します。取得できない場合は、`hostname -I` の最初のアドレス、次に `127.0.0.1` へフォールバックします。NAT、プロキシ、ロードバランサーの背後では、検出した送信元 IP とユーザーがアクセスする受信先アドレスが異なることがあるため、その場合は `TOKENHUB_PUBLIC_HOST` を明示的に設定してください。IPv6 リテラルを使用すると、URL の生成時に角括弧が自動的に追加されます。

```bash
sudo env TOKENHUB_PUBLIC_HOST=tokenhub.example.com \
  bash /tmp/tokenhub-install.sh install
```

解決したホストは `/etc/tokenhub/tokenhub.env` に保存され、その後のインストーラー実行でも再利用されます。これにより、自動 IP 検出の結果が変化してもアップグレード時の表示 URL は変わりません。

デフォルトの SQLite ではなく、初回起動から PostgreSQL を使用する場合は、最初のインストール時にデータベース URL を渡します。

```bash
sudo env \
  TOKENHUB_DATABASE_URL='postgres://user:password@db.example.com:5432/tokenhub?sslmode=require' \
  bash /tmp/tokenhub-install.sh install
```

インストーラーがこの値を `/etc/tokenhub/tokenhub.env` に書き込むのは、設定を初めて作成するときだけです。その後の install、upgrade、rollback では既存の設定を保持します。データベースを意図的に変更する場合は、このファイルを編集して TokenHub を再起動してください。

初回インストールでは、本番用シークレットと初期管理者パスワードが生成されます。パスワードは一度だけ表示されます。実行ファイルは次の場所に分けて保存されます。

- Release と `current` シンボリックリンク: `/opt/tokenhub`
- 設定とシークレット: `/etc/tokenhub/tokenhub.env`
- SQLite データベースとバックアップ: `/var/lib/tokenhub`
- 生成画像: `/var/lib/tokenhub/images`
- Linux systemd ユニット: `/etc/systemd/system/tokenhub.service`

公開 URL、CORS Origin、ポート、データベース、シークレットを変更する場合は `/etc/tokenhub/tokenhub.env` を編集して、サービスを再起動します。

```bash
sudo systemctl restart tokenhub
sudo systemctl status tokenhub
sudo journalctl -u tokenhub -f
```

インストーラーは、Release アーカイブを `checksums.txt` で検証してから有効化し、アップグレード時も設定とデータを保持します。

```bash
sudo bash /tmp/tokenhub-install.sh upgrade
sudo bash /tmp/tokenhub-install.sh upgrade --version 0.3.3
sudo bash /tmp/tokenhub-install.sh rollback --version 0.3.2
sudo bash /tmp/tokenhub-install.sh uninstall
```

`upgrade` は、インストール済みバージョンより古い対象を拒否します。ダウングレードする場合は、明示的に `rollback` を使用してください。古いインストーラーで作成した環境をアップグレードすると、`TOKENHUB_IMAGE_STORAGE_DIR` が未設定の場合に限り、永続画像ディレクトリとして `/var/lib/tokenhub/images` が自動的に追加されます。

`uninstall` は `/etc/tokenhub` と `/var/lib/tokenhub` を保持します。設定とアプリケーションデータも削除する場合に限り、`uninstall --purge` を使用してください。
インストーラーは、アプリケーション、設定、状態の各ディレクトリに所有権マーカーを記録します。マーカーがない、または現在の設定と一致しないディレクトリは再帰削除されず、`/opt`、`/etc`、`/var/lib` などのシステムレベルのパスを管理対象ディレクトリとして指定することもできません。初回インストールでは、バックエンドとフロントエンドに同じポートを指定できません。`ss` または `lsof` が利用できる場合は、Release のダウンロード前に使用中のポートも拒否します。インストールまたはアップグレードは、systemd ユニットが active になり、バックエンドのヘルスチェックと管理コンソールの両方が応答した後にのみ成功と報告されます。Readiness に失敗した場合は、直近のサービスログも出力します。

fork をテストする場合は、その fork のインストーラーをダウンロードし、公開 Release リポジトリを指定します。

```bash
sudo env TOKENHUB_RELEASE_REPOSITORY=your-account/TokenHub \
  bash /tmp/tokenhub-install.sh install --version 0.3.3
```

ネイティブ Release インストールは、バージョンパネルに「ネイティブ Release」と表示されます。管理者はパネルから更新またはロールバックを直接ダウンロードして検証し、「今すぐ再起動」を選択して systemd で対象バージョンを有効化できます。各 GitHub Release タグは `v` で始まる厳密なセマンティックバージョンで、Linux アーカイブと `checksums.txt` を含む必要があります。Release の公開時に `.github/workflows/native-release.yml` が `linux/amd64` と `linux/arm64` のファイルをビルドして添付します。
ダウンロードおよび検証済みの Release はローカルに保持されるため、GitHub Releases API に接続できない場合でもロールバックできます。

## Docker Compose

デプロイ用の環境変数ファイルを作成します。

```bash
cp deploy/.env.example deploy/.env
```

起動前に `deploy/.env` を編集してください。

- `TOKENHUB_ADMIN_TOKEN`: Admin API の初期 Token。32 バイト以上のランダム値を使用してください。
- `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD`: 初期 `admin` ユーザーの作成時にのみ使用するパスワード。12 バイト以上にしてください。
- `TOKENHUB_SECRET_KEY`: バックエンド秘密鍵。32 バイト以上のランダム値を使用し、安定して保持してください。
- `TOKENHUB_IMAGE_TAG`: 管理対象 TokenHub イメージのタグ。デフォルトは `latest`。
- `TOKENHUB_PUBLIC_BASE_URL`: ユーザーに表示するバックエンド URL。
- `TOKENHUB_API_BASE_URL`: ブラウザの管理コンソールが使用するバックエンド URL。フロントエンドサーバーが実行時に読み取ります。非推奨の `NEXT_PUBLIC_API_BASE_URL` は、1 回の互換期間に限りフォールバックとして残します。
- `TOKENHUB_BACKEND_PORT`: バックエンドのホスト側ポート。デフォルトは `8080`。
- `TOKENHUB_FRONTEND_PORT`: 管理コンソールのホスト側ポート。デフォルトは `3000`。
- `TOKENHUB_BACKEND_REPLICAS`: リモート PostgreSQL Compose のバックエンドレプリカ数。デフォルトは `2`。
- `TOKENHUB_FRONTEND_REPLICAS`: リモート PostgreSQL Compose のフロントエンドレプリカ数。デフォルトは `2`。

リポジトリルートから起動します。

```bash
./deploy/install.sh
```

スクリプトは Compose の環境変数を検証し、公開済みイメージを取得して、ローカルではビルドせずに管理対象アプリケーションコンテナを起動します。Compose のヘルスチェックが成功するまで最大 180 秒待ってから成功を報告します。以前の 2 コンテナ構成から更新する場合、廃止された個別フロントエンドコンテナを削除しますが、`tokenhub-data` ボリュームは保持します。GHCR イメージの初回公開中に取得できない場合は、現在のチェックアウトからのビルドへ自動的に切り替えます。秘密値を表示せずに安全でない変数を個別に報告します。新しいバックエンドが起動に失敗するか healthy にならない場合、その試行のログを最大 100 行表示します。

インストーラーは現在の `docker compose` CLI プラグインを優先し、それがなく旧式コマンドだけが利用可能な場合に `docker-compose` へフォールバックします。`config --format` は利用できるものの `config --environment` を持たない Compose リリースにも対応し、この互換経路では `python3` が必要です。

イメージを取得したりコンテナを起動したりせず、設定だけを検証するには次を実行します。

```bash
./deploy/install.sh --check-only
```

別の環境ファイルを使用する場合は、`./deploy/install.sh --env-file /path/to/deploy.env` を実行します。

### 公開イメージのバージョンルール

GitHub Actions は `linux/amd64` と `linux/arm64` 向けに完全な `ghcr.io/astaxie/tokenhub-backend` イメージを公開します。互換性のためイメージ名は維持しますが、バックエンド、スタンドアロン Next.js コンソール、Node.js ランタイム、コンテナスーパーバイザーを含みます。

- 厳密な `v` プレフィックス付きセマンティックタグで GitHub Release を公開すると、対応する数値イメージタグを自動生成します。プレリリースでない場合は、メジャー・マイナータグと `latest` も更新します。
- `workflow_dispatch` では `edge` または分離された `manual-*` タグのみを公開でき、正式なリリースタグや `latest` は上書きできません。
- PR ではコンテナイメージをビルドまたは push しません。
- `main` へのマージではイメージを公開しません。

ワークフローは、まず実行ごとのステージングタグでマルチプラットフォームイメージを push して検証し、その後に最終タグを公開します。本番環境では `latest` ではなく、完全なリリースタグを固定することを推奨します。

GHCR で初めて公開した Package はデフォルトで非公開です。匿名デプロイを有効にする前に、リポジトリ所有者がその Package を Public に変更する必要があります。それまでは、デフォルトの `latest` タグを使用するデプロイに限り、取得に失敗するとローカルのソースビルドへ自動的に切り替えます。明示した `TOKENHUB_IMAGE_TAG` を取得できない場合、現在のソースをそのバージョンとして扱わず、インストールスクリプトは終了します。

### Docker のバージョン状態とロールバック

プラットフォーム管理者は TokenHub ロゴの下にあるバージョンバッジを選択すると、実行中のバージョン、最新の安定版 GitHub Release、最大 3 件の過去の安定版を確認できます。正式なイメージビルドには公開ワークフローから正確なバージョンが設定され、ローカルのソースビルドにはパッケージバージョンとソースビルドの表示が使用されます。管理対象の更新、ロールバック、再起動リクエストは管理者監査ログに記録されます。

バージョン確認は、タイムアウト付きの送信 HTTPS リクエストで公開 GitHub Releases API にアクセスし、成功結果を 20 分間キャッシュします。デフォルトでは `astaxie/TokenHub` を確認します。fork の Release を検証する場合、管理者は `TOKENHUB_RELEASE_REPOSITORY` に信頼できる公開 `owner/repository` を設定できます。GitHub の障害や Release がまだない状態でもゲートウェイトラフィックには影響せず、パネルは現在のバージョンを保ったまま利用不可の状態を表示します。

たとえば、ソース実行中に fork の Release を確認するには次を実行します。

```bash
TOKENHUB_RELEASE_REPOSITORY=your-account/TokenHub ./start.sh
```

デフォルトの SQLite およびローカル PostgreSQL Compose は、1 つの管理対象アプリケーションコンテナを使用します。管理者は「今すぐ更新」を選択し、チェックサム検証済みのプラットフォーム Release バンドルが `tokenhub-releases` ボリュームへインストールされた後に「今すぐ再起動」を選択できます。応答後にプロセスが終了し、Docker の `restart: unless-stopped` が対象バージョンのバックエンドとフロントエンドを同時に起動します。Docker Socket のマウントやホスト daemon の操作は行いません。

新しく取得したイメージがこのボリュームを初めて使用すると、そのイメージバージョンとコンテンツフィンガープリントが基準になります。画面から適用したバージョン、`current` リンク、履歴 Release は `tokenhub-releases` に保存されるため、同じイメージでの通常の再起動やコンテナ再作成でも更新結果は保持されます。別のイメージを取得した場合や、同じバージョンで異なるソースを再ビルドした場合は、新しいイメージ内容が有効化されます。リモート PostgreSQL のマルチインスタンス Compose では、管理リクエストを受けた 1 レプリカだけが変わることによるバージョン分裂を防ぐため、インプレース更新を無効化します。このモードのパネルでは、設定済みのレプリカ数を保持するため、元の Compose ファイルと環境設定を使用した手動更新を案内します。ソースデプロイでも手動更新の案内を維持します。ロールバック前にはデータベースをバックアップし、対象リリースが現在のスキーマをサポートすることを確認してください。

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

## ローカルで本番ビルドを実行する（Docker なし）

`deploy/local/run-local.sh` は、Docker も root も systemd も使わずに、自分のマシンで本番ビルドからバックエンドとコンソールを実行します。これは開発用の補助手段であり、デプロイ手段ではありません。サーバーに TokenHub をインストールする場合は[ネイティブ Release + systemd](#ネイティブ-release--systemd) または [Docker Compose](#docker-compose) を使用してください。

```bash
./deploy/local/run-local.sh          # フォアグラウンド。Ctrl-C で両方停止
./deploy/local/run-local.sh -d       # バックグラウンド。すぐに戻る
./deploy/local/run-local.sh status
./deploy/local/run-local.sh logs -f
./deploy/local/run-local.sh stop
```

必要に応じて両コンポーネントをビルドし、ループバック上で実行します。バイナリ、コンソールバンドル、データベース、ログ、pid ファイルはすべてリポジトリ内の `.tokenhub/`（gitignore 済み）に置かれ、そのディレクトリを削除すればインスタンスをリセットできます。ビルド時にはフロントエンドの通常の無視対象成果物（`frontend/node_modules`、`frontend/.next`）も更新されることがあります。システム全体へのインストールもサービスアカウントの作成も行いません。

実行されるのは dev サーバーではなく**本番ビルド**（デプロイ時と同じ standalone バンドル）なので、本番ビルドでのみ現れる問題を検出できます。開発用の資格情報（`admin` / `admin123456`）を使用し、ループバックのみにバインドし、データは `.tokenhub/tokenhub.db` の SQLite に保存されます。

`-d` を付けるとサービスは起動元のシェルから切り離され、シェルの終了後もターミナルを閉じた後も動き続けます。ただし再起動後は自動で復帰しません。それが必要な場合は正式なインストール方法を使用してください。どちらのモードでも pid ファイルを記録するため、`status` と `stop` はフォアグラウンドのインスタンスにも有効です。`stop` はシグナルを送る前に記録された pid が本当にこのインスタンスのものかを検証するので、再利用された pid を誤って kill することはありません。また起動前に両方のポートを確保するため、既に他のサービスが応答しているポートを「起動成功」と誤認することもありません。

バックエンドは cgo 経由で SQLite をリンクするため、Go（バージョンは `backend/go.mod` を参照）、Node 22 以上、npm、C コンパイラが必要です。

以上は Linux で検証済みです。macOS には `setsid` がないため、停止時はプロセスツリーをたどる方式にフォールバックします。この経路は実装済みですが macOS 上では未検証です。

オプション: `--rebuild`、`--reset`（ローカルデータベースを破棄）、`--backend-port N`、`--console-port N`、`restart`。

## バックエンド環境変数

| 変数 | デフォルト | 説明 |
| --- | --- | --- |
| `TOKENHUB_ENV` | `prod` | ランタイム環境名 |
| `TOKENHUB_HTTP_ADDR` | `:8080` | バックエンド待受アドレス |
| `TOKENHUB_PUBLIC_BASE_URL` | `http://localhost:8080` | ユーザーに表示するバックエンド URL |
| `TOKENHUB_RELEASE_REPOSITORY` | `astaxie/TokenHub` | バージョン確認に使用する信頼済み公開 GitHub リポジトリ。形式は `owner/repository` |
| `TOKENHUB_DEPLOYMENT_TYPE` | ビルド時の値 | バイナリに埋め込まれたデプロイ種別を上書きします: `source`、`container`、`native`。Compose ファイルは `container` を設定します |
| `TOKENHUB_MANAGED_UPDATES` | `false` | コンテナデプロイでオンライン更新とロールバックを許可します。ネイティブデプロイでは常に許可されます |
| `TOKENHUB_INSTALL_ROOT` | `/opt/tokenhub` | 管理対象 Release のオンライン更新とロールバックで使用するインストールルート |
| `TOKENHUB_TRUSTED_PROXY_CIDRS` | 空 | `X-Forwarded-For`、`X-Forwarded-Host`、`X-Forwarded-Proto` を提供できるプロキシ IP または CIDR（カンマ区切り）。信頼済みプロキシはクライアント値を転送せず、これらのヘッダーを上書きする必要があります |
| `TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS` | 空 | カスタムプロバイダーの base URL としてリテラル IP を許可するプライベート CIDR（RFC1918/ULA のみ、カンマ区切り、社内モデルサーバー向け）。明示的に許可したプライベートリテラルでは HTTP を使用できますが、公開プロバイダー URL には HTTPS が必須です。プライベートアドレスに解決されるホスト名とリダイレクト先は引き続き拒否 |
| `TOKENHUB_PROVIDER_UPSTREAM_NAT64_PREFIX` | 空 | 埋め込まれた IPv4 宛先を分類するための任意の RFC 6052 DNS64/NAT64 プレフィックス。32、40、48、56、64、96 ビット長をサポートします。`64:ff9b:1::/48` などのネットワーク固有プレフィックスを使用する場合に設定します。標準の `64:ff9b::/96` は設定不要です |
| `TOKENHUB_PROVIDER_UPSTREAM_ALLOW_LOOPBACK` | `false` | ローカルの Ollama/LM Studio 開発用に、provider base URL の `localhost`、`127.0.0.1`、`::1`（HTTP URL を含む）を明示的に許可します。公開プロバイダー URL には HTTPS が必須です。本番環境では無効のままにしてください |
| `TOKENHUB_CORS_ALLOWED_ORIGINS` | 公開 URL | バックエンドを呼び出せる正確なブラウザー Origin（カンマ区切り）。設定時は同じ一覧が OAuth コンソールの戻り先 Origin の完全一致 allowlist にもなります。各値には scheme、host、任意の port だけを含め、path は含めません |
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
| `TOKENHUB_MODEL_CATALOG_FILE` | `/opt/tokenhub/current/catalog/model-catalog.yaml` | 管理対象デプロイの標準モデルカタログファイル |
| `TOKENHUB_PROVIDER_CATALOG_FILE` | `/opt/tokenhub/current/catalog/provider-catalog.json` | 管理対象デプロイの Provider テンプレートと候補モデルのカタログファイル |
| `TOKENHUB_SEED_DEMO` | `false` | デモデータを投入するか |
| `TOKENHUB_RESOURCE_FAILURE_THRESHOLD` | `3` | Provider リソースをクールダウンするまでの失敗しきい値 |
| `TOKENHUB_RESOURCE_COOLDOWN_SECONDS` | `300` | クールダウンした Provider リソースがハーフオープン再試行を得るまでの基本待機秒数 |
| `TOKENHUB_RESOURCE_COOLDOWN_MAX_SECONDS` | `3600` | 復旧失敗が続く場合の指数バックオフの上限秒数 |
| `TOKENHUB_METRICS_ENABLED` | `false` | Prometheus メトリクスを収集し `GET /metrics` を提供 |
| `TOKENHUB_METRICS_TOKEN` | 空 | `/metrics` の Bearer トークン。空の場合は管理者トークンにフォールバック |
| `TOKENHUB_METRICS_PROJECT_LABEL` | `false` | ゲートウェイメトリクスに `project_id` を追加。プロジェクト数だけ系列数が増加 |
| `TOKENHUB_TRACING_ENABLED` | `false` | ゲートウェイ呼び出しごとに 1 本の OpenTelemetry トレースを OTLP/HTTP でエクスポート |
| `TOKENHUB_TRACING_ENDPOINT` | 空 | シグナル固有の OTLP traces URL。そのまま使用。Langfuse は `<host>/api/public/otel/v1/traces` |
| `TOKENHUB_TRACING_HEADERS` | 空 | カンマ区切りの `name=value` エクスポートヘッダー。資格情報を含む |
| `TOKENHUB_TRACING_CAPTURE_PAYLOADS` | `false` | プロンプト・レスポンス・上流エラー本文をエクスポートする span に含める |
| `TOKENHUB_TRACING_SAMPLE_RATIO` | `1` | エクスポートする割合。0 から 1 |
| `TOKENHUB_TRACING_TIMEOUT_SECONDS` | `10` | 1 回のエクスポート試行の時間上限 |
| `TOKENHUB_TRACING_QUEUE_SIZE` | `2048` | span 化を待つ完了イベント数。満杯時はリクエストを遅らせずトレースを破棄 |
| `TOKENHUB_UPSTREAM_NON_STREAM_TIMEOUT_SECONDS` | `120` | 非ストリーミングの上流リクエスト 1 件あたりの全体タイムアウト |
| `TOKENHUB_UPSTREAM_STREAM_IDLE_TIMEOUT_SECONDS` | `300` | ストリーミング呼び出しに全体タイムアウトはありません。この値はレスポンスヘッダーの待機時間と、その後ストリームが無音でいられる時間を制限します。1 バイト受信するたびに計測し直します |
| `TOKENHUB_MAX_JSON_REQUEST_BYTES` | `8388608`（8 MiB） | `/v1` エンドポイントの JSON リクエストボディ上限。生のバイト数または二進接尾辞（`8m`、`8mib`、`512k`）を指定できます。512 MiB を超える値は上限に丸められます |
| `TOKENHUB_MAX_MULTIMODAL_REQUEST_BYTES` | `33554432`（32 MiB） | マルチモーダルのチャットエンドポイント（`/v1/chat/completions`、`/v1/responses`、`/v1/messages`、playground）向けの上限。リバースプロキシの `client_max_body_size` をこの値以上に設定してください |
| `TOKENHUB_NGINX_CLIENT_MAX_BODY_SIZE` | `32m` | 同梱のマルチインスタンス nginx ロードバランサーのみが読み取ります。バックエンドのバイト形式ではなく nginx のサイズ構文（`32m`、`512k`）を使用し、`TOKENHUB_MAX_MULTIMODAL_REQUEST_BYTES` 以上に設定してください |
| `TOKENHUB_IN_FLIGHT_LEASE_TTL_SECONDS` | `300` | クラスター全体の同時実行リースの期限と更新間隔の基準 |
| `TOKENHUB_CLUSTER_LOCK_TTL_SECONDS` | `180` | クラスター調整ロックの期限と更新間隔の基準 |
| `TOKENHUB_GRACEFUL_SHUTDOWN_SECONDS` | `150` | 停止時に処理中リクエストを待機する最大秒数 |
| `TOKENHUB_STOP_GRACE_PERIOD` | `180s` | Docker がバックエンドを強制停止するまでの Compose 猶予時間 |
| `TOKENHUB_CACHE_AFFINITY_ENABLED` | `false` | Chat Completions、Anthropic Messages、Responses で同一セッションを同一の上流アカウントに固定し、上流の prompt cache が継続的にヒットするようにします。ルーティング挙動を変えるため既定では無効 |
| `TOKENHUB_CACHE_AFFINITY_MODELS` | 空 | 段階的ロールアウト用のモデル許可リスト（カンマ区切り）。空の場合は全モデルが対象 |
| `TOKENHUB_CACHE_AFFINITY_ALLOW_USER_SCOPE` | `false` | Chat/Responses の `user` と Anthropic の `metadata.user_id` もアフィニティキーとして受け入れるか。同一ユーザーの並行セッションが同じ値を共有し単一アカウントに集中するため既定では無効 |
| `TOKENHUB_GUARDRAIL_MODEL_URL` | 空 | 専用 Qwen3Guard サービスの完全な OpenAI-compatible chat-completions URL。呼び出し前にローカルの `mask` ルールで一致した値を `[REDACTED]` に置き換え、一致しなかった検査対象テキストはそのサービスへ送信する。空の場合はモデルを呼び出さず、各ポリシーの利用不可時設定を適用 |
| `TOKENHUB_GUARDRAIL_MODEL_API_KEY` | 空 | 専用ガードレールモデルサービス用の任意 Bearer 資格情報 |
| `TOKENHUB_GUARDRAIL_MODEL_NAME` | `Qwen/Qwen3Guard-Gen-0.6B` | ガードレールサービスへ送信するモデル識別子 |
| `TOKENHUB_GUARDRAIL_MODEL_TIMEOUT_SECONDS` | `10` | 1 回のガードレールモデル分類の制限時間 |
| `TOKENHUB_IMAGE_STORAGE_DIR` | `data/images` | 生成された画像アセットを保存するディレクトリ |
| `TOKENHUB_IMAGE_WORKER_CONCURRENCY` | `2` | 画像生成キューを処理するワーカー数 |
| `TOKENHUB_IMAGE_QUEUE_CAPACITY` | `64` | キューで待機できる画像ジョブの上限 |
| `TOKENHUB_IMAGE_JOB_TIMEOUT_SECONDS` | `300` | 単一の画像生成ジョブのタイムアウト。超過すると失敗として扱われます |
| `TOKENHUB_IMAGE_CAPABILITY_RETRY_SECONDS` | `86400` | 画像生成非対応と記録されたプロバイダーリソースを再検査するまでの待機時間 |
| `TOKENHUB_RESPONSE_WORKER_CONCURRENCY` | `2` | 永続化されたバックグラウンド Responses ジョブを取得する Worker 数 |
| `TOKENHUB_RESPONSE_POLL_INTERVAL_MILLIS` | `250` | バックグラウンド Responses ジョブとキャンセル状態を確認するデータベースのポーリング間隔 |
| `TOKENHUB_RESPONSE_JOB_TIMEOUT_SECONDS` | `300` | 1 件のバックグラウンド Responses ジョブの実行タイムアウト |
| `TOKENHUB_RESPONSE_LEASE_TTL_SECONDS` | `30` | 複数レプリカ間でバックグラウンド Responses Worker を保護するリース期間 |
| `TOKENHUB_RESPONSE_RESULT_TTL_SECONDS` | `3600` | 完了後に暗号化されたリクエストと結果 payload を保持する期間 |
| `TOKENHUB_RESPONSE_MAX_QUEUED_JOBS` | `1000` | 1 つのデプロイが受け付ける待機中および実行中のバックグラウンド Responses ジョブ上限 |
| `TOKENHUB_API` | 空 | `tokenhub-migrate` CLI が対象とする Admin API の URL。この CLI のみが読み取り、バックエンドサーバーは読み取りません。`--to` で上書きされます |

TokenHub ホストのプロキシが Fake-IP モードで動作する場合は、**システム設定 → 基本設定 → Synthetic DNS / Fake-IP 範囲** で設定します。この例外は既定で無効であり、ホスト名の DNS 解決結果にだけ適用され、リテラル IP の Provider URL には適用されません。すべての実装が `198.18.0.0/15` を使うと仮定せず、プロキシが実際に使用するプールを入力してください。この範囲はベンチマーク用に予約され、Fake-IP でよく使われますが、Fake-IP 専用ではありません。通常モードでは RFC1918 プライベートネットワークと IPv6 ULA は引き続きブロックされます。プロキシが実際にこれらの範囲を使用する場合（例：Xray の IPv6 Fake-IP プール）は、別の高リスクなプライベート範囲信頼を明示的に有効にする必要があります。有効にすると、Provider ホスト名が設定範囲内の実在する内部サービスへ到達できる可能性があります。loopback、link-local、metadata、multicast、NAT64 の各範囲はどのモードでもブロックされます。

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

公開される管理対象イメージとネイティブアーカイブには、対応するバージョンの `data/model-catalog.yaml` と `data/provider-catalog.json` が含まれます。これらは Release の残りのファイルとともに `/opt/tokenhub/current/catalog/` で有効化されるため、バックエンドプログラムと両方のカタログが常に同じバージョンになります。バックエンドの起動時は同梱されたローカル Provider カタログだけを読み込み、ネットワークに依存しません。管理者が Provider カタログを明示的に更新すると、`https://raw.githubusercontent.com/ThinkInAIXYZ/PublicProviderConf/dev/dist/all.json` から完全な `PublicProviderConf` カタログを取得します。レスポンスの取得に失敗した場合や内容が不完全な場合は、設定済みのローカル `provider-catalog.json` へフォールバックします。

カスタムモデルカタログを使用する場合は、マウントするファイルを明示します。

```bash
./deploy/install.sh --model-catalog /absolute/path/to/model-catalog.yaml
```

カスタムファイルはイメージ内の追跡対象モデルカタログを上書きするため、そのバージョンは `TOKENHUB_IMAGE_TAG` とは別に管理します。ファイルを更新した後、バックエンドコンテナを再起動するかシステム設定の同期操作を実行し、モデルカタログエラーなしで完了することを確認します。

設定済みカタログファイルを更新した後は、バックエンドを再起動するか、**システム設定 → 基本設定** で **モデル参照カタログを同期** を実行します。どちらも参照メタデータを同期し、カスタム外部モデルを保持しますが、モデルは公開しません。

`data/model-catalog.yaml` は追跡対象カタログの参照メタデータを提供します。ルートの許可リストではなく、モデルを公開するものでもありません。`data/provider-catalog.json` は Provider テンプレートと、Provider 設定時に選択できる上流モデルを提供します。選択項目の取り込みでは永続化された Provider モデルインベントリだけが作成されます。外部モデルと統一された顧客向け価格は Model Directory で個別に作成し、Routing Policies で取り込み済みの Provider モデルへマッピングします。`GET /v1/models` は有効かつ 1 つ以上の有効なルートを持つ外部モデルだけを返し、API Key のモデル許可リストが設定されている場合はさらに絞り込みます。起動時の読み込みと更新時のフォールバックにカスタム Provider カタログを使うには、同じ `providers` 構造を持つローカル JSON ファイルを `TOKENHUB_PROVIDER_CATALOG_FILE` に指定します。

### Kronk への接続

TokenHub は外部の Kronk Model Server に接続するだけで、Kronk のインストール、GGUF ファイルのダウンロード、llama.cpp の組み込みは行いません。TokenHub コンテナ内の `127.0.0.1` は Docker ホストではなく、そのコンテナ自身を指します。Kronk をホストで実行する場合は、環境で利用可能な `host.docker.internal` などのホスト到達可能なアドレスを使用してください。別コンテナで実行する場合は、共有 Docker ネットワークと Kronk のサービス名を使用します。信頼済みプライベートリテラル IP は `TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS` で許可します。TokenHub と Kronk が同じホストネットワーク名前空間を共有する場合に限り、既定の loopback アドレス用に `TOKENHUB_PROVIDER_UPSTREAM_ALLOW_LOOPBACK=true` を設定してください。

Kronk は既定で平文 HTTP を待ち受けます。リモート配置では、信頼済みプライベートネットワークまたは TLS リバースプロキシを使用し、適切な Kronk authorization mode を有効にしてください。TokenHub は推論、モデル検出、liveness、readiness エンドポイントだけを使用し、モデルダウンロード、ディレクトリ、セキュリティ管理、debug、pprof、管理 UI の各エンドポイントはプロキシしません。

## リバースプロキシ

本番環境では HTTPS の背後に置き、次のように転送してください。

- 管理コンソールのトラフィックはフロントエンドサービスへ。
- `/v1/*` と `/api/admin/*` はバックエンドサービスへ。

長いモデル応答に備えて、リクエストボディサイズとストリーミングタイムアウトを十分に設定してください。

Liveness には `/livez`、Readiness には `/readyz` を使用します。データベースが利用できない場合、またはデータベース進化状態がサービス提供可能でない場合（不完全なマイグレーション、台帳検証の失敗、未完了のブロッキングデータバックフィル）、`/readyz` と後方互換の `/healthz` は `503` を返します。保留中のオンラインデータバックフィルは準備状態に影響しません。
