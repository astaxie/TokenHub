# TokenHub 全体アーキテクチャ

Language: [English](../architecture.md) | [简体中文](../zh-CN/architecture.md) | 日本語

このドキュメントは、開発、運用、セキュリティ担当者向けに、リポジトリで実装されている TokenHub のデプロイ形態、リクエスト経路、データ境界を説明します。TokenHub は SQLite による単一インスタンスを既定とし、PostgreSQL による単一インスタンスおよびリモート PostgreSQL を使うマルチインスタンスもサポートします。

## 概要

Go バックエンドの 1 プロセスで管理 API、OpenAI 互換モデル API、ルーティング、Provider アダプター、監査、永続化を提供します。Next.js は管理コンソールです。コントロールプレーンとデータプレーンは論理的な境界であり、既定構成では 1 つのバックエンドとデータベースを共有します。マルチインスタンス構成では PostgreSQL を介して状態を共有します。

```mermaid
flowchart TB
    admin["管理者 / チームリーダー"]
    app["業務アプリケーション / SDK"]
    ingress["直接ポートまたは HTTPS リバースプロキシ"]
    frontend["Next.js 管理コンソール"]
    backend["TokenHub Go バックエンド"]

    subgraph backendProcess["バックエンドプロセス"]
        adminApi["管理 API\n/api/admin/*"]
        modelApi["モデル API\n/v1/*"]
        governance["認証とガバナンス\nKey、RBAC、クォータ、並行数、IP 許可リスト"]
        routing["ルーティング\n候補、戦略、重み、フェイルオーバー、アフィニティ"]
        adapters["アダプターレジストリ\n汎用 Provider / OpenAI Codex"]
        operations["運用と可観測性\n利用量、監査、アラート、ヘルスチェック"]
        store["GORM Store"]

        adminApi --> governance
        adminApi --> store
        modelApi --> governance --> routing --> adapters
        modelApi --> operations --> store
        adminApi --> operations
        routing --> store
    end

    subgraph persistence["永続化と設定"]
        sqlite[("SQLite\n既定の単一インスタンス")]
        postgres[("PostgreSQL\n本番およびマルチインスタンス")]
        catalog["イメージ内モデルカタログ\nまたはカスタム読み取り専用上書き"]
    end

    subgraph upstream["上流モデルサービス"]
        compatible["OpenAI と互換サービス\nDeepSeek / Qwen / vLLM / Ollama など"]
        azure["Azure OpenAI"]
        anthropic["Anthropic"]
        gemini["Gemini"]
        codex["OpenAI Codex Subscription"]
    end

    admin --> ingress --> frontend
    frontend -->|"TOKENHUB_API_BASE_URL"| backend
    app --> ingress -->|"/v1/*"| backend
    adapters --> compatible
    adapters --> azure
    adapters --> anthropic
    adapters --> gemini
    adapters --> codex
    store --> sqlite
    store --> postgres
    catalog --> store
```

## プレーン

| プレーン | エントリーポイントと利用者 | 主な責務 | 現在の実装 |
| --- | --- | --- | --- |
| コントロールプレーン | 管理コンソールと `/api/admin/*` | Provider、リソース、モデル、ルート、Project、ユーザー、Key、クォータ、アラート、承認、バックアップの管理 | Next.js コンソールと Go 管理 API。状態は SQLite または PostgreSQL に保存 |
| データプレーン | 業務アプリケーションと `/v1/*` | Project API Key の検証、ルート選択、上流モデル呼び出し、互換レスポンスの返却 | Go `net/http`。Chat Completions、Responses、ストリーミング Responses、`/v1/responses/compact`、Embeddings |
| 運用プレーン | プローブ、管理 API、デプロイツール | リクエスト監査、利用量、ルート試行、Provider プローブ、バックアップ、クラスタ協調 | バックエンドプロセス内で実行。マルチインスタンスでは PostgreSQL に協調状態を保存 |

## デプロイ形態

| 形態 | Compose ファイル | サービスと入口 | データベースと用途 |
| --- | --- | --- | --- |
| 既定の単一インスタンス | `deploy/docker-compose.yml` | フロントエンド 1 台、バックエンド 1 台、`3000` と `8080` を直接公開 | SQLite。開発、テスト、単一ホストのプライベートデプロイ向け |
| PostgreSQL 単一インスタンス | `deploy/docker-compose.postgres.yml` | フロントエンド、バックエンド、ローカル PostgreSQL を各 1 台 | より高い並行性またはデータベース統制が必要な本番向け |
| リモート PostgreSQL マルチインスタンス | `deploy/docker-compose.remote-postgres.yml` | Nginx とスケール可能なフロントエンド/バックエンド | 高可用性と水平スケールのためのマネージド PostgreSQL |

