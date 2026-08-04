# LiteLLM 适配器

## 支持版本

LiteLLM ≥ 1.52.0，< 1.70.0。

## 输入

LiteLLM `proxy_config.yaml` 文件，包含 `model_list`、`key_management_settings` 等节。

## 映射规则

| LiteLLM 概念 | TokenHub 资源 |
|-------------|----------------|
| `model_list[].litellm_params.model` 前缀 | Provider |
| `model_list[].model_name` | Model + Route |
| `key_management_settings.teams` | Team + Project |
| `key_management_settings.users` | User |
| `key_management_settings.virtual_keys` | API Key |

## 限制

- 历史用量/花费数据不在迁移范围内。
- `budgets`、`router_settings` 仅部分映射，会生成警告。

详细说明请参阅 `docs/migration/litellm.md`。
