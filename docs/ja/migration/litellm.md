# LiteLLM アダプター

## 対応バージョン

LiteLLM ≥ 1.52.0、< 1.70.0。

## 入力

LiteLLM `proxy_config.yaml` ファイル（`model_list`、`key_management_settings` 等のセクションを含む）。

## マッピングルール

| LiteLLM 概念 | TokenHub リソース |
|-------------|-------------------|
| `model_list[].litellm_params.model` プレフィックス | Provider |
| `model_list[].model_name` | Model + Route |
| `key_management_settings.teams` | Team + Project |
| `key_management_settings.users` | User |
| `key_management_settings.virtual_keys` | API Key |

## 制限事項

- 履歴使用量/費用データは移行対象外。
- `budgets`、`router_settings` は部分的にのみマッピングされ、警告が生成されます。

詳細は `docs/migration/litellm.md` を参照してください。
