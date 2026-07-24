# 管理者ガイド

Language: [English](../administrator-guide.md) | [简体中文](../zh-CN/administrator-guide.md) | 日本語

このガイドは、TokenHub を企業 AI ゲートウェイとして運用するプラットフォーム管理者、セキュリティ運用者、インフラ担当者向けです。

## 管理者の範囲

| 領域 | 責任 |
| --- | --- |
| Provider Channels | 上流 Base URL、認証情報、リソース、ヘルスチェックを設定します |
| Model Catalog | 標準モデル名、能力、コンテキスト長、価格単位を管理します |
| Routing Policies | 優先度、重み、フェイルオーバー戦略で標準モデルを Provider モデルに割り当てます |
| Projects and Teams | Key、クォータ、コスト配賦の組織境界を定義します |
| Identity Sources | OAuth または OIDC の企業ログインを設定します |
| Security and Audit | リクエストログ、管理操作、Key ローテーション、ポリシー変更を確認します |

## 本番設定順序

1. 少なくとも 1 つの ID プロバイダーを設定し、管理者アカウントを保持します。
2. `OpenAI Production`、`Azure East US`、`Internal Model Gateway` などの上流 Provider を追加します。
3. `gpt-4.1-mini` などの英語モデル名でモデルカタログを管理します。
4. 呼び出し可能にする各モデルに、有効なルーティングルールを作成します。
5. Team、Project、Cost Center、既定クォータポリシーを作成します。
6. Model Playground と Request Logs でフローを検証します。
7. Key を広く発行する前に利用量配賦を確認します。

## ルーティング要件

利用者には呼び出し可能なモデルだけを表示します。モデルはカタログで有効で、少なくとも 1 つの有効ルートがある場合に呼び出し可能です。

| 状態 | 管理 UI の挙動 |
| --- | --- |
| 有効モデルかつ有効ルートあり | 通常のモデルカード |
| 有効モデルだがルートなし | 設定不足を見つけやすい背景色で表示 |
| 無効モデル | 利用者には非表示 |
| Provider ルートが不健全 | ルーティング診断と Request Logs に表示 |

## Prompt Cache の料金

モデルカタログでは、100 万 Token あたりのキャッシュ読み取り単価を任意で設定できます。設定した場合、キャッシュにヒットした入力 Token の推定コストにはその単価を使用します。空欄の場合、DeepSeek V4 Pro は標準入力単価の約 0.83%、その他の DeepSeek モデルは 2%、残りの Embedding 以外のモデルは 10% で推定します。モデル料金表では推定値を示し、ホバー時に適用した比率を説明します。

## セキュリティチェックリスト

| コントロール | 要件 |
| --- | --- |
| API keys | 完全な Secret は一度だけ表示し、その後は prefix と suffix のみ保存 |
| OAuth redirect URI | ローカルと本番の callback URL を ID プロバイダーに登録 |
| RBAC | user、team leader、administrator、finance、security、operator の範囲を分離 |
| Audit retention | リクエストログと管理イベントをコンプライアンス確認に十分な期間保持 |
| Cost controls | 可能な限り各リクエストを user、project、team、cost center に配賦 |

## スクリーンショット

![Routing policies](../assets/screenshots/routes-en.png)
