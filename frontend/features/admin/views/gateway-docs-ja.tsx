import { gatewayChineseDocs } from "./gateway-docs-zh";
import { type GatewayDocBundle } from "./gateway-view";

export function gatewayJapaneseDocs(stats: GatewayDocBundle): GatewayDocBundle {
  return {
    ...gatewayChineseDocs(stats),
    nav: {
      title: "ドキュメント",
      subtitle: "ロールとタスク別に確認",
      searchPlaceholder: "ガイド、API、エラーコードを検索",
      noResults: "一致するドキュメントはありません",
    },
    title: "3 つのロール別ゲートウェイガイド",
    description: "TokenHub のドキュメントは、利用者、チームリーダー、管理者の 3 つの企業ロールを中心に整理しています。",
    languageLabel: "ドキュメント言語",
    quickInfoLabel: "API 基本情報",
    quickCards: {
      ...stats.quickCards,
      sampleModel: "サンプルモデル",
      currentConfig: "現在の設定",
      activeRoutes: stats.quickCards.activeRoutes.replace("active routes", "件の有効ルート").replace("active route", "件の有効ルート"),
      apiKeys: stats.quickCards.apiKeys.replace("API Keys", "件の API Key").replace("API Key", "件の API Key"),
    },
    groups: [
      {
        title: "はじめに",
        items: [
          {
            ...stats.groups[0].items[0],
            group: "はじめに",
            title: "プラットフォーム概要",
            description: "TokenHub がモデル、プロジェクト、Key、ルーティング、監査、コスト配賦を 1 つの統制フローにまとめる仕組みを説明します。",
            notesTitle: "開始手順",
            notes: [
              "利用者は利用可能モデル、Key 管理、個人利用量から始めます。Provider 認証情報を理解する必要はありません。",
              "チームリーダーはプロジェクトスペースを中心に、詳細サイドパネルでメンバー、Key、コスト帰属を管理します。",
              "管理者は Provider、モデルカタログ、ルーティング、ID プロバイダー、監査とコスト統制を設定します。",
            ],
          },
          {
            ...stats.groups[0].items[1],
            group: "はじめに",
            title: "主要概念",
            description: "企業 AI ゲートウェイのリソース境界と権限境界を共通語彙で整理します。",
            table: {
              title: "概念一覧",
              columns: ["概念", "意味"],
              rows: [
                ["Project", "社内アプリケーションまたは業務スペース。Key、クォータ、メンバー、コスト配賦の基本単位です。"],
                ["API Key", "Project に紐づく呼び出し認証情報。業務アプリが /v1/* を呼び出すために使います。"],
                ["Model Catalog", "ユーザーに表示する標準モデル一覧。有効なルートがあるモデルだけ呼び出せます。"],
                ["Routing Rule", "標準モデルを上流 Provider モデルへ割り当て、優先度、重み、戦略を定義します。"],
                ["Provider", "上流モデルサービスまたは社内モデルリソース。Base URL、認証情報、ヘルス状態を持ちます。"],
                ["Usage Attribution", "リクエスト、Token、コストを個人、Project、Team、Cost Center に配賦します。"],
              ],
            },
          },
        ],
      },
      {
        title: "ロールガイド",
        items: [
          {
            ...stats.groups[1].items[0],
            group: "ロールガイド",
            title: "利用者ガイド",
            description: "利用者は利用可能モデル、プロジェクト Key、呼び出し例、個人利用量、リクエストログを確認します。",
            notesTitle: "日常フロー",
            notes: [
              "Available Models または Model Playground で呼び出せるモデルを確認します。",
              "Key Management で割り当て済み Project を選び、業務 API Key を作成またはコピーします。",
              "業務アプリは /v1/chat/completions、/v1/responses、/v1/embeddings などのモデル API だけを呼び出します。",
              "401/403/429 が出た場合は request_id を Request Logs で検索し、必要に応じてチームリーダーに権限調整を依頼します。",
            ],
            table: {
              title: "利用者の操作",
              columns: ["タスク", "画面", "説明"],
              rows: [
                ["モデル確認", "Available Models", "現在のアカウントで呼び出せるモデルを表示します。"],
                ["モデル検証", "Model Playground", "プロンプト、応答、ルーティング、コスト見積もりを確認します。"],
                ["Key 管理", "Key Management", "Key 作成時は割り当て済み Project を選択します。"],
                ["利用量確認", "Usage Analytics", "現在のアカウントに見えるリクエスト、Token、コストだけを表示します。"],
              ],
            },
          },
          {
            ...stats.groups[1].items[1],
            group: "ロールガイド",
            title: "チームリーダーガイド",
            description: "チームリーダーはプロジェクト、メンバー、Key 発行、チームレポート、プロジェクト別コスト配賦を管理します。",
            notesTitle: "プロジェクト統制フロー",
            notes: [
              "Project Spaces でプロジェクトを作成または選択します。Project はメンバー、Key、クォータ、コスト帰属の境界です。",
              "プロジェクトをクリックし、右側の詳細パネルでメンバーの表示、追加、編集、削除を行います。",
              "プロジェクトに Key を発行するときは、メンバーのロールに応じて Key 作成可否を決めます。",
              "Team Reports でメンバー、Project、モデル、Cost Center 別の利用量とコストを確認します。",
            ],
            table: {
              title: "プロジェクトメンバーのロール",
              columns: ["ロール", "既定能力"],
              rows: [
                ["Owner", "プロジェクト設定、メンバー、Key、クォータを管理します。"],
                ["Maintainer", "メンバーと Key を保守します。プロジェクトの技術責任者に適しています。"],
                ["Developer", "プロジェクト Key を作成、利用できます。アプリ開発者に適しています。"],
                ["Viewer", "プロジェクトと利用量のみ閲覧でき、Key は発行できません。"],
              ],
            },
          },
          {
            ...stats.groups[1].items[2],
            group: "ロールガイド",
            title: "管理者ガイド",
            description: "管理者は Provider、モデルカタログ、ルーティング、ID プロバイダー、権限、監査、コスト統制を管理します。",
            notesTitle: "公開前チェック",
            notes: [
              "Provider Channels で上流 Base URL、認証情報、リソースグループ、ヘルスチェックを設定します。",
              "Model Catalog で業務に公開する標準モデル名、能力タグ、価格単位を管理します。",
              "Routing Policies で公開モデルごとに少なくとも 1 つの有効ルートを設定します。",
              "System Settings で ID プロバイダー、ロール権限、既定ポリシー、監査保持期間、企業連携を設定します。",
            ],
            table: {
              title: "管理者チェックリスト",
              columns: ["領域", "確認項目"],
              rows: [
                ["Identity", "少なくとも 1 つの企業 ID プロバイダーを設定し、管理者アカウントを保持します。"],
                ["Routing", "ルート未設定モデルは管理画面で異なる背景色で表示します。"],
                ["Security", "API Key は一度だけ表示し、ローテーションと削除は監査します。"],
                ["Cost", "Provider、Project、Team、Cost Center を追跡可能にします。"],
              ],
            },
          },
        ],
      },
      {
        title: "API リファレンス",
        items: [
          {
            ...stats.groups[2].items[0],
            group: "API リファレンス",
            title: "モデル API",
            description: "Project API Key で OpenAI 互換のモデル API を呼び出します。",
            params: {
              title: "リクエストパラメーター",
              columns: ["フィールド", "型", "必須", "説明"],
              rows: [
                ["Authorization", "header", "はい", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["model", "string", "はい", "標準モデル名"],
                ["messages", "array", "はい", "system/user/assistant のメッセージ配列"],
                ["stream", "boolean", "いいえ", "true の場合は SSE ストリーミングレスポンスを返します"],
              ],
            },
            examplesTitle: "英語サンプル",
          },
          {
            ...stats.groups[2].items[1],
            group: "API リファレンス",
            title: "Key と Project",
            description: "Key は常に Project に属します。1 人のユーザーは複数 Project に参加でき、Key 作成時に所属 Project を選びます。",
            table: {
              title: "割り当てモデル",
              columns: ["対象", "管理者", "説明"],
              rows: [
                ["Project", "管理者またはチームリーダー", "メンバー、Key、クォータ、コスト配賦を持ちます。"],
                ["Membership", "Project Owner または Maintainer", "ユーザーが Project を閲覧できるか、Key を発行できるかを決めます。"],
                ["API Key", "権限を持つ Project メンバー", "Project に見えて、有効ルートがあるモデルだけ呼び出せます。"],
              ],
            },
          },
          {
            ...stats.groups[2].items[2],
            group: "API リファレンス",
            title: "トラブルシューティング",
            description: "ステータスコードから API Key、Project メンバー、モデルルート、クォータの問題を切り分けます。",
            table: {
              title: "よくあるエラー",
              columns: ["ステータス", "エラーコード", "対応"],
              rows: [
                ["401", "invalid_api_key", "Authorization に TokenHub API Key を指定しているか確認します。"],
                ["403", "project_forbidden / model_not_allowed", "ユーザーが Project に所属しているか、モデルが Project に公開されているか確認します。"],
                ["404/503", "provider_unavailable", "モデルに有効ルートを設定するか、上流 Provider のヘルスを確認します。"],
                ["429", "quota_exceeded", "Project クォータ、同時実行制限、Provider リソース制限を確認します。"],
                ["500", "upstream_error", "Request Logs で request_id を確認します。"],
              ],
            },
          },
        ],
      },
    ],
  };
}
