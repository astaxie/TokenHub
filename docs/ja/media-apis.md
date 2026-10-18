# テキスト・画像・音声・動画 API ガイド

言語：[English](../media-apis.md) | [简体中文](../zh-CN/media-apis.md) | 日本語

TokenHub の共通ゲートウェイとプロジェクト API キーで、許可されたテキスト・メディアモデルを呼び出します。このガイドは [APIMart API マニュアル](https://docs.apimart.ai/cn/index)の用途別構成を参考にしながら、TokenHub 独自の認証、ルーティング、タスク分離、制限を説明します。

**エンドポイントへの対応は、モデルが利用可能であることを意味しません。** 管理者による上流認証情報の設定、公開モデル ID の登録、上流モデルへのマッピング、キーへのアクセス許可が必要です。形式、長さ、解像度、音声、モデルのバージョン、素材の有効期限は上流に依存します。TokenHub はベンダーアカウントを同梱せず、APIMart の全モデル・機能を保証しません。

## API を選ぶ

| 用途 | メソッドとパス | 結果 |
| --- | --- | --- |
| 利用可能モデルの確認 | `GET /v1/models` | キーがアクセス可能なモデル |
| テキスト・画像理解・ツール呼び出し | `POST /v1/chat/completions` | Chat JSON または SSE |
| Responses 互換リクエスト | `POST /v1/responses` | Response JSON または SSE。任意でローカルバックグラウンドジョブ |
| Anthropic Messages | `POST /v1/messages` | Anthropic JSON または SSE |
| Gemini ネイティブリクエスト | `POST /v1beta/models/{model}:generateContent` | Gemini ネイティブ JSON |
| Gemini ネイティブストリーミング | `POST /v1beta/models/{model}:streamGenerateContent?alt=sse` | Gemini ネイティブ SSE |
| 画像生成 | `POST /v1/images/generations` | 画像または上流タスクの受付結果 |
| 画像編集・バリエーション | `POST /v1/images/edits`、`POST /v1/images/variations` | プロバイダーの画像応答 |
| 音声合成 | `POST /v1/audio/speech` | 音声バイト列またはプロバイダー対応ストリーム |
| 音声認識・翻訳 | `POST /v1/audio/transcriptions`、`POST /v1/audio/translations` | JSON、テキスト、字幕 |
| 動画生成 | `POST /v1/videos/generations` | 上流タスクの受付結果 |
| 画像・動画タスクの照会 | `GET /v1/tasks/{task_id}` | プロバイダーの状態と結果 |

ベクトルと文書の順位付けは[埋め込み](embeddings.md)と[再ランキング](rerank.md)を参照してください。デプロイ先の対話式 API 仕様は `/docs`、機械可読スキーマは `/openapi.json` にあります。

## 認証とクイックスタート

コンソールのポートではなくゲートウェイのアドレスを使います。以下の例は `/v1` を含まないルート URL です。OpenAI SDK の `base_url` には `$TOKENHUB_URL/v1` を指定します。プロジェクト API キーは**キー管理**から取得します。コンソールのログイントークンではモデル API を呼び出せません。

```bash
export TOKENHUB_URL='http://localhost:8080'
export TOKENHUB_API_KEY='YOUR_TOKENHUB_PROJECT_KEY'

curl --fail-with-body "$TOKENHUB_URL/v1/models" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY"
```

返された `data[].id` を `model` に指定します。以下の `text-model`、`image-model`、`speech-model`、`transcription-model`、`seedance-video` は、公開するか実際の名前に置き換える**公開エイリアス**です。上流モデル ID と同名である必要はありません。

```bash
curl --fail-with-body "$TOKENHUB_URL/v1/chat/completions" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"text-model","messages":[{"role":"user","content":"Explain an API gateway in one sentence."}],"stream":false}'
```

テキストは `choices[].message.content` から読み取ります。調査用にリクエスト ID を保存し、**リクエストログ**で確認します。`/v1/models` への表示は可視性と利用条件を満たすルートの存在を示しますが、上流の全機能の稼働を保証しません。APIMart の `expand` メタデータ形式とアカウント残高 API は実装していません。

## プロバイダー設定とモデル選択

APIMart 互換アカウントには **OpenAI-compatible** プロバイダーを追加し、ベース URL を `https://api.apimart.ai/v1` に設定して認証情報を登録します。例えば `text-model` を利用可能なテキストモデルへ、`image-model` を `gpt-image-2` へ、`seedance-video` を `seedance-2.0` へマッピングします。用途に応じて `image`、`audio`、`video` を設定し、マルチモーダルチャットには入出力モダリティも設定します。メディアの Chat/Responses は応答キャッシュを使用しません。

Anthropic と Gemini のネイティブ要求には、そのプロトコルと応答形式に対応するルートが必要です。OpenAI-compatible のベース URL 設定だけではネイティブ互換性は確認できません。アダプターと SDK 設定は[利用者ガイド](user-guide.md)を参照してください。ベンダーの例ではテキスト応答にラッパーが付くこともあるため、クライアントが期待する形式を返すルートか確認してください。

公開名 `gpt-image-2`、Codex サブスクリプション画像モデル、管理対象画像プラグインは TokenHub の管理対象画像フローを使います。APIMart 非同期生成など上流 Images の全仕様を使う場合は、`image-model` など別のエイリアスを公開し、`gpt-image-2` にマッピングします。[管理対象画像ガイド](user-guide.md#codex-サブスクリプション画像生成)を参照してください。

アップグレード時は通常の起動時マイグレーションで v8 `add-media-task-bindings` を適用します。タスク関連付けは再起動後も保持されます。保存した上流 ID を復号できるようサーバーの秘密鍵を維持してください。新しい環境変数は不要です。

## テキストとマルチモーダル理解

### Chat Completions

| フィールド | 要件 | 意味 |
| --- | --- | --- |
| `model` | 必須 | 公開モデル ID |
| `messages` | 必須 | 会話。対応モデルではコンテンツパーツを使用可能 |
| `stream` | 任意の boolean | SSE は明示的に `true`、単一 JSON は `false` |
| `max_tokens`、`max_completion_tokens` | 任意 | 出力上限。実際の挙動はルートとモデルに依存 |
| `temperature`、`reasoning_effort` | 任意 | モデルに応じた生成制御 |
| `tools`、`tool_choice`、`response_format` | 任意 | 対応モデルのツール・構造化出力 |

ストリーミングは `curl -N` と `"stream":true` を使い、完了まで `choices[].delta` イベントを処理します。他社の文書にある既定のストリーミング動作を全モデル共通と考えないでください。

画像理解では、画像入力に対応するモデルを選び、ユーザーメッセージを次のパーツに置き換えます。

```json
{"role":"user","content":[{"type":"text","text":"Describe this image."},{"type":"image_url","image_url":{"url":"https://your-assets.example/reference.png"}}]}
```

素材 URL はアクセス可能なファイルに置き換えます。OpenAI-compatible ルートは対応する画像・音声フィールドを保持しますが、ネイティブ変換ルートの能力はより限定されます。Chat から Gemini への変換では base64 画像 data URI を使用し、非対応の音声パーツは削除せずエラーにします。[利用者ガイド](user-guide.md)のプロトコル差異を参照してください。

### Responses・Anthropic・Gemini

```bash
curl --fail-with-body "$TOKENHUB_URL/v1/responses" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"text-model","input":"Summarize the benefits of a shared gateway.","stream":false}'

curl --fail-with-body "$TOKENHUB_URL/v1/messages" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'anthropic-version: 2023-06-01' \
  -H 'Content-Type: application/json' \
  -d '{"model":"claude-model","max_tokens":256,"messages":[{"role":"user","content":"Hello"}]}'

curl --fail-with-body "$TOKENHUB_URL/v1beta/models/gemini-model:generateContent" \
  -H "x-goog-api-key: $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"contents":[{"role":"user","parts":[{"text":"Hello"}]}]}'
```

Responses は `output`、Anthropic は `content[]`、Gemini は `candidates[].content.parts[]` から結果を取得します。次のターンにはツールと推論フィールドを引き継ぎます。Responses は対応するベンダー固有のメディアオブジェクトも扱います。Gemini ネイティブメディアは `generationConfig`、`responseModalities`、`imageConfig`、インラインメディア、思考署名を保持します。メディア JSON/SSE はサイズ上限内でバッファリングして配信します。

## 画像

### 画像生成

```bash
curl --fail-with-body "$TOKENHUB_URL/v1/images/generations" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"image-model","prompt":"A watercolor lighthouse at sunrise","n":1,"size":"16:9","resolution":"2k"}'
```

公開エイリアスを介して APIMart `gpt-image-2` のパラメーター形式を使用する例です。他のプロバイダーでは `1024x1024` などのピクセル指定が必要な場合があります。異なるモデルの規則を混在させないでください。

| フィールド | 意味 |
| --- | --- |
| `model`、`prompt` | 公開モデルと画像の説明 |
| `n` | 画像数。上限は上流が検証 |
| `size`、`resolution`、`quality` | 上流独自の比率、ピクセル寸法、解像度、品質 |
| `image_urls` など | 対応モデルの参照画像 |
| `response_format` | `url`、`b64_json`、`base64` など上流が対応する形式 |

同期応答は通常 `data[].url` または `data[].b64_json` を含みます。非同期応答は `data[].task_id`、または APIMart の新しい受付形式では `data.object:"generation.task"` を伴う `data.id` を含みます。返された各 TokenHub ID を以下のタスク API で照会します。プロバイダーの結果フィールドを保持し、TokenHub は URL のダウンロードや有効期限の延長を行いません。

### 画像編集とマスク

```bash
curl --fail-with-body "$TOKENHUB_URL/v1/images/edits" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -F 'model=image-model' \
  -F 'prompt=Replace the background with a sunrise sky' \
  -F 'image=@input.png' \
  -F 'mask=@mask.png'
```

ローカルファイルと multipart 編集に対応するルートを使い、boundary は cURL に生成させます。JSON 編集と `/v1/images/variations` も互換プロバイダーで使用できます。マスク、複数画像フィールド、その他のオプションは上流仕様に従います。管理対象画像ジョブの単一画像検証、保存アセット、`Prefer: respond-async` は別の仕様を維持します。

## 音声

### 音声合成

```bash
curl --fail-with-body "$TOKENHUB_URL/v1/audio/speech" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"speech-model","input":"Hello from TokenHub.","voice":"alloy","response_format":"opus","speed":1}' \
  --output speech.opus
```

| フィールド | 意味 |
| --- | --- |
| `model` | 公開した音声モデル |
| `input` | 合成するテキスト。長さの上限は上流に依存 |
| `voice` | 上流の音声 ID。`alloy` は例であり共通ではない |
| `response_format` | 対応する音声形式。拡張子も一致させる |
| `speed` | 対応モデルの読み上げ速度 |

成功応答は上流 Content-Type の音声バイト列であり、JSON や base64 ラッパーではありません。保存ファイルを使う前に HTTP ステータスを確認します。上流が対応すれば SSE 音声も受け付けますが、直接メディア配信はバッファリングされ、低遅延音声ストリーミングにはなりません。

### 音声認識と翻訳

```bash
curl --fail-with-body "$TOKENHUB_URL/v1/audio/transcriptions" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -F 'model=transcription-model' \
  -F 'file=@sample.wav' \
  -F 'response_format=json'
```

`file` はローカルアップロード、`model` は公開音声認識モデルです。任意の `language`、`prompt`、`response_format`、繰り返しの `timestamp_granularities[]` は上流仕様に従います。JSON は通常 `{"text":"..."}` を返し、テキスト、SRT、VTT、verbose JSON の対応はプロバイダー次第です。翻訳は `/v1/audio/translations` と対応モデルを使います。翻訳先言語の規則もモデルに依存します。

multipart の文字起こしと翻訳では、空でない `file` を 1 個だけ指定し、最大 25 MiB とします。対応形式は FLAC、MP3、MP4、MPEG、MPGA、M4A、OGG、WAV、WebM です。拡張子、メディア型、コンテナー署名が一致する必要があり、コーデック対応は上流が確認します。要求全体のサイズ上限も適用されます。ファイル欠落は `400 missing_audio_file`、未対応形式は `415 unsupported_audio_format`、ファイル上限超過は `413 audio_file_too_large` です。

公開するルートのアダプターは明示的に `transcription` をサポートし、モデルのエンドポイントに対象操作を含める必要があります。OpenAI と設定済み OpenAI 互換/ローカルアダプターが対応し、各要求で最新のインポート済みエンドポイント情報も再確認します。JSON ベンダー拡張は一致するプロバイダー呼び出しフックで処理できますが、multipart アップロードにはネイティブアダプターが必要です。公開、フック、アップロード検証、SDK スモークテストは[文字起こしアップロードと計量の契約](media-api-operations.md#文字起こしアップロードと計量の契約)を参照してください。

音声クローンと音楽は、既存のプロバイダー固有 Chat/Responses フローを引き続き使用できます。[メディア運用](media-api-operations.md)を参照してください。APIMart の `/v1/music/*`、音声・素材管理、アップロード、webhook 管理を汎用 TokenHub API としては実装していません。

## 動画と Seedance

```bash
curl --fail-with-body "$TOKENHUB_URL/v1/videos/generations" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"seedance-video","prompt":"A calm ocean at sunrise, slow camera movement","duration":5,"size":"16:9","resolution":"720p","generate_audio":true}'
```

`seedance-video` を APIMart `seedance-2.0` などの上流モデルに設定します。他の互換動画モデルも同じエンドポイントでそれぞれのオプションを使います。

| フィールド | 意味 |
| --- | --- |
| `model`、`prompt` | 公開モデルとシーンの説明 |
| `duration` | 希望する秒数。範囲はモデルに依存 |
| `size`、`resolution` | モデル固有の比率と解像度 |
| `generate_audio` | 対応モデルで音声も生成 |
| `image_urls`、`video_urls`、`audio_urls` | 対応する参照素材。上流からアクセス可能な URL を使用 |
| その他 | 上流拡張を保持し、検証は上流で実施 |

先頭・末尾フレーム、複数参照、長さの範囲、参照の組み合わせは共通仕様ではありません。Volcengine ネイティブの `/content_generation/tasks` は実装していません。

DMXAPI Seedance は引き続き `/v1/responses` と生成モデル、`seedance-2-0-get` などの補助モデルを使います。タスク ID と照会規則は `/v1/tasks` と異なります。[DMXAPI の設定と例](media-api-operations.md)を参照してください。

## 非同期タスクの作成と照会

APIMart 互換の画像・動画要求は、通常、次の従来形式で受付結果を返します。

```json
{"code":200,"data":[{"status":"submitted","task_id":"task_example"}]}
```

Grok Imagine 2.0 Ext や GPT Image 2.5 Ext などの画像モデルは、APIMart の新しい HTTP `202` 受付形式にも対応します。

```json
{"code":202,"data":{"id":"task_example","object":"generation.task","type":"image","status":"pending","progress":0,"poll_url":"/v1/tasks/task_example"}}
```

この上流形式を要求するには、プロバイダーのカスタムヘッダーに `X-APIMart-Response-Version: 2026-07-27` を設定します。TokenHub はクライアントのヘッダーを自動転送しません。HTTP `202` と受付構造を保持し、`data.id` を置き換え、既存の `poll_url` を対応する TokenHub パスに書き換えます。この相対パスをゲートウェイのオリジンに結合して照会してください。通常の同期画像のアセット ID は変更しません。

どちらの受付形式も上流が要求を受け付けたことを示し、生成完了を意味しません。TokenHub はベンダーのタスク ID を不透明な ID に置き換え、送信プロジェクト・キー・プロバイダー・リソースの関連を永続化します。**送信時と同じプロジェクト API キー**と返された ID を使ってください。

```bash
export TOKENHUB_TASK_ID='TASK_ID_FROM_SUBMISSION'
curl --fail-with-body "$TOKENHUB_URL/v1/tasks/$TOKENHUB_TASK_ID?language=en" \
  -H "Authorization: Bearer $TOKENHUB_API_KEY"
```

任意の `language` クエリは `en`、`zh`、`ja`、`ko`、`ru`、`fr`、`de`、`id`、`pt`、`es` に対応し、上流のタスクエラーテキストの言語を選びます。`language` の重複や他のクエリは `400 invalid_task_query` になります。

| 応答フィールド | 意味 |
| --- | --- |
| `data.id` | この照会フローの TokenHub タスク ID |
| `data.status` | 上流タスクの状態 |
| `data.progress` | 任意の上流進捗。ゲートウェイの推定値ではない |
| `data.result` | モデル固有の結果。APIMart は `images[]` や `videos[]` 内の `url` 配列などを返す |
| `data.error` | 存在する場合は上流タスクの失敗情報 |
| `data.cost`、`data.credits_cost` | 上流メタデータ。TokenHub の請求額ではない |

`pending`/`processing` は総タイムアウトと間隔を設け、例えば 3–5 秒からバックオフして照会します。`completed` は成功、`failed` と `cancelled` は終了した失敗です。他の進行中状態は上流仕様に従います。URL は速やかに保存し、`expires_at` を確認します。HTTP 200 でも失敗タスクを返すため、HTTP ステータスと `data.status` の両方を確認してください。

照会は元のルート・アカウントを使用し、別アカウントへフェイルオーバーしません。キーは有効で、モデル権限を維持している必要があります。同じプロジェクト内でも別キーからの照会はできず、任意の上流 ID も受け付けません。ルートやプロバイダーの変更によって既存タスクを照会できなくなる場合があります。生成は上流が実行し、TokenHub はワーカー、キャンセル、完了 webhook サービスを追加しません。TokenHub の外で設定したプロバイダーのコールバックは、そのプロバイダーの ID と仕様を使います。

関連付けは**送信から 30 日後**に期限切れとなり、上流素材 URL の有効期限とは独立しています。期限切れ・不明・権限のないタスクは `404 media_task_not_found` になります。プロジェクトキーのローテーションで旧タスクが新キーへ移ることはありません。元の上流認証情報、URL、ヘッダー、その他のアカウント設定を変更すると、同じモデルを使えるアカウントでも `503 media_task_route_unavailable` になります。関連付けの保存に失敗した送信は `503 media_task_binding_unavailable` を返しますが、上流は既に生成を受け付けている可能性があるため、再送前にログを確認してください。

### 動画の継続・再生成

既存タスクを参照できる動画モデルでは、該当するトップレベルのフィールドに**元の送信で返された TokenHub タスク ID**を指定します。

| フィールド | 上流操作の例 |
| --- | --- |
| `draft_task_id` | Seedance 2.5 のドラフトから最終動画を生成 |
| `draft_from_task_id` | FLUX.3 のドラフトから最終動画を生成 |
| `extend_from_task_id` | Gemini Omni または PixVerse の動画延長 |
| `source_task_id` | 別の対象モデルを使った MiniMax H3 の再生成 |

これらのフィールドは `POST /v1/videos/generations` でのみ認識します。例えば `{"model":"followup-video","source_task_id":"task_example"}` は、管理者が公開した互換再生成モデルの別名を使用します。完了状態、有効期間、解像度、パラメーターの制限は引き続き上流が検証し、すべてのモデルがすべてのフィールドに対応するわけではありません。プロンプト、素材 URL、入れ子の拡張識別子はタスク参照として扱いません。

同じプロジェクト API キーを使用し、元のモデルと対象モデルの両方への権限を維持して、関連付けの 30 日間の有効期限内に参照します。不正・不明・期限切れ・別キー所有のタスクや直接指定した上流タスク ID は、生成前に `404 media_task_not_found` を返します。TokenHub は許可された ID を上流 ID に解決し、元のプロバイダーアカウント・リソースを使用します。そのアカウントが後続操作に対応していれば、対象の別名、ルート、上流モデルは異なっていても構いません。TokenHub は別のアカウントに切り替えません。認証情報や設定などの変更でアカウントのフィンガープリントが変わると `503 media_task_route_unavailable` を返します。元のアカウントが利用できない場合には、通常のルーティング、ポリシー、容量制限のエラーも適用されます。

複数の参照が異なるアカウントを指す場合は `400 invalid_media_task_reference` になります。それ以外の有効な参照の組み合わせをサポートするかどうかは上流が判断します。受け付けられた後続生成は新しいタスクを作成し、新たな上流料金が発生する場合があります。リクエストフックでは公開 ID を保持し、プロバイダー呼び出しフックには解決済みの上流 ID を渡します。

次の非同期処理を混同しないでください。

| 仕組み | ID と照会 |
| --- | --- |
| APIMart 互換画像・動画タスク | 返された `task_id`。`GET /v1/tasks/{task_id}` |
| TokenHub 管理対象画像ジョブ | [画像ガイド](user-guide.md#codex-サブスクリプション画像生成)の管理対象ジョブ仕様 |
| Responses の `background:true` | ローカル Responses ジョブ。完了は上流 API 呼び出しの終了であり、上流レンダリング完了ではない |

完了したバックグラウンドメディア Responses は、妥当な上流ルート ID を `x-tokenhub-upstream-response-id` に返します。プロバイダーの補助照会モデルへ渡す ID であり、`/v1/tasks` の ID ではありません。

## エラー・制限・課金

通常のゲートウェイエラーは以下の形式です。ネイティブ Anthropic/Gemini は各プロトコルのエラー形式を使います。

```json
{"error":{"message":"Model is not allowed for this API key","type":"model_not_allowed","code":"model_not_allowed"},"request_id":"REQUEST_ID"}
```

| HTTP ステータス | 主な原因 | 対応 |
| --- | --- | --- |
| `400` | JSON、multipart 制御フィールド、パラメーターが不正 | 選択モデルに合わせて修正 |
| `401` / `403` | キーが無効・停止・期限切れ、またはモデル権限なし | プロジェクトキーと権限を確認 |
| `404` | タスク・モデルが存在しないかアクセス不可 | ID と送信キーを確認 |
| `413` | 要求がサイズ上限を超過 | アップロードを縮小、または管理者が上限を調整 |
| `429` | レート・同時実行・クォータ・予算の制限 | エラーコードと再試行ヘッダーを確認 |
| `501` | ルートがプロトコル非対応 | 対応プロバイダー・ルートを設定 |
| `502` / `503` | 上流出力不正、ルートなし、プロバイダー失敗 | リクエスト ID でログを確認 |

直接メディア要求は `TOKENHUB_MAX_MULTIMODAL_REQUEST_BYTES` に従い、既定は 32 MiB です。multipart は最大 128 パート、テキストフィールドは各 1 MiB です。直接応答は最大 128 MiB までバッファリングします。管理対象画像は別の制限を持ち、上流の上限はさらに低い場合があります。multipart の `model` と `stream` は一度だけ指定し、JSON の `stream` は boolean にしてください。

1 回の送信で関連付けできるタスク ID は最大 128 件です。不正な ID や件数超過は `502 invalid_media_task` になりますが、上流処理が取り消されたことを意味しません。

認証、モデル許可、クォータ、レート制限、テキストポリシー、スコープ付きフック、保護された送信、ログ記録が適用されます。ファイルのバイト列はテキストポリシーでは解析しません。クライアント認証情報と Cookie は転送せず、設定済みの上流認証情報を使用します。結果不明の生成失敗は別ルートへ自動再送しません。タイムアウト後の無条件再試行は避けてください。課金対象の生成が上流で受け付けられている場合があります。

Token 計量は報告された使用量に基づき、バイナリ音声、字幕、使用量を返さない上流に架空の Token 料金を付けません。ポーリングは生成 Token を重複計上しません。時間で計量する文字起こしでは、公開モデルとプロバイダー在庫モデルのそれぞれに `metadata.audio_price_usd_per_second` の秒単価を設定します。小数秒を保持し、単価が未設定の場合は不明のままとし、Token 単価にはフォールバックしません。画像単位、文字単位、ベンダークレジットを Token 単価に自動変換しないため、未対応の単位は上流請求と照合してください。時間課金、Token 内訳のクォータ、フック、ストリーム検証の詳細は[メディアプロトコル運用](media-api-operations.md)にあります。

## 組み合わせ例

| 目的 | 手順 |
| --- | --- |
| 対話アシスタント | モデル一覧 → 会話送信 → ツール・推論の継続情報を保持 |
| 画像生成 | Images へ送信 → 同期結果を利用、または各タスク ID を照会 |
| ナレーション付き動画 | 音声生成 → 上流から音声を取得できるよう保存 → 音声参照対応モデルに動画要求 → 照会 → 素材保存 |
| 議事録 | 録音をアップロードして文字起こし → テキストを Chat/Responses に送信 |
| DMXAPI Seedance | 同一アカウントで生成・照会モデルを公開 → Responses へ送信 → 上流 ID で照会 |

回帰テストは合成データとローカルプロバイダーでプロトコル、アクセス分離、アップロード、フック、計量を検証します。有料生成や実アカウントでの全モデルの可用性は検証していません。参考：[APIMart テキスト](https://docs.apimart.ai/en/api-reference/texts/general/chat-completions.md)、[画像](https://docs.apimart.ai/en/api-reference/images/gpt-image-2/generation.md)、[音声](https://docs.apimart.ai/en/api-reference/audios/tts.md)、[Seedance](https://docs.apimart.ai/en/api-reference/videos/seedance-2-0/generation.md)、[タスク照会](https://docs.apimart.ai/en/api-reference/tasks/status.md)。
