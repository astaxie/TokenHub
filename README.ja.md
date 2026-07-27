<p align="center">
  <img src="frontend/public/brand/tokenhub-logo.png" alt="TokenHub" width="96" />
</p>

<h1 align="center">TokenHub</h1>

<p align="center">
  TokenHub は、ユーザー、チームリーダー、管理者のためのロール別ワークスペースを備えたエンタープライズ向けプライベート AI ゲートウェイです。
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="License" /></a>
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" alt="Go 1.26" />
  <img src="https://img.shields.io/badge/Next.js-16.2.9-black?logo=nextdotjs" alt="Next.js 16.2.9" />
  <img src="https://img.shields.io/badge/React-19.2.7-61DAFB?logo=react&logoColor=111111" alt="React 19.2.7" />
  <img src="https://img.shields.io/badge/SQLite-first-003B57?logo=sqlite&logoColor=white" alt="SQLite first" />
  <img src="https://img.shields.io/badge/Docker-Compose-2496ED?logo=docker&logoColor=white" alt="Docker Compose" />
  <img src="https://img.shields.io/badge/OpenAI-Compatible-10A37F" alt="OpenAI Compatible" />
  <img src="https://img.shields.io/badge/i18n-ZH%20%7C%20EN%20%7C%20JA-6f42c1" alt="i18n ZH EN JA" />
</p>

<p align="center">
  <a href="README.md">English</a> | <a href="README.zh-CN.md">简体中文</a> | 日本語
</p>

## スクリーンショット

| ログインコンソール | ゲートウェイ概要 |
| --- | --- |
| ![Login Console](docs/assets/screenshots/login-en.png) | ![Gateway Overview](docs/assets/screenshots/overview-en.png) |
| API ドキュメント | Provider チャネル |
| ![API Documentation](docs/assets/screenshots/gateway-en.png) | ![Provider Channels](docs/assets/screenshots/providers-en.png) |
| モデルカタログ | ルーティングポリシー |
| ![Model Catalog](docs/assets/screenshots/models-en.png) | ![Routing Policies](docs/assets/screenshots/routes-en.png) |
| 利用分析 | システム設定 |
| ![Usage Analytics](docs/assets/screenshots/usage-en.png) | ![System Settings](docs/assets/screenshots/settings-en.png) |

## 3つのロールを中心に設計

TokenHub は、日常的なモデル利用、チームガバナンス、プラットフォーム運用を明確に分け、企業ユーザーが自分の責任に合ったワークフローへすぐ入れるようにします。

| ロール | ワークスペースの重点 | ガイド |
| --- | --- | --- |
| ユーザー | 利用可能なモデルの確認、プロジェクト Key の作成、モデル API の呼び出し、個人利用状況の確認 | [ユーザーガイド](docs/ja/user-guide.md) |
| チームリーダー | プロジェクトスペース、プロジェクトメンバー、プロジェクト Key、チームレポート、プロジェクト別コスト配賦の管理 | [チームリーダーガイド](docs/ja/team-leader-guide.md) |
| 管理者 | Provider、モデルカタログ、ルーティングポリシー、ID ソース、RBAC、監査、コスト制御の設定 | [管理者ガイド](docs/ja/administrator-guide.md) |

## プラットフォーム機能