```mermaid
flowchart LR
    users["ブラウザと業務アプリケーション"] --> nginx["Nginx ロードバランサー\nリモート PostgreSQL マルチインスタンス"]
    nginx --> frontends["Next.js レプリカ x N"]
    nginx --> backends["Go バックエンドレプリカ x N"]
    frontends --> backends
    backends --> database[("リモート PostgreSQL")]
    catalog["イメージ内カタログ\nまたはカスタム読み取り専用マウント"] --> backends
    backends --> providers["外部 Provider API"]
```

既定 Compose にはリバースプロキシは含まれず、フロントエンドとバックエンドのポートを直接公開します。本番では外部で HTTPS を終端できます。リモート PostgreSQL 用 Compose には Nginx が含まれ、`/api/*`、`/v1/*`、`/livez`、`/readyz`、`/healthz` をバックエンドレプリカへ転送します。

既定イメージはビルド時に内蔵したモデルカタログを使用し、実行ファイルとカタログのバージョンを一致させます。カスタムカタログは `./deploy/install.sh --model-catalog /absolute/path/to/model-catalog.yaml` による明示的な上書きであり、既定の Compose マウントではありません。

## コンポーネントと Provider

| コンポーネント | 場所 | 責務 |
| --- | --- | --- |
| 管理コンソール | `frontend/` | ロール対応コンソール。バックエンド URL は実行時に `TOKENHUB_API_BASE_URL` から読み取り、`NEXT_PUBLIC_API_BASE_URL` は互換フォールバックのみ |
| HTTP サーバー | `backend/internal/server/http.go` | API、認証、ルーティング呼び出し、レスポンス、ヘルスエンドポイント |
| ルーティング | `backend/internal/server/http.go` | 優先度、リソース優先度、戦略、重み、アフィニティによる候補の順序付け |
| アダプターレジストリと統合サービス | `adapter_registry.go`、`integration_service.go` | Provider 能力の宣言と Provider/リソースのプローブ |
| Provider アダプター | `providers.go`、`provider_account_codex.go` | プロトコル変換、Codex Subscription の OAuth、更新、セッションアフィニティ |
| Store | `store.go` | GORM、クォータ、認証情報暗号化、SQLite バックアップ、PostgreSQL リース、クラスタロック |

| Provider 型 | アダプターと能力 |
| --- | --- |
| `openai`、`openai_compatible`、`qwen`、`local` | OpenAI 互換：Chat、ストリーミング Chat、Responses、Embeddings、プローブ |
| `deepseek` | OpenAI 互換：Chat、ストリーミング Chat、Embeddings、プローブ。Responses とストリーミング Responses はモデル単位で宣言し、`deepseek-v4-flash` と `deepseek-v4-pro` で有効 |
| `azure_openai` | Chat、ストリーミング Chat、Embeddings、プローブ |
| `anthropic` | Chat、ストリーミング Chat、プローブ |
| `gemini` | Chat、ストリーミング Chat、Embeddings、プローブ |
| `openai_codex` | OpenAI Codex Subscription：Responses、ストリーミング Responses、モデル、クォータ、OAuth、セッションアフィニティ、Compact |
| `mock` | ローカル検証とテスト用の内蔵アダプター |

## モデルリクエスト経路

`Model` は外部 API 契約、`ProviderModel` は 1 つの Provider に対する永続化された上流モデルインベントリ、`ModelRoute` はその間のマッピングです。外部モデルには明示的で永続化されたディレクトリロールが付くため、最後のルートを削除しても候補テンプレートへ戻らず、下書きとして残ります。ルートの作成または編集では、選択した `ProviderModel` がインベントリに存在する必要があります。限定的な例外は、サブスクリプション型仮想モデル `codex-gpt-image-2` です。このルートは OpenAI Codex Provider を対象とし、上流モデルを `gpt-image-2` に固定する必要があります。これはチャットモデルのインベントリ項目ではなく、実行能力です。これにより、同名 1:1 マッピングとカスタムエイリアスの両方を支持し、呼び出し側は Provider 固有のモデル名を意識しません。`POST /v1/chat/completions`、`POST /v1/responses`、`POST /v1/responses/compact`、`POST /v1/embeddings` は同じ認証、クォータ、ルーティング入口を共有します。

