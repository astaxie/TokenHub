# 画像・音声・動画・音楽 API

言語：[English](../media-apis.md) | [简体中文](../zh-CN/media-apis.md) | 日本語

TokenHub は [DMXAPI ドキュメント](https://doc.dmxapi.cn/jichu.html)で使用されるメディアプロトコルに対応します。プロバイダーを設定し、生成、アップロード、照会、ダウンロードの各モデルを公開してから呼び出してください。対応範囲はプロトコル互換性です。モデルの提供状況とパラメーターの仕様は上流プロバイダーに依存し、認証情報や料金は同梱しません。

## エンドポイント

| エンドポイント | 用途と例 |
| --- | --- |
| `POST /v1/images/generations` | GPT Image、Seedream/即夢、Qwen Image。独自パラメーター、参照画像、複数結果、URL、base64 を保持 |
| `POST /v1/images/edits` | JSON または multipart による画像編集。マスクとファイルを保持 |
| `POST /v1/images/variations` | 互換プロバイダーによる画像バリエーション |
| `POST /v1/audio/speech` | MiniMax speech-2.6 などの音声合成。上流の音声バイト列を返す |
| `POST /v1/audio/transcriptions` | gpt-4o-transcribe などの multipart 音声認識。JSON、テキスト、SRT、VTT |
| `POST /v1/audio/translations` | 互換プロバイダーによる音声翻訳 |
| `POST /v1/responses` | Seedance、Hailuo、Kling、Vidu、PixVerse/Paiwo、Wan、HappyHorse 動画。Seedream、Wan、Qwen、Agnes、SciDraw 画像。MiniMax 音声アップロード/クローン、高度な音声合成、音楽/歌詞、Mureka |
| `POST /v1/chat/completions` | MiMo 音声合成/声の設計/クローン、Qwen Omni 音声説明とマルチモーダル音声、Recraft 画像 |
| `POST /v1beta/models/{model}:generateContent` と `:streamGenerateContent` | Gemini ネイティブ画像生成/編集とマルチモーダル出力 |

各モデルの上流ドキュメントで指定されたエンドポイントを使用します。DMXAPI の動画例は `/v1/responses` で作成と照会を行うため、新たな `/v1/videos` は不要です。既存の Chat、Responses、Gemini API のストリーミングと拡張フィールドは維持されます。

## プロバイダーとモデルの設定

1. **OpenAI-compatible** プロバイダーに `https://www.dmxapi.cn/v1` と上流の認証情報を設定します。Gemini ネイティブの例では **Gemini** プロバイダーを使い、ドキュメントのベース URL と同じ上流アカウントを設定します。
2. 公開モデルのルートを正確な上流モデル ID にマッピングします。モダリティは `image`、`video`、`audio`（音楽は `audio`）を選びます。マルチモーダルチャットではこれらの出力モダリティを設定することもできます。該当する Chat/Responses 要求は応答キャッシュを使用せず、生成結果の再利用や古いタスク状態を防ぎます。
3. `seedance-2-0-get`、`MiniMax-Hailuo-query`、`MiniMax-Hailuo-get`、音声/素材アップロードなどの補助モデルも公開します。プロジェクトキーには処理全体で必要なモデルの権限を付与します。
4. 生成と補助モデルのルートを同じプロバイダーアカウント/リソースに固定します。タスク ID、ファイル ID、大きな JSON 整数はそのまま渡されます。TokenHub のローカルバックグラウンドジョブへの変換や、上流タスクの所有権の自動バインドは行いません。タスクの名前空間を分離する必要があるテナントには、別々の上流アカウントを使用してください。

管理対象の `gpt-image-2`、Codex サブスクリプション、プラグイン画像プロファイルは、従来の検証、単一画像ジョブ、保存アセット、`Prefer: respond-async` を維持します。同名の上流モデルで完全な Images 仕様を使用する場合は、別の公開エイリアス（例：`vendor-gpt-image`）をその上流 ID にマッピングします。通常の画像ルートはプロバイダーの結果を直接返します。ローカルジョブの照会と TokenHub の署名付き画像 URL は管理対象ジョブ専用です。

`/v1/responses` で `background:true` を指定すると、応答本文の `id` は TokenHub のローカル要求ジョブを識別します。完了したメディアジョブの照会応答では、`x-tokenhub-upstream-response-id` にプロバイダーの元のルート ID を返します。この ID を補助照会モデルに渡してください。ローカルジョブの完了は上流 API 呼び出しの終了を意味し、生成タスクは引き続きポーリングが必要な場合があります。文字列と数値の ID は正確に保持します。非スカラー ID、制御文字や前後の空白を含む値、2048 バイトを超える値ではヘッダーを省略します。

## 呼び出し例

環境変数 `TOKENHUB_API_KEY` にプロジェクトキーを設定します。以下のモデルは公開済みで、キーからのアクセスが許可されている必要があります。

```bash
curl https://tokenhub.example/v1/audio/speech \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"speech-2.6-hd","input":"Hello from TokenHub","voice":"male-qn-qingse","response_format":"mp3"}' \
  --output speech.mp3

curl https://tokenhub.example/v1/audio/transcriptions \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -F model=gpt-4o-transcribe -F file=@sample.wav

curl https://tokenhub.example/v1/responses \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"doubao-seedance-2-0-260128","input":[{"type":"text","text":"A calm ocean at sunrise"}],"duration":4,"resolution":"720p","generate_audio":true}'

# 返された id を保存し、上流が完了を報告するまで照会します。
curl https://tokenhub.example/v1/responses \
  -H "Authorization: Bearer $TOKENHUB_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"model":"seedance-2-0-get","input":"TASK_ID_FROM_SUBMISSION"}'
```

## 制限・計量・検証

新しい直接メディアルートには `TOKENHUB_MAX_MULTIMODAL_REQUEST_BYTES`（既定 32 MiB）が適用されます。multipart は最大 128 パート、テキストフィールドは各 1 MiB です。応答は最大 128 MiB までバッファリングしてから返し、上流の Content-Type を保持します。この経路は低遅延のチャンク配信を提供しません。管理対象画像の従来の制限は変更されず、応答内の URL を TokenHub がダウンロードすることもありません。

multipart の `model` と `stream` は単一のテキストフィールドである必要があります。JSON の `stream` は真偽値、multipart の `stream` は真偽値の文字列を指定します。曖昧な制御フィールドはルーティング前に拒否し、要求フックの変更にも同じ検証を適用します。テキストポリシーは、繰り返された multipart フィールドを含め、`input`、`instructions`、`prompt`、`negative_prompt`、`text` のすべての値を検査し、`timestamp_granularities[]` などの繰り返しオプションは保持します。

認証、モデル許可リスト、クォータ、スコープ付きルーティング、プロバイダーリソースの容量、テキスト事前ポリシー、応答フック、使用量の帰属が適用されます。要求フックには JSON/テキストフィールドが渡され、multipart ファイルのバイト列は不透明です。メディア Responses のテキストポリシーは、プロバイダー固有のプロンプト、歌詞、Wan のネストしたメッセージも検査し、不透明なアセット/タスク ID は保持します。直接メディアの監査にはモデル、機密内容を除いたポリシー判断、Content-Type、バイト数を記録し、アップロード/生成メディアは保存しません。クライアントの Cookie や認証ヘッダーを転送せず、設定済みのプロバイダー認証情報と保護された上流トランスポートを使用します。生成結果が不明なメディア要求の失敗時は、Chat/Responses のメディアモデルも含め、別ルートで自動的に再生成しません。明確な認証拒否やレート制限の場合は引き続きフェイルオーバーできます。

一致する `provider_call` フックは直接メディアアダプターより先に実行され、ルートの拒否、スキップ、処理を行えます。非ストリーミングフックはプロバイダーの JSON 応答、または文字列の `data_base64` と `content_type` を含むバイナリエンベロープを返します。ストリーミングフックは `stream_events` を返し、配信は引き続きバッファリングされます。バイナリやテキスト応答を処理する応答/ガードレールフックは、有効な `data_base64` を保持する必要があります。不正な変更はエラーとなり、空の成功応答にはなりません。128 MiB の応答上限はフック処理後にも適用し、シリアライズした JSON またはデコード後のバイナリのバイト数で判定します。上限を超える置換結果はエラーになりますが、報告済みの使用量は保持します。

SSE 応答では、バッファリングされた結果を配信する前に、各イベントへ `stream_transform`、`response_post`、`guardrail_post` を適用します。後続のストリームエラーやポリシーで配信が停止しても、上流が報告した Token 使用量は保持します。終端エラーは失敗として記録し、再生成を行わず、エラーメッセージ内のプロバイダー認証情報をマスクします。クライアントによるキャンセルはプロバイダーの正常性エラーに数えません。プラグインが生成した SSE にも同じエラーと使用量の検査を適用し、ゼロを含む明示的なプラグイン使用量はイベント内の使用量より優先します。使用量帰属フックが失敗しても元の上流使用量を保持します。直接メディア要求の HTTP 408 応答では自動再送しません。監査記録には機密内容を除いたポリシー判断を保持し、置換テキストやメディア内容は保存しません。

メディアモデルのストリーミング Responses API は、標準 Responses 完了イベント、Wan の `[DONE]` マーカー、MiniMax の `data.status=2` 完了フレームを認識します。トップレベルおよびネストされた Token 使用量を保持し、完了マーカーなしで終了したストリームは失敗として記録します。メディアイベントの上限は 1 件あたり 128 MiB です。テキストモデルの Responses は従来の完了判定を維持します。

Token 課金は上流が報告する使用量に基づきます。音声文字起こしの JSON および SSE 使用量では、`input_token_details.audio_tokens` に報告された音声 Token の内訳を保持します。バイナリ音声、字幕テキスト、Token を返さないプロバイダーに対して架空の Token コストを付与しません。要求数/同時実行制限と要求ログは有効です。秒数、画像数、文字数ベースの料金を Token 単価へ変換しません。Token を報告するモデルに料金を設定し、それ以外は上流の請求と照合してください。

回帰テストはローカル HTTP プロバイダーと合成データを使用し、アップロード、バイナリ/テキスト応答、画像拡張フィールド、非同期動画の要求形状、音声素材、大きな整数、権限拒否、フック、キャッシュ回避を検証します。有料の実生成は行わないため、個別プロバイダー/モデルの提供状況は実際のアカウントで確認してください。
