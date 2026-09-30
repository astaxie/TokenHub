# テキスト埋め込み

`POST /v1/embeddings` は API キー権限、ルーティング、制限、監査を適用します。`model` と単一テキストまたは独立したテキスト配列を送信します。入力ごとに密ベクトルを返し、`index` を保持します。上流の件数、索引、次元が不正な場合はエラーです。

任意パラメータは `dimensions`、`encoding_format`（float/base64）、`input_type`（query/document）、`task`、`normalized`、`truncation`、`late_chunking`、`user` です。対応はプロトコルによって異なり、未対応の指定は拒否します。疎ベクトル、量子化、マルチモーダル、非同期 Batch は対象外です。互換上流では token ID 入力を利用できます。

ゲートウェイは 1 リクエスト最大 2048 入力を受け付けます。プロトコルによって上限がさらに低い場合があります。`dimensions` は 1–65536 で、`task` と `input_type` はどちらか一方だけ指定します。

## プロバイダー設定

詳細設定のテキスト埋め込み設定で `embedding_protocol` を選択します。`openai`、`cohere`、`jina`、`voyage`、`dashscope`、`tei` に対応し、Gemini は専用アダプターを使います。空欄はカタログ既定値または OpenAI 互換です。カスタム接続では実際のプロトコルを選択してください。

| プロトコル | Base URL の末尾例 | 既定パス | 注意点 |
| --- | --- | --- | --- |
| OpenAI 互換 | `/v1` | `/embeddings` | SiliconFlow、対応 vLLM/Xinference、Alibaba 互換 API など |
| Cohere | `/v2` | `/embed` | input_type または task が必要 |
| Voyage / Jina | `/v1` | `/embeddings` | タスクと次元のパラメータを変換 |
| DashScope | `/api/v1` | `/services/embeddings/text-embedding/text-embedding` | 密ベクトル、1 リクエスト最大 10 入力 |
| TEI | サーバールート | `/embed` | 未報告の使用量を生成しない |
| Gemini | Gemini 基本 URL | `:embedContent` / `:batchEmbedContents` | 独立テキストを結合しない |

`embedding_path` は Base URL に追加するパスを変更し、ホストや認証情報は変更しません。リージョン URL は公式資料で確認してください。カタログや接続テストだけでは全パラメータ対応を保証できません。ローカルプロトコルテストと実アカウント・モデル検証は区別します。

## ベクトル空間

既定では Provider、上流モデル、アダプター種別、実効 Base URL、Embedding プロトコルとパスが一致する必要があります。リソース上書きも反映し、異なる配備は共有空間の明示確認が必要です。互換性確認済みの配備には `embedding_spaces` にモデル ID と空間 ID の JSON 対応表（例 `{"my-embedding-model":"space-v1"}`）を設定できます。モデルのバージョンと符号化動作の互換性を確認してください。同じ次元数だけでは不十分です。空間変更時はアプリケーションで索引を再構築します。TokenHub は外部索引を変更しません。

計量証拠では未報告とゼロを区別し、推定を実測に置き換えません。上流コストとテナント料金は別設定です。

新規または再公開するルートでは、テナントと Provider の双方に料金設定が必要です。テナントは埋め込み料金、上流インベントリは入力料金を使用します。無料 Token 料金を明示的に確認するには、該当する公開モデルまたは上流モデルに `metadata.retrieval_pricing_confirmed="true"` を設定します。未設定の旧ゼロ値は無料の確認にはなりません。

```json
{"model":"public-embedding","input":["最初の文書","次の文書"],"dimensions":1024,"encoding_format":"float"}
```

OpenAI SDK は `client.embeddings.create` を使用します。Dify、LangChain/LlamaIndex では対応する統合に公開モデル名と TokenHub URL を設定してください。実際のクライアントバージョンの受け入れ検証は別途必要です。

モデル検出は明示された type、modality、model_type を優先します。未指定の場合は BGE、GTE、E5、Voyage、sentence-transformer の既知の名前を認識し、再ランキング名を優先して分類します。名前からの推定は配備能力の検証ではありません。

キャッシュ照会やルーティング前に、有効なルート定義、リソースの上書き、Provider へのフォールバック候補が同じ空間か確認します。不健全・クールダウン中のノードも対象です。健康状態や重み付き順序で空間は変わりません。競合は `409 embedding_space_conflict` を返します。非互換ルートを削除するか、互換性確認後に同じ空間 ID を設定してください。