```mermaid
sequenceDiagram
    participant C as 業務アプリケーション
    participant G as TokenHub /v1
    participant S as Store とデータベース
    participant A as Provider Adapter
    participant U as 上流モデルサービス

    C->>G: Bearer Project API Key とモデルリクエスト
    G->>S: Key、Project、有効期限、IP 許可リストを検証
    G->>G: Project と API Key のモデルアクセスを積集合化
    G->>S: 適用されるコンテンツセキュリティポリシーを一貫したスナップショットで取得
    G->>G: ユーザー表示リクエストテキストを検査、監査、マスク、またはブロック
    G->>S: クォータと並行リースを確認し、呼び出しコンテキストを作成
    G->>S: 有効かつ健全な Provider / Resource / Route を取得
    G->>G: API Key、Project、Global ポリシーを解決し候補を絞り込む
    G->>G: 戦略、重み、セッションアフィニティで試行順序を計画
    loop フェイルオーバー可能な候補ルート
        G->>A: 正規化済みリクエストとルート選択
        A->>U: Provider プロトコルリクエスト
        U-->>A: レスポンスまたはエラー
        A-->>G: 正規化済みレスポンス、利用量、ヘッダー、またはエラー
    end
    G->>S: 試行、ログ、利用量、リソース状態を保存
    G-->>C: 互換レスポンスと x-request-id
```

非アクティブまたは不健全な Provider、Resource、Route は除外されます。ただし例外として、クールダウンが満了した Resource はハーフオープン候補として再び候補に加わります。最初に到達したリクエストがクールダウン期限を前方へ進めることで試行権を取得するため、同時実行のリクエストは引き続き拒否され、試行が失敗した場合は次のより長いウィンドウがすでに設定されています。その試行自身が成功した場合にのみブレーカーが閉じ、管理者の操作なしに Resource が復旧します。ブレーカー作動時にすでに実行中だったリクエストが Resource を復活させることはありません。失敗が繰り返される場合は `TOKENHUB_RESOURCE_COOLDOWN_MAX_SECONDS` を上限として指数的にウィンドウが延長されます。管理者が無効化した Resource が再び組み入れられることはありません。非ストリーミング呼び出しは候補を順番に試行します。出力開始後のストリームは安全に上流を切り替えられません。ストリーミング Responses には `response_stream` 能力を持つアダプターが必要です。`openai_codex` のルートでは、リクエストと API Key からセッションアフィニティキーを導出し、継続性のために Resource バインドを永続化できます。

`background: true` を設定した `POST /v1/responses` では、同期リクエストフローは認証と永続化された投入の後で終了します。各レプリカは起動時にキューが空でも永続キューを継続して poll します。Worker はジョブを取得して元の認可を再検証し、admitted phase、request ID、quota counter、token reservation、concurrency lease を同じデータベース transaction で commit してから、同じ guardrail、routing、Provider、metering、audit、trace のフローへ入ります。lease epoch が古い Worker を fence します。PostgreSQL の複数レプリカは row lock と `SKIP LOCKED` で取得し、SQLite は対応対象の単一バックエンド構成で原子的に取得します。Admission 前の lease 喪失は安全に再実行できます。Admission 後の lease 喪失は Provider への重複リクエストを避けるため明示的な終端状態となり、未 dispatch の token reservation は復旧時に返却されます。

Project と API Key のモデルアクセスはルート選択前の明示的な最小権限レイヤーです。制限リストは積集合化され、制限かつ空リストはすべてを拒否し、レガシーの空モードは継承のままです。スコープルーティングポリシーは、`routing-policies` kind の監査可能な `AdminResource` として保存されます。実行時は厳密な API Key → Project → Global の優先順位で最大 1 つのバインドを選び、その Provider、Resource、Model、タグ、リージョン、環境の制約をルートの Project スコープと積集合化します。無効、競合、または候補が空の上位バインドはフェイルクローズします。戦略の上書き、アフィニティ、ハーフオープン復旧、フェイルオーバーは絞り込み後の候補内でのみ動作します。有効ポリシー ID、スコープ、優先度はリクエスト監査レコードにコピーされます。

## セキュリティ、ヘルス、データ境界

