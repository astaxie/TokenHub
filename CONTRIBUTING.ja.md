# TokenHub へのコントリビューション

[English](CONTRIBUTING.md) | [简体中文](CONTRIBUTING.zh-CN.md) | 日本語

TokenHub は、Go バックエンド、Next.js 管理コンソール、Node.js SDK スモークテスト、YAML モデルカタログ、Docker Compose デプロイファイルで構成されています。このガイドでは、ローカル開発、検証、オプションの AI Agent ワークフロー、Pull Request の準備について説明します。

## リポジトリ構成

| パス | 用途 |
| --- | --- |
| `backend/` | Go HTTP API、永続化、ルーティング、認証、管理機能、バックエンドテスト |
| `frontend/` | Next.js と React の管理コンソール |
| `sdk/` | OpenAI-Compatible API とセキュリティポリシーエンドポイントの Node.js スモークテスト |
| `data/model-catalog.yaml` | バージョン管理対象のモデルカタログソース |
| `deploy/` | Docker Compose デプロイファイルと環境変数テンプレート |
| `docs/` | 英語、簡体字中国語、日本語のドキュメント |

## ローカル開発

ローカル開発には Go 1.26 と Node.js 20 以降が必要です。Docker と Docker Compose は、コンテナおよびデプロイの検証でのみ必要です。

リポジトリのルートからローカル開発スタック全体を起動します。

```bash
./start.sh
```

コンポーネントを個別に実行する場合は、`backend/` でバックエンドを起動します。

```bash
TOKENHUB_CORS_ALLOWED_ORIGINS=http://localhost:3000 go run ./cmd/tokenhub
```

`frontend/` で管理コンソールを起動します。

```bash
npm ci
npm run dev
```

互換性のあるバックエンドが利用でき、必要な環境変数が設定されている場合にのみ、`sdk/` で SDK スモークテストを実行します。

```bash
npm ci
npm run test:deepseek
npm run test:anthropic-messages
npm run test:security-policy
```

## 変更ガイドライン

- 変更範囲を限定し、無関係な既存の変更を保持します。
- バックエンドの動作を変更する場合は、テストを追加または更新します。外部ネットワークへの依存を避け、プロセス内の HTTP または SMTP フェイクサーバーを優先します。
- 明示的に契約を変更する場合を除き、OpenAI-Compatible `/v1` エンドポイントとの互換性を維持します。
- 認証情報、ローカルの `.env` ファイル、データベース、生成されたバックアップ、実行時ログをコミットしないでください。
- 環境変数を変更する場合は、関連するサンプル、Compose ファイル、`start.sh`、デプロイドキュメントを同期して更新します。
- ユーザー向けの共通ドキュメントは、英語、簡体字中国語、日本語で同期して更新します。
- `data/model-catalog.yaml` をバージョン管理対象のまま維持し、その他の実行時データファイルをコミットしないでください。

## 検証

変更中は対象範囲が最も狭いテストを実行し、Pull Request を作成する前に該当する一連のチェックを実行します。

`backend/` でバックエンドを検証します。

```bash
gofmt -w path/to/changed.go
go test ./...
go vet ./...
```

`frontend/` でフロントエンドを検証します。

```bash
npm ci
npm run lint
npm run typecheck
npm test
npm run build
npx playwright install chromium
npm run test:e2e
```

ブラウザスモークテストは、分離された Next.js フロントエンド、Go バックエンド、模擬 Provider アップストリーム、一時 SQLite データベースを起動します。実際の認証情報や、起動済みの TokenHub 環境は不要です。

Docker またはデプロイ構成を変更した場合は、Docker を利用できる環境で Compose 構成をレンダリングします。

```bash
docker compose --env-file deploy/.env.example \
  -f deploy/docker-compose.yml config
```

すべての変更で `git diff --check` を実行します。実行できなかったチェックを明記し、新しい失敗とベースブランチにすでに存在する失敗を区別してください。

## オプションの AI Agent 開発ワークフロー

TokenHub には、AI Agent がリポジトリを変更する際に選択できる 2 つのワークフローがあります。

| ワークフロー | 適用範囲 |
| --- | --- |
| [`fast-dev`](docs/development/workflows/fast-dev.md) | 公開 API、永続化、認証または認可、デプロイ、コンポーネント横断の動作を変更しない、範囲が明確でリスクの低い変更 |
| [`feature-dev`](docs/development/workflows/feature-dev.md) | 重要な機能、ユーザーに見える変更、コンポーネント横断の変更、公開 API やデータモデルの変更、セキュリティに関わる作業、デプロイ変更、大規模なリファクタリング、アーキテクチャ上の判断 |

依頼でワークフロー名を明示すると有効になります。例: `この変更では fast-dev を使用してください。` 指定がなければ、Agent は通常のリポジトリガイドに従います。`fast-dev` が適さなくなった場合、Agent は `feature-dev` に切り替える前に確認を求めます。ワークフローの選択は、コミット、プッシュ、Pull Request の作成、マージ、その他の外部書き込み操作を許可するものではありません。

Agent 固有のリポジトリ指示については、[AGENTS.md](AGENTS.md#optional-development-workflows)を参照してください。

## Pull Request

- タイトルと本文のすべてのセクションを英語で記述します。
- 英語の Conventional Commits 形式のタイトル `<type>[optional scope][!]: <short summary>` を使用します。
- タイトルは 72 文字以内とし、要約は小文字の命令形で記述し、末尾にピリオドを付けません。
- [Pull Request テンプレート](.github/pull_request_template.md)のすべてのセクションを記入し、スキップしたチェックや該当しないチェックについて説明します。
- 必要に応じて、API 互換性、セキュリティ、データベース、環境変数、デプロイ、ロールアウト、ロールバックへの影響を記載します。
- デフォルトではレビュー可能な Pull Request を作成します。明示的に要求された場合にのみ Draft を使用します。