フックはテキストを書き換えられますが、入力数、次元、符号化、タスクの意味を保持する必要があります。キャッシュ・プラグイン・後処理の出力は元の契約で検証します。キャッシュプラグインには空間・要求・呼び出し元に結び付くホスト生成 cache_key を渡します。ヒット時は保存済みキーを cache_key 書き込みで返し、欠落・不一致はミスとして扱います。書き込み側にも同じキーを渡すため、既存プラグインは対応が必要です。

公開モデルは embedding 型で、フック実行前に Embedding 料金または無料の明示確認が必要です。フックはテキスト/token 入力モードや token ID を変更できません。token 使用量が未報告の場合、TPM 精算は割当制御に限り受付時の推定量を維持し、課金証跡は未報告のままです。

モデル一覧はチャット入力料金ではなく Embedding 料金を表示します。在庫の検索モデル料金はコンパクトに編集でき、非表示のチャット・キャッシュ料金は保存時も維持します。リクエスト詳細はレスポンスを優先し、リクエストへの切替やメタデータ・使用量内訳の展開が可能です。

無料と明示確認した Embedding は旧チャット入力単価やアダプターの報告費用が残っていてもテナント料金をゼロに保ち、上流原価と token 数は維持します。一覧では確認済み無料をゼロ、未設定を不明として表示します。識別変更により旧キャッシュは無効になり、DB 移行は不要です。

token ID 入力の TPM はバッチを含む ID 数の合計で予約します。全体のルーティング前フックはキャッシュ識別前にテキストを変更できますが、ルート単位の request_transform は埋め込み入力を変更できません。旧契約のキャッシュは名前空間変更で無効になります。実行前に非 mock ルートの上流在庫とテキスト能力を再検証し、公開後の設定変更も反映します。

## ルート拒否の診断

実行時のフィルタリングですべての候補が除外された場合、互換性のため HTTP `501` と `provider_capability_not_supported` を維持します。API 応答とリクエストログに保存される応答には、対処方法を示す `error.message` と `error.details` が含まれます。詳細は `stage="route_selection"`、`upstream_attempted=false`、`reasons` 配列です。各要素は原因 `code`、対処方法 `message`、候補ルート数 `route_count` を持ちます。この数は上流への試行回数ではありません。候補ごとに最初の拒否理由を報告し、複数の理由はコード順に並べます。Provider の識別子、URL、認証情報、具体的な原価は公開しません。

| 原因コード | 確認事項 |
| --- | --- |
| `provider_capability_or_protocol_unsupported` | Provider アダプターと Embedding プロトコルの対応状況 |
| `tenant_price_not_configured` | 公開モデルの Embedding 料金、または明示的な無料確認 |
| `upstream_model_inventory_missing` | ルートの上流モデル名に一致する在庫エントリー |
| `upstream_model_modality_mismatch` | 在庫モデルの種別が `embedding` であること |
| `upstream_text_input_unsupported` | 在庫モデルのテキスト入力対応 |

この場合、上流リクエストは送信されていないため、ルート試行一覧が空なのは正常です。`/embeddings` の入力では在庫やテナント料金の問題は解決しません。起動時に既存ルートから在庫を補完する場合がありますが、補完した未確認のゼロ原価は既存ルートを停止させません。それ以前の公開モデルの受け入れ検証は引き続き `400 embedding_model_not_configured` を返し、ベクトル空間の競合なども既存のエラーを維持します。一部のみが拒否された場合は、有効な候補を通常どおり実行します。

## ローカル Embedding ルートのアップグレード

公開済みルートは、上流の入力原価がゼロまたは未確認であっても呼び出せます。これにより v0.8 の自己ホストモデルの動作を維持し、未知の原価を無料と見なしません。使用量とテナント料金は引き続き記録し、欠落した上流原価の証拠は計量で未確定のままとします。管理者は Provider 在庫で実際の原価や明示的なゼロ原価を保存できます。新規ルートの公開では双方の料金設定が引き続き必要です。モデル種別、テキスト対応、権限、ベクトル空間の検証は維持します。

ローカル OpenAI 互換エンドポイントが `http://model-host:8000/v1/embeddings` の場合、Base URL は `http://model-host:8000/v1`、Embedding プロトコルは自動または `openai`、パスは空欄または `/embeddings` にします。パスは Base URL に追加する値であり、完全な URL ではありません。データベースの書き換えや無料料金の自動確認は行いません。
