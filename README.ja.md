<p align="center">
  <img src="frontend/public/brand/tokenhub-logo.png" alt="TokenHub" width="96" />
</p>

<h1 align="center">TokenHub</h1>

<p align="center">
  TokenHub は、企業の AI モデル接続とガバナンスを一元化し、すべてのリクエストを制御・追跡し、利用主体を特定できるプライベートゲートウェイです。
</p>

<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="License" /></a>
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white" alt="Go 1.26" />
  <img src="https://img.shields.io/badge/Docker-Compose-2496ED?logo=docker&logoColor=white" alt="Docker Compose" />
  <img src="https://img.shields.io/badge/OpenAI-Compatible-10A37F" alt="OpenAI Compatible" />
</p>

<p align="center">
  <a href="README.md">English</a> | <a href="README.zh-CN.md">简体中文</a> | 日本語
</p>

## 対応 Provider

> [!TIP]
> **Codex サブスクリプションに対応：**OpenAI Codex のサブスクリプションアカウントを TokenHub に接続し、API Provider と同じ統合ゲートウェイでモデルを提供・管理できます。[Codex 接続ガイド →](docs/ja/codex-tokenhub-profile-quick-start.md)

TokenHub は、Codex サブスクリプション、OpenAI、Azure OpenAI、Anthropic、Gemini、DeepSeek、Qwen、ローカルモデル向けのネイティブアダプターに加え、150 以上の Provider テンプレートとカスタム OpenAI-Compatible 上流接続を備えています。主な接続先：

