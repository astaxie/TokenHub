import { type AppRole } from "../core/types";
import { formatNumber } from "../domain/formatting";
import {
  gatewayAnthropicCountTokensCurl,
  gatewayAnthropicMessagesCurl,
  gatewayClaudeCodeExample,
  gatewayEmbeddingsCurl,
  gatewayListModelsCurl,
  gatewayOpenAISDKExample,
  gatewayPythonSDKExample,
  gatewayResponsesCurl,
  gatewayRetrieveModelCurl,
  gatewayStreamingCurl,
} from "./gateway-llm-en";
import { type GatewayDocBundle, type GatewayDocStats } from "./gateway-view";

export function gatewayJapaneseLLMUsageDocs(stats: GatewayDocStats, role: AppRole): GatewayDocBundle {
  const teamLeader = role === "team_leader";
  return {
    defaultDocID: "quickstart",
    nav: {
      title: "API ドキュメント",
      subtitle: "OpenAI 互換 LLM 利用",
      searchPlaceholder: "エンドポイント、パラメーター、エラーを検索",
      noResults: "一致する API ドキュメントはありません",
    },
    eyebrow: "LLM API Docs",
    title: "大規模言語モデルを呼び出す",
    description: teamLeader
      ? "Project Key で承認済みモデルを呼び出し、Project 単位で権限、クォータ、コスト配賦を管理します。"
      : "Project API Key で OpenAI 互換モデル API を呼び出します。モデル一覧を確認してから Chat、Responses、Embeddings を利用します。",
    languageLabel: "ドキュメント言語",
    quickInfoLabel: "API 基本情報",
    quickCards: {
      baseURL: "Base URL",
      authorization: "Authorization",
      sampleModel: "サンプルモデル",
      currentConfig: "現在の API 範囲",
      activeRoutes: `${formatNumber(stats.visibleModelCount)} 件の呼び出し可能モデル`,
      apiKeys: `${formatNumber(stats.apiKeyCount)} 件の Project Key`,
    },
    groups: [
      {
        title: "クイックスタート",
        items: [
          {
            id: "quickstart",
            group: "クイックスタート",
            badge: "GUIDE",
            title: "はじめての接続",
            description: "TokenHub 経由で最初の OpenAI 互換 LLM リクエストを送信します。",
            details: [
              { label: "Base URL", value: stats.baseURL },
              { label: "認証 Header", value: "Authorization: Bearer <API Key>" },
              { label: "サンプルモデル", value: stats.sampleModel },
              { label: "現在の範囲", value: teamLeader ? "チーム Project" : "割り当て済み Project" },
            ],
            notesTitle: "呼び出し順序",
            notes: [
              "Key Management で Project API Key を作成またはコピーします。コンソールログイントークンは /v1 モデル API では利用できません。",
              "まず GET /v1/models を呼び出します。レスポンスがその Key で利用できるモデル一覧です。",
              "モデル ID を選び、POST /v1/chat/completions、/v1/responses、/v1/embeddings を呼び出します。",
              "失敗時はレスポンスの request_id をコピーし、Request Logs で調査します。",
            ],
            examplesTitle: "最初のリクエスト",
            examples: [
              { title: "利用可能モデル一覧", code: gatewayListModelsCurl(stats) },
              { title: "Chat Completion 作成", code: stats.chatCurl },
            ],
          },
          {
            id: "authentication",
            group: "クイックスタート",
            badge: "AUTH",
            title: "認証方式",
            description: "すべてのモデル API は Project API Key を Authorization header で送信します。",
            table: {
              title: "必須 Header",
              columns: ["Header", "必須", "値"],
              rows: [
                ["Authorization", "はい", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "POST リクエスト", "application/json"],
              ],
            },
            notesTitle: "権限チェック",
            notes: [
              "Key は有効で、有効な Project に紐づいている必要があります。",
              "リクエストしたモデルは Project に表示され、少なくとも 1 つの有効ルートが必要です。",
              teamLeader
                ? "チームリーダーは Key 発行時に正しい Project を選び、利用量が正しいチームと Cost Center に集計されるようにします。"
                : "複数 Project に所属している場合、利用量とコストを持つ Project を選んでから Key を作成します。",
            ],
          },
        ],
      },
      {
        title: "LLM API",
        items: [
          {
            id: "list-models",
            group: "LLM API",
            method: "GET",
            path: "/v1/models",
            title: "モデル一覧を取得",
            description: "現在の API Key で利用できる LLM モデル一覧を返します。この Endpoint は OpenAI API 互換です。",
            params: {
              title: "リクエスト Header",
              columns: ["フィールド", "型", "必須", "説明"],
              rows: [
                ["Authorization", "header", "はい", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "header", "はい", "application/json"],
              ],
            },
            table: {
              title: "モデルフィールド",
              columns: ["フィールド", "説明"],
              rows: [
                ["id", "以降の API 呼び出しで model に指定するモデル ID。"],
                ["object", "オブジェクト種別。通常は model。"],
                ["created", "モデル作成 Unix timestamp。"],
                ["input_token_price_per_m", "JieKou 互換の 100 万 input tokens あたり整数価格。"],
                ["output_token_price_per_m", "JieKou 互換の 100 万 output tokens あたり整数価格。"],
                ["title", "モデルタイトル。"],
                ["description", "モデル説明。"],
                ["context_size", "最大コンテキスト長。"],
              ],
            },
            examplesTitle: "例",
            examples: [{ title: "cURL", code: gatewayListModelsCurl(stats) }],
          },
          {
            id: "retrieve-model",
            group: "LLM API",
            method: "GET",
            path: "/v1/models/{model}",
            title: "指定モデル情報を取得",
            description: "現在の API Key で利用できる単一モデル情報を返します。レスポンスは JieKou 互換のモデルオブジェクトです。",
            params: {
              title: "Path と Header",
              columns: ["フィールド", "型", "必須", "説明"],
              rows: [
                ["model", "path", "はい", `/v1/models のモデル ID。例: ${stats.sampleModel}`],
                ["Authorization", "header", "はい", "Bearer YOUR_TOKENHUB_API_KEY"],
                ["Content-Type", "header", "はい", "application/json"],
              ],
            },
            table: {
              title: "レスポンスフィールド",
              columns: ["フィールド", "説明"],
              rows: [
                ["id", "API 呼び出しで使うモデル ID。"],
                ["created", "モデル作成 Unix timestamp。"],
                ["object", "オブジェクト種別。常に model。"],
                ["input_token_price_per_m", "JieKou 互換の 100 万 input tokens あたり整数価格。"],
                ["output_token_price_per_m", "JieKou 互換の 100 万 output tokens あたり整数価格。"],
                ["title", "モデルタイトル。"],
                ["description", "モデル説明。"],
                ["context_size", "最大コンテキスト長。"],
              ],
            },
            examplesTitle: "例",
            examples: [{ title: "cURL", code: gatewayRetrieveModelCurl(stats) }],
          },
          {
            id: "chat-completions",
            group: "LLM API",
            method: "POST",
            path: "/v1/chat/completions",
            title: "Chat Completion を作成",
            description: "メッセージ一覧からモデル応答を生成します。通常のチャット、ツール呼び出し、構造化出力、ストリーミングに利用します。",
            params: {
              title: "リクエスト Body",
              columns: ["フィールド", "型", "必須", "説明"],
              rows: [
                ["model", "string", "はい", `/v1/models のモデル ID。例: ${stats.sampleModel}`],
                ["messages", "array", "はい", "system、user、assistant のメッセージ配列。"],
                ["max_tokens", "integer", "いいえ", "最大生成 tokens。"],
                ["temperature", "number", "いいえ", "サンプリング温度。"],
                ["reasoning_effort", "string", "いいえ", "推論強度。選択されたルートが対応しない場合は省略します。"],
                ["stream", "boolean", "いいえ", "true の場合は SSE で返し、data: [DONE] で終了します。"],
                ["tools", "array", "いいえ", "互換上流モデルの関数ツール。"],
                ["response_format", "object", "いいえ", "対応モデルでは JSON object または JSON schema を指定できます。"],
              ],
            },
            table: {
              title: "レスポンスフィールド",
              columns: ["フィールド", "説明"],
              rows: [
                ["id", "リクエスト ID。"],
                ["choices[].message", "モデルが返した assistant メッセージ。"],
                ["choices[].finish_reason", "停止理由。stop や length など。"],
                ["usage", "prompt、completion、total token 統計。"],
              ],
            },
            examplesTitle: "例",
            examples: [
              { title: "非ストリーミング", code: stats.chatCurl },
              { title: "ストリーミング", code: gatewayStreamingCurl(stats) },
            ],
          },
          {
            id: "responses-api",
            group: "LLM API",
            method: "POST",
            path: "/v1/responses",
            title: "Responses リクエストを作成",
            description: "Responses 形式でシンプルなテキスト入力を扱い、将来のマルチモーダル拡張にも備えます。",
            params: {
              title: "リクエスト Body",
              columns: ["フィールド", "型", "必須", "説明"],
              rows: [
                ["model", "string", "はい", "呼び出し可能なモデル ID。"],
                ["input", "string | array", "はい", "入力テキストまたは構造化入力。"],
                ["reasoning.effort", "string", "いいえ", "OpenAI 互換、Anthropic、Gemini ルート向けのネストされた推論強度。"],
                ["stream", "boolean", "いいえ", "未実装です。true の場合は 501 を返します。"],
              ],
            },
            examplesTitle: "例",
            examples: [{ title: "cURL", code: gatewayResponsesCurl(stats) }],
          },
          {
            id: "anthropic-messages",
            group: "LLM API",
            method: "POST",
            path: "/v1/messages",
            title: "Anthropic Message を作成",
            description: "Claude Code または Anthropic 互換クライアントから、テキスト、画像、クライアントツール、ツール結果、ストリーミングを利用できます。",
            params: {
              title: "リクエスト Body",
              columns: ["フィールド", "型", "必須", "説明"],
              rows: [
                ["model", "string", "はい", "呼び出し可能な TokenHub モデル ID。"],
                ["max_tokens", "integer", "はい", "最大生成 tokens。"],
                ["messages", "array", "はい", "user、assistant、構造化 content block からなる Anthropic メッセージ。"],
                ["system", "string | array", "いいえ", "トップレベルの Anthropic system prompt。"],
                ["tools", "array", "いいえ", "input_schema で定義するクライアントツール。"],
                ["tool_choice", "object", "いいえ", "自動、必須、指定、無効のツール利用を制御します。"],
                ["stream", "boolean", "いいえ", "true の場合は Anthropic の名前付き SSE event を返します。"],
              ],
            },
            notesTitle: "ルーティング動作",
            notes: [
              "Anthropic ネイティブルートでは content block と beta header を保持します。",
              "OpenAI 互換ルートではクライアントツール、ツール結果、画像、ストリーミング event を変換します。",
              "OpenAI 互換ルートで未対応の Anthropic サーバーツールを受けた場合は、明示的な 400 response を返します。",
            ],
            examplesTitle: "例",
            examples: [
              { title: "Messages API", code: gatewayAnthropicMessagesCurl(stats) },
              { title: "Claude Code", code: gatewayClaudeCodeExample(stats) },
            ],
          },
          {
            id: "anthropic-count-tokens",
            group: "LLM API",
            method: "POST",
            path: "/v1/messages/count_tokens",
            title: "Anthropic Message の Token を見積もる",
            description: "Key 認証とモデル権限の確認後、決定的な input token 見積もりを返します。推論利用としては課金されません。",
            examplesTitle: "例",
            examples: [{ title: "cURL", code: gatewayAnthropicCountTokensCurl(stats) }],
          },
          {
            id: "embeddings-api",
            group: "LLM API",
            method: "POST",
            path: "/v1/embeddings",
            title: "Embeddings を作成",
            description: "検索、RAG、分類、クラスタリング向けのテキストベクトルを生成します。",
            params: {
              title: "リクエスト Body",
              columns: ["フィールド", "型", "必須", "説明"],
              rows: [
                ["model", "string", "はい", "Key から見える embedding モデル ID。"],
                ["input", "string | array", "はい", "ベクトル化するテキスト。"],
                ["encoding_format", "string", "いいえ", "対応時は float または base64。"],
              ],
            },
            examplesTitle: "例",
            examples: [{ title: "cURL", code: gatewayEmbeddingsCurl(stats) }],
          },
        ],
      },
      {
        title: teamLeader ? "チーム導入" : "Project Key",
        items: [
          {
            id: "project-keys",
            group: teamLeader ? "チーム導入" : "Project Key",
            badge: "KEY",
            title: teamLeader ? "Project に Key を発行" : "Project Key を利用",
            description: teamLeader
              ? "メンバーは複数 Project に所属できます。利用量、クォータ、コスト配賦を持つ正しい Project に Key を発行します。"
              : "複数 Project に所属している場合があります。利用量、クォータ、コスト配賦を持つ Project を選んで Key を作成します。",
            table: {
              title: teamLeader ? "チーム Key 発行チェックリスト" : "Project Key ルール",
              columns: ["項目", "ルール"],
              rows: teamLeader ? [
                ["Project", "Key 発行前に Project を作成または選択します。"],
                ["Members", "Project 詳細パネルでアプリ責任者を追加します。"],
                ["Models", "アプリに渡す前に新しい Key で GET /v1/models を検証します。"],
                ["Reports", "メンバー、Project、モデル、Cost Center 別にチーム利用量を確認します。"],
              ] : [
                ["Project", "各 API Key は 1 つの Project に属します。"],
                ["Models", "モデル一覧は Project と Key の権限でフィルタリングされます。"],
                ["Secret", "新しい Key は一度だけ表示されます。アプリのシークレット管理に保存します。"],
                ["Usage", "リクエストは Key の Project とアカウントに配賦されます。"],
              ],
            },
          },
          {
            id: "sdk-examples",
            group: teamLeader ? "チーム導入" : "Project Key",
            badge: "SDK",
            title: "SDK と Claude Code",
            description: "OpenAI 互換 SDK は TokenHub Base URL を使い、Claude Code は TokenHub Host URL を使います。",
            examplesTitle: "クライアント例",
            examples: [
              { title: "Node.js", code: gatewayOpenAISDKExample(stats) },
              { title: "Python", code: gatewayPythonSDKExample(stats) },
              { title: "Claude Code", code: gatewayClaudeCodeExample(stats) },
            ],
          },
          {
            id: "errors",
            group: teamLeader ? "チーム導入" : "Project Key",
            badge: "REF",
            title: "エラーと調査",
            description: "ステータスコードから API Key、Project メンバー、モデルルート、クォータの問題を切り分けます。",
            table: {
              title: "よくあるエラー",
              columns: ["ステータス", "主な原因", "対応"],
              rows: [
                ["401", "API Key の不足、形式不正、無効化、期限切れ。", "Authorization header と Key 状態を確認します。"],
                ["403", "Project、Key、モデル権限がリクエストを許可していません。", teamLeader ? "Project メンバー、Key のモデル範囲、チーム Project 所有を確認します。" : "チームリーダーに Project メンバーとモデル権限の確認を依頼します。"],
                ["404/503", "モデルを処理できる健全なルートがありません。", "管理者にルート有効化または Provider ヘルス確認を依頼します。"],
                ["429", "Project クォータ、同時実行、Provider リソース制限に達しました。", teamLeader ? "Project クォータと同時実行制限を確認します。" : "クォータ回復を待つか、増枠を依頼します。"],
                ["500", "上流 Provider またはルーティングエラー。", `Request Logs で request_id を検索します。現在見えるログサンプル: ${formatNumber(stats.requestLogCount)} 件。`],
              ],
            },
          },
        ],
      },
    ],
  };
}
