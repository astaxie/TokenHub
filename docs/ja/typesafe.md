# TypeSafe / Jev の接続

言語：[English](../typesafe.md) | [简体中文](../zh-CN/typesafe.md) | 日本語

TokenHub は専用 Provider adapter と `POST /v1/systemone` で TypeSafe System One を提供します。Jev は共有状態から分類、確率、スコアを返し、チャットテキストは生成しません。第 1 段階には Provider 設定、モデルメタデータ、検出、ヘルスチェック、ルーティング、ガバナンス、課金が含まれます。専用 Playground とゲートウェイ内部の意味的ルーティングは対象外です。

## Provider の設定

1. **Provider Channels** で **TypeSafe** を追加します。adapter type は `typesafe`、上流 Base URL は `https://api.typesafe.ai/v1` です。
2. 認証情報フィールドに TypeSafe API Key を入力し、必要な Jev モデルを取り込みます。既存の暗号化保存とマスキング規則が適用されます。
3. modality が `decision`、capability が `systemone` の公開モデルと、取り込んだ Provider モデルへのルートを設定します。プロジェクト Key に公開モデルの利用権限を付与します。Provider 在庫の原価と公開モデルの販売価格は別々に設定します。
4. Provider ヘルスチェックを実行します。上流の `GET /v1/models` で認証を検証し、推論は実行しません。推論は合成 System One リクエストで別途検証します。

カタログには `jev-1.13.0`、`jev-latest`、`jev-preview` が含まれます。再現性が必要な評価では固定バージョンを使用します。エイリアスは変更される可能性があります。ライブ検出がエイリアスのみを返しても、固定バージョンが利用できないとは限りません。`POST /api/admin/provider-catalog/custom` は `type: "typesafe"` を受け取り、ネイティブ形式でモデルを検出します。新しいモデルの価格は推定しません。

## ゲートウェイ呼び出し

公開 TokenHub モデル名と **TokenHub プロジェクト API Key** を使用します。

```bash
curl https://tokenhub.example/v1/systemone \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "jev-1.13.0",
    "state": "Please refund this order.",
    "questions": {
      "intent": {
        "type": "choice",
        "instructions": "Classify the customer intent.",
        "criteria": {"refund": "Return funds.", "other": "Another request."}
      },
      "urgent": {"type": "noul", "instructions": "Is immediate attention required?"},
      "severity": {"type": "score", "instructions": "Assess urgency.", "criteria": ["low", "high"]}
    }
  }'
```

ネイティブ応答は `model`、`answers`、`usage.input_tokens` / `usage.output_tokens` を含みます。回答キーは質問キーと一致します。応答モデルは上流で解決されたバージョンです。リクエストログには公開モデル名とルートの上流モデル名も保存されます。

成功応答には TokenHub の `x-request-id` が含まれ、成功した上流が ID を報告した場合は `x-typesafe-request-id` も含まれます。SDK の `client.systemOne(...).withResponse().requestId` は後者を読み取ります。両ヘッダーはブラウザクライアントにも公開され、上流 ID がない場合は補完しません。

| Primitive | Criteria | 回答 |
| --- | --- | --- |
| `choice` | ラベルから説明への空でないオブジェクト | `choice`、ラベルごとの `probabilities`、`confidence` |
| `noul` | `true` と／または `false` を説明する任意のオブジェクト、または `null` | `[0, 1]` の `noul`。confidence は必須ではありません |
| `score` | 2 段階以上の説明を並べた配列 | `[0, N-1]` の期待レベル索引 `score`、`legend`、レベルごとの `probabilities`、`confidence` |