- OpenAI-Compatible モデル API: `/v1/chat/completions`、`/v1/responses`、`/v1/embeddings`。Anthropic Messages API: `/v1/messages`、`/v1/messages/count_tokens`。
- OpenAI-Compatible の画像生成および参照画像編集 API: `/v1/images/generations`、`/v1/images/edits`。非同期ジョブとサーバー側の画像保持に対応し、`codex-gpt-image-2` は Codex サブスクリプション枠、`gpt-image-2` は OpenAI API Provider を使用します。[画像生成ガイド](docs/ja/user-guide.md#codex-サブスクリプション画像生成)を参照してください。
- Provider チャネル: OpenAI-Compatible、Azure OpenAI、Anthropic、Gemini、DeepSeek、Qwen、ローカル vLLM/Ollama、カスタム上流。
- モデルカタログとルーティングポリシー: 優先度、重み、フェイルオーバー順序、ルートヘルス診断に対応。
- プロジェクト単位の Key 管理: チーム所有、メンバー権限、クォータ、並行数制限に対応。
- ユーザー、プロジェクト、チーム、モデル、コストセンターに紐づく利用分析とリクエストログ。
- OAuth/OIDC によるエンタープライズサインイン、RBAC、監査証跡に対応する ID ソース設定。
- クリーンなコンソール: ロール別ナビゲーション、グローバル検索、ライト/ダーク切り替え、左ナビ + 右詳細の API ドキュメント。
- SQLite-first のプライベートデプロイと Docker Compose サポート。
- PostgreSQL はマルチインスタンス構成に対応します。リモート PostgreSQL で状態を共有し、フロントエンドとバックエンドのレプリカを水平スケールできるほか、コネクションプールも設定できます。[デプロイガイド](docs/ja/deployment.md)を参照してください。
- 管理コンソールは英語、中国語、日本語の切り替えに対応。
- TokenHub は OpenAI Codex のサブスクリプションアカウントリソースにも接続できます。分離および復旧が可能な Codex Profile を使用し、指定したローカル Codex CLI またはデスクトップセッションを TokenHub 経由で実行できます。[Codex 接続ガイド](docs/ja/codex-tokenhub-profile-quick-start.md)を参照してください。

## クイックスタート

```bash
cp deploy/.env.example deploy/.env
# deploy/.env のすべての change-me 値を強いシークレットに置き換えます。
./deploy/install.sh
```

アクセス先:

- 管理コンソール: `http://localhost:3000`
- バックエンド API: `http://localhost:8080`
- ヘルスチェック: `http://localhost:8080/healthz`

初期管理者ログイン:

- ユーザー名: `admin`
- パスワード: `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD` の設定値

デプロイスクリプトは本番用認証情報を検証し、公開済みイメージを取得して、ローカルではビルドせずにコンテナを起動します。イメージがまだ公開されておらず、デフォルトの `latest` タグの取得に失敗した場合は、ローカルのソースビルドへ自動的に切り替えます。明示したタグでは切り替えません。秘密値を表示せずに安全でない変数を個別に報告します。その試行で作成または再起動したバックエンドコンテナの異常により Compose が失敗した場合に限り、その試行で生成された直近のバックエンドログを自動表示します。現在のチェックアウトから明示的にビルドする場合は `./deploy/install.sh --build` を使用します。

## ドキュメント

- [ドキュメントホーム](docs/ja/README.md)
- [全体アーキテクチャ](docs/ja/architecture.md)
- [ユーザーガイド](docs/ja/user-guide.md)
- [チームリーダーガイド](docs/ja/team-leader-guide.md)
- [管理者ガイド](docs/ja/administrator-guide.md)
- [コントリビューションガイド](CONTRIBUTING.ja.md)
- [English documentation](docs/README.md)
- [简体中文文档](docs/zh-CN/README.md)

## Contributors

TokenHub は、実際のエンタープライズ利用からのフィードバック、ゲートウェイ連携、ドキュメント、テスト、継続的なメンテナンスによって育っています。プロジェクトをより信頼できるものにしてくれるすべての方に感謝します。

<p align="center">
  <a href="https://github.com/astaxie/TokenHub/graphs/contributors">
    <img src="https://contrib.rocks/image?repo=astaxie/TokenHub" alt="TokenHub contributors" />
  </a>
</p>

<p align="center">
  <a href="https://github.com/astaxie/TokenHub/graphs/contributors">すべてのコントリビューターを見る</a>
  ·
  <a href="CONTRIBUTING.ja.md">コントリビュートを始める</a>
</p>

## Star History

[![GitHub Repo stars](https://img.shields.io/github/stars/astaxie/TokenHub?style=social)](https://github.com/astaxie/TokenHub/stargazers)

TokenHub の成長は [Star History のライブチャート](https://star-history.com/#astaxie/TokenHub&Date)で確認できます。

## License

TokenHub は [Apache License 2.0](LICENSE) の下で提供されています。