<table>
  <tr>
    <td width="20%" align="center" bgcolor="#ffffff"><a href="docs/ja/codex-tokenhub-profile-quick-start.md"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/openai.svg" alt="Codex サブスクリプション" width="32" height="32"></a><br><sub><strong><a href="docs/ja/codex-tokenhub-profile-quick-start.md">Codex サブスクリプション</a></strong></sub></td>
    <td width="20%" align="center" bgcolor="#ffffff"><a href="https://platform.openai.com/docs/models"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/openai.svg" alt="OpenAI" width="32" height="32"></a><br><sub><strong><a href="https://platform.openai.com/docs/models">OpenAI</a></strong></sub></td>
    <td width="20%" align="center" bgcolor="#ffffff"><a href="https://docs.anthropic.com/en/docs/about-claude/models"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/anthropic.svg" alt="Anthropic" width="32" height="32"></a><br><sub><strong><a href="https://docs.anthropic.com/en/docs/about-claude/models">Anthropic</a></strong></sub></td>
    <td width="20%" align="center" bgcolor="#ffffff"><a href="https://ai.google.dev/gemini-api/docs/models"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/gemini-color.svg" alt="Google Gemini" width="32" height="32"></a><br><sub><strong><a href="https://ai.google.dev/gemini-api/docs/models">Google Gemini</a></strong></sub></td>
    <td width="20%" align="center" bgcolor="#ffffff"><a href="https://learn.microsoft.com/azure/ai-foundry/openai/concepts/models"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/azure-color.svg" alt="Azure OpenAI" width="32" height="32"></a><br><sub><strong><a href="https://learn.microsoft.com/azure/ai-foundry/openai/concepts/models">Azure OpenAI</a></strong></sub></td>
  </tr>
  <tr>
    <td align="center" bgcolor="#ffffff"><a href="https://docs.aws.amazon.com/bedrock/latest/userguide/models-supported.html"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/bedrock-color.svg" alt="Amazon Bedrock" width="32" height="32"></a><br><sub><strong><a href="https://docs.aws.amazon.com/bedrock/latest/userguide/models-supported.html">Amazon Bedrock</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://cloud.google.com/vertex-ai/generative-ai/docs/models"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/vertexai-color.svg" alt="Google Vertex AI" width="32" height="32"></a><br><sub><strong><a href="https://cloud.google.com/vertex-ai/generative-ai/docs/models">Google Vertex AI</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://docs.x.ai/docs/models"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/grok.svg" alt="xAI Grok" width="32" height="32"></a><br><sub><strong><a href="https://docs.x.ai/docs/models">xAI / Grok</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://api-docs.deepseek.com"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/deepseek-color.svg" alt="DeepSeek" width="32" height="32"></a><br><sub><strong><a href="https://api-docs.deepseek.com">DeepSeek</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://www.alibabacloud.com/help/en/model-studio/models"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/qwen-color.svg" alt="Qwen DashScope" width="32" height="32"></a><br><sub><strong><a href="https://www.alibabacloud.com/help/en/model-studio/models">Qwen / DashScope</a></strong></sub></td>
  </tr>
  <tr>
    <td align="center" bgcolor="#ffffff"><a href="https://platform.moonshot.cn/docs/api/chat"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/moonshot.svg" alt="Moonshot AI Kimi" width="32" height="32"></a><br><sub><strong><a href="https://platform.moonshot.cn/docs/api/chat">Moonshot AI / Kimi</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://docs.z.ai"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/zhipu-color.svg" alt="Z.AI GLM" width="32" height="32"></a><br><sub><strong><a href="https://docs.z.ai">Z.AI / GLM</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://platform.minimax.io/docs"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/minimax-color.svg" alt="MiniMax" width="32" height="32"></a><br><sub><strong><a href="https://platform.minimax.io/docs">MiniMax</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://www.volcengine.com/docs/82379/1330310"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/doubao-color.svg" alt="Doubao" width="32" height="32"></a><br><sub><strong><a href="https://www.volcengine.com/docs/82379/1330310">Doubao</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://cloud.siliconflow.com/models"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/siliconcloud-color.svg" alt="SiliconFlow" width="32" height="32"></a><br><sub><strong><a href="https://cloud.siliconflow.com/models">SiliconFlow</a></strong></sub></td>
  </tr>
  <tr>
    <td align="center" bgcolor="#ffffff"><a href="https://modelscope.cn/docs/model-service/API-Inference/intro"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/modelscope-color.svg" alt="ModelScope" width="32" height="32"></a><br><sub><strong><a href="https://modelscope.cn/docs/model-service/API-Inference/intro">ModelScope</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://openrouter.ai/docs"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/openrouter-color.svg" alt="OpenRouter" width="32" height="32"></a><br><sub><strong><a href="https://openrouter.ai/docs">OpenRouter</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://console.groq.com/docs/models"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/groq.svg" alt="Groq" width="32" height="32"></a><br><sub><strong><a href="https://console.groq.com/docs/models">Groq</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://docs.together.ai/docs/serverless-models"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/together-color.svg" alt="Together AI" width="32" height="32"></a><br><sub><strong><a href="https://docs.together.ai/docs/serverless-models">Together AI</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://fireworks.ai/docs"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/fireworks-color.svg" alt="Fireworks AI" width="32" height="32"></a><br><sub><strong><a href="https://fireworks.ai/docs">Fireworks AI</a></strong></sub></td>
  </tr>
  <tr>
    <td align="center" bgcolor="#ffffff"><a href="https://docs.mistral.ai/getting-started/models"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/mistral-color.svg" alt="Mistral AI" width="32" height="32"></a><br><sub><strong><a href="https://docs.mistral.ai/getting-started/models">Mistral AI</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://docs.cohere.com/docs/models"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/cohere-color.svg" alt="Cohere" width="32" height="32"></a><br><sub><strong><a href="https://docs.cohere.com/docs/models">Cohere</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://docs.perplexity.ai"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/perplexity-color.svg" alt="Perplexity" width="32" height="32"></a><br><sub><strong><a href="https://docs.perplexity.ai">Perplexity</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://huggingface.co/docs/inference-providers"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/huggingface-color.svg" alt="Hugging Face" width="32" height="32"></a><br><sub><strong><a href="https://huggingface.co/docs/inference-providers">Hugging Face</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://docs.api.nvidia.com/nim"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/nvidia-color.svg" alt="NVIDIA NIM" width="32" height="32"></a><br><sub><strong><a href="https://docs.api.nvidia.com/nim">NVIDIA NIM</a></strong></sub></td>
  </tr>
  <tr>
    <td align="center" bgcolor="#ffffff"><a href="https://docs.github.com/en/github-models"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/github.svg" alt="GitHub Models" width="32" height="32"></a><br><sub><strong><a href="https://docs.github.com/en/github-models">GitHub Models</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://docs.github.com/en/copilot"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/githubcopilot.svg" alt="GitHub Copilot" width="32" height="32"></a><br><sub><strong><a href="https://docs.github.com/en/copilot">GitHub Copilot</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://vercel.com/docs/ai-gateway"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/vercel.svg" alt="Vercel AI Gateway" width="32" height="32"></a><br><sub><strong><a href="https://vercel.com/docs/ai-gateway">Vercel AI Gateway</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://developers.cloudflare.com/ai-gateway"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/cloudflare-color.svg" alt="Cloudflare AI Gateway" width="32" height="32"></a><br><sub><strong><a href="https://developers.cloudflare.com/ai-gateway">Cloudflare AI Gateway</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://docs.ollama.com"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/ollama.svg" alt="Ollama" width="32" height="32"></a><br><sub><strong><a href="https://docs.ollama.com">Ollama</a></strong></sub></td>
  </tr>
  <tr>
    <td align="center" bgcolor="#ffffff"><a href="https://lmstudio.ai/docs"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/lmstudio.svg" alt="LM Studio" width="32" height="32"></a><br><sub><strong><a href="https://lmstudio.ai/docs">LM Studio</a></strong></sub></td>
    <td align="center" bgcolor="#ffffff"><a href="https://docs.vllm.ai/en/latest/serving/openai_compatible_server.html"><img src="https://unpkg.com/@lobehub/icons-static-svg@1.94.0/icons/vllm-color.svg" alt="vLLM とカスタム Provider" width="32" height="32"></a><br><sub><strong><a href="https://docs.vllm.ai/en/latest/serving/openai_compatible_server.html">vLLM / カスタム</a></strong></sub></td>
  </tr>
