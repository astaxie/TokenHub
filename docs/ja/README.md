# TokenHub ドキュメント

Language: [English](../README.md) | [简体中文](../zh-CN/README.md) | 日本語

TokenHub のドキュメントは、企業 AI ゲートウェイで使う 3 つのロールを中心に再編成されています。既定言語は英語です。中国語版と日本語版も、同じ英語スクリーンショットと英語サンプルデータを利用します。

## アーキテクチャとデプロイ

- [全体アーキテクチャ](architecture.md)：デプロイ形態、コントロール/データプレーン、モデルリクエスト経路、永続化、セキュリティ境界。
- [デプロイ](deployment.md)：Docker Compose、環境変数、データベース、リバースプロキシ、ヘルスチェック。
- [PostgreSQL 設定ガイド](../postgresql-setup.md)：PostgreSQL の設定、運用、移行。
- [パフォーマンスベンチマーク](performance-benchmarking.md)：再現可能なゲートウェイ比較、プロセス内割り当てベンチマーク、回帰バジェット。

## ロールガイド

| ガイド | 対象者 | 主なワークフロー |
| --- | --- | --- |
| [利用者ガイド](user-guide.md) | 社員、アプリケーション開発者 | 利用可能モデルの確認、Project Key の作成、モデル API 呼び出し、個人利用量の確認 |
| [チームリーダーガイド](team-leader-guide.md) | チーム Owner、プロジェクト保守者 | Project、メンバー、API Key、チームレポート、Project コスト配賦の管理 |
| [管理者ガイド](administrator-guide.md) | プラットフォーム管理者、セキュリティ運用者 | Provider、モデルカタログ、ルーティング、ID プロバイダー、RBAC、監査、コスト統制の設定 |
| [Agent Token コスト API](agent-token-cost-api.md) | ローカルレポート Agent、プラットフォーム管理者 | 最小権限の分析 Credential を作成し、フィルター・集計・差分 Token コストを JSON/CSV で取得 |
| [画像生成ガイド](user-guide.md#codex-サブスクリプション画像生成) | 画像モデルを利用するアプリケーション開発者 | 画像の生成と編集、非同期ジョブ、Codex サブスクリプション枠と OpenAI API 利用量の区別 |
| [Codex を TokenHub に接続：Profile クイック設定](codex-tokenhub-profile-quick-start.md) | 分離 Profile のみを必要とする Codex ユーザー | Profile の作成、Key の設定、接続確認、復旧 |
| [Codex を TokenHub に接続：4 つの設定方法と復旧](codex-tokenhub-configuration.md) | 接続方法を比較する Codex ユーザーおよび開発者 | Profile、プロセス単位の一時設定、CLI グローバル設定、デスクトップ設定 |

## 共通の英語サンプルデータ

| オブジェクト | サンプル |
| --- | --- |
| Organization | Acme AI Platform |
| Team | Platform Engineering |
| Project | Payments Assistant |
| Cost center | AI Platform Cost Center |
| Model | gpt-4.1-mini |
| API key placeholder | YOUR_TOKENHUB_API_KEY |

## スクリーンショット

製品画面は英語 UI から取得しています。Codex ガイドでは、機密情報をマスキングした同一の実際のターミナル画面を全言語版で共用します。

| 画面 | ファイル |
| --- | --- |
| Gateway documentation | `../assets/screenshots/gateway-en.png` |
| Overview | `../assets/screenshots/overview-en.png` |
| Models | `../assets/screenshots/models-en.png` |
| Routes | `../assets/screenshots/routes-en.png` |
| Usage | `../assets/screenshots/usage-en.png` |
| Settings | `../assets/screenshots/settings-en.png` |
| Codex Profile 設定 | `../assets/codex-profile/tokenhub-profile-config-redacted.png` |
| Codex Profile ステータス | `../assets/codex-profile/codex-status-redacted.png` |