- Project API Key はハッシュ、状態、Project 状態、有効期限、モデル範囲、IP 許可リスト、クォータ、並行数で検証されます。
- 管理 API はログインセッション Token または `TOKENHUB_ADMIN_TOKEN` を使用します。初期 `admin` アカウントは `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD` から作成されます。
- 開発以外の起動時は、プレースホルダー値、32 バイト未満の Admin Token/バックエンドシークレット、12 バイト未満の初期管理者パスワードを拒否します。
- `TOKENHUB_TRUSTED_PROXY_CIDRS` は `X-Forwarded-For`、`X-Forwarded-Host`、`X-Forwarded-Proto` を提供できるプロキシを限定し、信頼済みプロキシはこれらのヘッダーを上書きする必要があります。`TOKENHUB_CORS_ALLOWED_ORIGINS` は資格情報を伴うブラウザ Origin を制御します。
- `/livez` はプロセス生存確認用です。`/readyz` と互換用の `/healthz` はデータベース可用性とデータベース進化状態を確認し、データベースが利用できない場合、マイグレーションが不完全な場合、台帳検証が失敗した場合、ブロッキングデータバックフィルが未完了の場合に `503` を返します。保留中のオンラインバックフィルは準備状態を維持します。

Provider 認証情報、請求コネクター認証情報、生の請求スナップショット、永続化されたバックグラウンド Responses payload は `TOKENHUB_SECRET_KEY` から導出した AES-GCM で暗号化されます。Project API Key は SHA-256 ダイジェストと表示用のプレフィックス/サフィックスだけを保持します。すべてのレプリカは同じ安定したシークレットを使用する必要があります。

| カテゴリー | 主なエンティティ | 用途 |
| --- | --- | --- |
| テナントと認証情報 | `Project`、`APIKey`、`AdminUser`、`AdminSession` | Project 所有、アプリケーションアクセス、管理セッション |
| ルーティング | `Provider`、`ProviderResource`、`ProviderModel`、`Model`、`ModelRoute`、`AdminResource (routing-policies)` | 上流チャネル、リソースプール、上流インベントリ、外部モデル、ルート、スコープポリシーバインド |
| コンテンツセキュリティ | `guardrails.Policy`、`guardrails.DetectionItem`、`guardrails.Binding` | Project 単位のリクエスト検査、検出器設定、アクション、ポリシーバインド |
| ガバナンスと計量 | `QuotaBucket`、`UsageRecord`、`ProviderResourceBucket`、`InFlightLease` | クォータ、利用量/コスト、レプリカ間並行数 |
| 外部請求 | `BillingConnector`、`BillingRecord`、`BillingRawSnapshot`、`BillingSyncRun` | Provider 請求の収集、正規化、チェックポイント、同期履歴 |
| マルチインスタンス協調 | `ClusterLease`、`ClusterTaskState`、`AdapterSessionBinding` | カタログ同期、クラスタ操作、Codex セッションの Resource バインド |
| バックグラウンド Responses | `ResponseJob`、`ResponseJobEvent` | 暗号化されたリクエストと結果の保持、fencing された実行状態、キャンセル、期限切れ、状態遷移監査 |
| 可観測性 | `RequestLog`、`RequestPayloadLog`、`RouteAttemptLog`、`ProviderObservation`、`AuditEvent` | リクエスト追跡、ペイロード監査、ルート試行、Provider 観測、管理監査 |

SQLite は単一接続と 5 秒の `busy_timeout` を使用し、バックエンドレプリカ間で共有してはなりません。PostgreSQL はコネクションプール、マイグレーション用 advisory lock、実行中リース、クラスタロックを提供します。内蔵バックアップ API は SQLite 専用です。PostgreSQL では `pg_dump` と `pg_restore` などのプラットフォーム機能を使用してください。

デプロイは Redis、メッセージブローカー、サービスメッシュに依存しません。同期リクエストとレスポンスのペイロードは監査用に記録される場合があるため、本番では保持期間、最小権限、ディスク暗号化、バックアップアクセス制御を適用してください。永続化されたバックグラウンド Responses は平文の payload 監査と trace export から除外され、その内容は TTL 付きの暗号化ジョブレコードだけに保持されます。

## 関連ドキュメント

- [デプロイ](deployment.md)：デプロイ形態、環境変数、リバースプロキシ、ヘルスチェック。
- [PostgreSQL 設定ガイド](../postgresql-setup.md)：PostgreSQL の設定、運用、移行。
- [管理者ガイド](administrator-guide.md)：Provider、ルート、アクセス制御、監査、コスト統制。
- [利用者ガイド](user-guide.md)：Project API Key とモデル API 呼び出し。
- [チームリーダーガイド](team-leader-guide.md)：チーム、Project、メンバー、コスト配賦。