`state` は必須で、文字列、JSON オブジェクト、配列、`null` を受け取ります。各質問の `instructions` は省略でき、文字列、オブジェクト、配列、`null` も指定できます。criteria の説明も同じ型を受け取ります。転送時には、[SDK 0.6.0 の型定義](https://github.com/typesafe-ai/typesafe-sdk-js/blob/v0.6.0/src/types.ts) に従い、省略と明示的な `null` を区別します。ネストした JSON 数値の精度は保持されます。質問は状態を共有し、独立して評価されます。低 confidence は通常の結果として返され、自動再試行やエスカレーションは行われません。採用閾値はアプリケーションで設定します。

ゲートウェイの上限は 1–1,024 質問、質問ごとに 4,096 criteria、JSON エントリーごとに 64 階層のコンテナです。通常のリクエストサイズ制限も適用されます。

各 System One リクエストまたは応答は最大 8 MiB、ペイロード全体で最大 32,768 JSON ノードです。コンテナ、スカラー値、オブジェクトキーを数えます。数値の指数部は先頭のゼロを含め最大 128 桁です。map や構造体にデコードする前に、重複キー（エスケープ後に同一となるキーを含む）とプロトコルフィールド名の大文字・小文字違いを拒否します。hook ペイロードにも同じ検証を適用します。リクエスト JSON の上限超過は `400`、リクエスト全体の guardrail 検査対象が 32,768 個を超えた場合は `422` です。不正な上流または hook 応答はマスキングされた `502` となり、独立して検証できる曖昧さのない有効な使用量だけを保持します。

上流ドキュメントに基づき、カタログには合計 64,000 token と「状態＋最大質問」32,000 token の制限を記録しています。TokenHub はクォータ受け入れのために token を推定し、tokenizer 固有の検証は TypeSafe が行います。ストリーミングとチャット専用フィールドは拒否されます。不正な JSON や未知のフィールドは `400`、primitive の意味的検証エラーは `422` です。

## JavaScript SDK の互換範囲

`@typesafe-ai/sdk@0.6.0` の `client.systemOne()` を使用できます。`baseURL` は **`/v1` を含まない TokenHub origin** にします。SDK が `/v1/systemone` を追加します。TokenHub Key と公開モデル名を渡します。SDK smoke test はこのメソッドを使用します。

```bash
cd sdk
npm ci
TOKENHUB_BASE_URL=http://localhost:8080 \
TOKENHUB_API_KEY="$TOKENHUB_API_KEY" \
TOKENHUB_MODEL=jev-1.13.0 npm run test:systemone
```

この段階は `systemOne()` のみを対象とし、TypeSafe SDK 全体の互換性は提供しません。TokenHub の既存の複数プロトコル用 `/v1/models` 契約は保持され、SDK の `models.list()` 用 TypeSafe 応答は模倣しません。OpenAI Chat、Responses、Embeddings、ストリーミング契約は変更しません。既存のチャット Playground では Jev を実行できません。

## ガバナンスと課金

スコアの各 `legend` エントリーは、上流に送信した対応する criterion と一致する必要があります。リクエスト hook が criteria を変換した場合は、成功したルートの実際の criteria で検証し、マスキングを含むその値を応答に保持します。応答 hook は別の criteria に置き換えられません。質問 ID、回答型、選択肢ラベル、スコアの段階数は、共通の前処理後のリクエストと一致する必要があります。

ゲートウェイは応答の整合性を検証します。確率の合計と 1 の差は 1 パーセントポイント以内、Choice の選択肢の確率と最大値の差も 1 パーセントポイント以内、Score と「索引 × 確率」の元の合計との差は `0.01 × (N-1)` 以内とします。境界を含むこれらの許容値には浮動小数点の微小誤差を加えますが、上流が各値を個別に丸めたすべての分布の受け入れを保証するものではありません。確率は再正規化しません。不整合な応答はマスキングされた `502` となり、報告済みの有効な使用量は保持します。TypeSafe のモデル検出にも、上流リクエスト前に共通のカスタムヘッダー検証を適用します。

プロジェクト認証、モデル権限、クォータ、ルート選択、Provider 認証情報、エラー分類、使用量保存、リクエストログ、Trace export を再利用します。privacy と guardrail hook は `route_protocol: "systemone"` を受け取ります。決定的な送信前チェックは state、質問 ID、instructions、criteria、ネストしたオブジェクトキーと JSON の数値リーフを検査します。数値の元の精度は保持され、整数値の科学表記も最大 64 文字まで展開して検査します。構造上のキーや数値のマスキングが必要な場合は、テキストマスクでは回答契約や JSON 型を維持できないため、リクエストを拒否します。任意の前処理、ルート、Provider 呼び出し、応答、使用量 hook は既存のゲートウェイライフサイクルに従います。この段階では System One 応答をキャッシュしません。

`422` などのクライアントエラーではフェイルオーバーしません。上流応答ボディの読み取り中に呼び出し元がキャンセルした場合も、キャンセルエラーを保持し、フェイルオーバーしません。上流の `429`、`529` などの一時的なエラーは既存ポリシーに従って別の互換ルートへ切り替えられます。回答の欠落・形式エラー、確率・スコアの範囲外、使用量の欠落・不正値は、マスキングされた `502` になります。失敗した試行の有効な使用量も保持します。上流の認証エラーは Provider エラーであり、プロジェクト Key の認証エラーには変換しません。

**2026-09-19** に確認した価格は、**入力 100 万 token あたり USD 0.042、出力単価 USD 0** です。出力 token も使用量と token クォータに含まれます。最終課金には上流が報告した token 数と設定価格を使用し、推定 token や質問単位では課金しません。導入前にエイリアスの価格と Provider 原価を確認します。出典：[TypeSafe モデル](https://docs.typesafe.ai/models)。

## 導入とロールバック

専用プロジェクト Key と明示的な Jev ルートから開始します。スコアを業務判断に使う前に、対象言語とタスクのラベル付きサンプルで評価します。遅延、上流エラー、実使用量、原価を観測します。データベース移行やデプロイ環境変数の追加はありません。公開モデルまたはルートを無効化すると通信を停止できます。`tokenhub.provider.typesafe` を無効化すると、カタログ項目と実行アダプターの両方が削除され、既存の TypeSafe ルートも利用できなくなります。TypeSafe ルートしかないモデルへの `/v1/systemone` 呼び出しは `501 provider_capability_not_supported` を返します。プラグインを再度有効化すると、既存のルート設定でアダプターを利用できます。一部の通信だけを停止する場合は、該当するモデルまたはルートを無効化します。

参考：[HTTP API](https://docs.typesafe.ai/api)、[高度な入力](https://docs.typesafe.ai/primitives/advanced)、[confidence](https://docs.typesafe.ai/confidence)、TokenHub の `/openapi.json` 契約。
