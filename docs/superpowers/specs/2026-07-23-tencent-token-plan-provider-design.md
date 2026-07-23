# 腾讯云 Token Plan Provider 接入设计

## 目标

在 Provider 创建向导中预置腾讯云 Token Plan 的四个套餐类型：企业版专业套餐、企业版轻享套餐、通用 Token Plan 个人版和 Hy Token Plan 个人版。所有套餐均使用 OpenAI-compatible 协议。个人套餐在保存上游 Key 前必须由管理员确认风险，确认结果将写入 Provider 配置并记录管理审计事件。

## 范围

- 新增四个内置 Provider Catalog 条目。企业版使用 `https://tokenhub.tencentmaas.com/plan/v3`，个人版使用 `https://api.lkeap.cloud.tencent.com/plan/v3`。
- 为 Catalog 条目增加可选的确认声明字段：标题、内容和是否需要确认。
- Provider 创建请求增加 `acknowledged_catalog_terms`。当所选 Catalog 需要确认且该字段不是 `true` 时，服务端返回 `provider_terms_acknowledgement_required`。
- 成功创建个人套餐 Provider 时，把套餐类型、确认时间和确认声明版本保存到 Provider `options`，并包含在既有的 `create provider` 审计事件中。
- 前端在个人套餐选中并进入“配置账号与凭据”步骤时展示风险提示和复选框；未确认时禁用下一步与保存。提交请求会携带确认字段。
- 增加后端 Catalog、确认校验和审计持久化测试。

## 套餐配置

| Catalog ID | 名称 | 模型 |
| --- | --- | --- |
| `tencent-token-plan-enterprise-pro` | 腾讯云 Token Plan / 企业版专业套餐 | `auto`、GLM、Kimi、MiniMax、DeepSeek 系列 |
| `tencent-token-plan-enterprise-auto` | 腾讯云 Token Plan / 企业版轻享套餐 | `auto` |
| `tencent-token-plan-general-personal` | 腾讯云 Token Plan / 通用 Token Plan（个人版） | `tc-code-latest`、GLM、Kimi、MiniMax、DeepSeek 系列 |
| `tencent-token-plan-hy-personal` | 腾讯云 Token Plan / Hy Token Plan（个人版） | `hy3`、`hy3-preview` |

模型 ID 采用腾讯云当前文档中的小写形式。目录仅提供创建时的可映射模型；现有标准模型目录不匹配的模型不会自动生成路由，管理员可在后续路由配置中决定是否新增统一模型名。

## 确认规则

企业套餐不需要额外确认。个人套餐提示管理员：套餐可能受腾讯云关于账号共享、API 调用方式和使用场景的限制，管理员应在录入 Key 前自行确认其适用性。提示不阻止管理员继续，但确认是创建的必填条件。

服务端必须执行同一校验，避免只通过调用管理 API 就绕过界面。编辑已创建的 Provider 不重复要求确认；只有新建个人套餐 Provider 需要确认。

## 流程

1. 管理员在 Provider 创建向导选择腾讯云套餐。
2. 向导加载目录模板、模型和基础地址。
3. 选择个人套餐时，凭据步骤展示确认框；管理员勾选后才可继续或保存。
4. 前端提交 `catalog_id`、`acknowledged_catalog_terms` 和正常的 Provider 配置。
5. 后端重新加载 Catalog，验证确认状态，并把确认元数据写入 Provider options。
6. 既有加密存储、路由创建、请求日志和审计事件照常生效。

## 测试

- `/api/admin/provider-catalog` 返回四个腾讯云条目及正确的模型、地址和个人套餐确认元数据。
- 创建个人套餐 Provider 且未确认时返回 400 和专用错误码。
- 创建个人套餐 Provider 且已确认时返回 201，并可读取确认元数据。
- 企业套餐在未提供确认字段时仍能创建。
- 现有 Catalog 自动路由行为不回归。

## 非目标

- 不验证腾讯云 API Key 真伪，也不代表用户判断套餐合同或合规性。
- 不自动同步腾讯云套餐余额、套餐到期时间或管理面 API。
- 不为标准目录中不存在的腾讯云模型自动创建新的公开模型。