</table>

Provider テンプレートは、利用可能な場合は対応するネイティブアダプターを使用し、それ以外は OpenAI-Compatible エンドポイントへ接続します。利用可能なモデルと機能は、上流サービスおよびアカウントによって異なります。

## スクリーンショット

<p align="center">
  <img src="docs/assets/screenshots/tokenhub-tour.webp" alt="TokenHub 製品ツアー：ログイン、概要、API ドキュメント、Provider チャネル、モデルカタログ、ルーティングポリシー、利用分析、システム設定" width="100%">
</p>

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
- SQLite-first のプライベートデプロイ向けに、ネイティブ systemd と Docker Compose の両方をサポート。
- PostgreSQL はマルチインスタンス構成に対応します。リモート PostgreSQL で状態を共有し、フロントエンドとバックエンドのレプリカを水平スケールできるほか、コネクションプールも設定できます。[デプロイガイド](docs/ja/deployment.md)を参照してください。
- 管理コンソールは英語、中国語、日本語の切り替えに対応。
- TokenHub は OpenAI Codex のサブスクリプションアカウントリソースにも接続できます。分離および復旧が可能な Codex Profile を使用し、指定したローカル Codex CLI またはデスクトップセッションを TokenHub 経由で実行できます。[Codex 接続ガイド](docs/ja/codex-tokenhub-profile-quick-start.md)を参照してください。
- Gemini CLI は TokenHub の Gemini ネイティブ API に直接接続し、Codex サブスクリプションアカウントの GPT モデルを CCswitch なしで使用できます。[Gemini CLI 接続ガイド](docs/ja/gemini-cli-codex-subscription.md)を参照してください。

## クイックスタート

Linux systemd ホストでネイティブ Release を使用する場合:

```bash
curl -fsSL https://raw.githubusercontent.com/astaxie/TokenHub/main/deploy/native/install.sh \
  -o /tmp/tokenhub-install.sh
sudo bash /tmp/tokenhub-install.sh install
```

リポジトリのチェックアウトから Docker Compose を使用する場合:

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
- ネイティブインストールのパスワード: インストーラーが一度だけ表示
- Docker のパスワード: `TOKENHUB_BOOTSTRAP_ADMIN_PASSWORD` の設定値

ネイティブインストーラーは Release のチェックサムを検証し、systemd サービスをインストールして、バージョンパネルから直接更新、ロールバック、再起動できるようにします。デフォルトの Docker デプロイは、バックエンドと管理コンソールを 1 つの管理対象コンテナで実行し、Docker Socket をマウントせずに同じ直接操作を提供します。Release バンドルは `tokenhub-releases` ボリュームへ保存されるため、通常のコンテナ再起動や再作成でも画面から適用した更新は保持されます。マルチインスタンス Docker では、全レプリカを同時に切り替えるため Compose による運用更新を維持します。詳細は[デプロイガイド](docs/ja/deployment.md)を参照してください。

## ドキュメント

- [ドキュメントホーム](docs/ja/README.md)
- [全体アーキテクチャ](docs/ja/architecture.md)
- [ユーザーガイド](docs/ja/user-guide.md)
- [チームリーダーガイド](docs/ja/team-leader-guide.md)
- [管理者ガイド](docs/ja/administrator-guide.md)
- [コントリビューションガイド](CONTRIBUTING.ja.md)

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

<a href="https://www.star-history.com/?repos=astaxie%2Ftokenhub&type=date&legend=top-left">
 <picture>
   <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/chart?repos=astaxie/tokenhub&type=date&theme=dark&legend=top-left&sealed_token=hWH3kDnssTf49CCLxzq3NVqEp0WTL-HFhsdpQJJz1DUuZt0D-nu1jgXLnhCxrUrMYujv6IJJk12B1wCp5qiU2bU_J03ECSYvb3Y9Pv-gqX7RuwS4SehRrQ" />
   <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/chart?repos=astaxie/tokenhub&type=date&legend=top-left&sealed_token=hWH3kDnssTf49CCLxzq3NVqEp0WTL-HFhsdpQJJz1DUuZt0D-nu1jgXLnhCxrUrMYujv6IJJk12B1wCp5qiU2bU_J03ECSYvb3Y9Pv-gqX7RuwS4SehRrQ" />
   <img alt="Star History Chart" src="https://api.star-history.com/chart?repos=astaxie/tokenhub&type=date&legend=top-left&sealed_token=hWH3kDnssTf49CCLxzq3NVqEp0WTL-HFhsdpQJJz1DUuZt0D-nu1jgXLnhCxrUrMYujv6IJJk12B1wCp5qiU2bU_J03ECSYvb3Y9Pv-gqX7RuwS4SehRrQ" />
 </picture>
</a>

## License

TokenHub は [Apache License 2.0](LICENSE) の下で提供されています。
