# Codex 生图 API 调用与测试指南

返回：[文档首页](README.md) | [普通用户指南](user-guide.md)

本文说明如何通过 TokenHub 的 OpenAI 兼容 Image API 调用 `codex-gpt-image-2` 和 `gpt-image-2`。

`codex-gpt-image-2` 是 TokenHub 对外暴露的 Codex 订阅虚拟模型。管理员可在 OpenAI Codex Provider 的“模型”页签勾选“Codex 订阅生图”，选择真实账号完成一次低质量生图测试；测试通过后，TokenHub 会自动创建或启用上游模型为 `gpt-image-2` 的线路。服务器随后从该线路覆盖的账号资源中选择已确认支持生图的 Codex 订阅账号，直接调用 Codex 订阅 Images 接口。服务器不需要安装或启动 Codex CLI。

`gpt-image-2` 通常是独立的 OpenAI API 模型，必须配置 `openai` 类型 Provider、API Key 和模型路由。它调用 Provider 的标准 `/v1/images/generations` 与 `/v1/images/edits`，不会选择 Codex 订阅账号或消耗 Codex 额度。唯一例外是带 Codex `originator` 或 `x-codex-image-turn-id` 请求头的 `/v1/images/generations` 请求：TokenHub 会将其映射为 `codex-gpt-image-2` 并返回 `b64_json`，API Key 必须允许 `codex-gpt-image-2`。

## 1. 协议概览

| 场景 | 方法 | Endpoint | Content-Type |
| --- | --- | --- | --- |
| 文生图 | `POST` | `/v1/images/generations` | `application/json` |
| 参考图编辑 | `POST` | `/v1/images/edits` | `multipart/form-data` |
| 查询异步任务 | `GET` | `/v1/image-jobs/{job_id}` | 无请求体 |
| 下载生成图片 | `GET` | 响应中返回的签名 URL | 无请求体 |
| 查询可用模型 | `GET` | `/v1/models` | 无请求体 |
| 管理员查询完整生图日志 | `GET` | `/api/admin/audit/image-jobs?limit=200` | Admin 鉴权 |

所有业务 Endpoint 使用 TokenHub API Key：

```http
Authorization: Bearer <TOKENHUB_API_KEY>
```

图片下载 URL 自带签名，不需要再次携带 API Key。签名 URL 有效期为 24 小时；服务器文件默认继续保留。URL 过期后，重新查询任务即可获得新的签名 URL。

管理员审计接口会返回数据库中解密后的完整提示词，以及输入图、输出图的新签名 URL；提示词在数据库中仍以密文保存。

## 2. TokenHub 与标准 Image API 的关系

TokenHub 保留了 OpenAI Image API 的主要调用形态：

- 文生图使用 `/v1/images/generations`
- 图片编辑使用 `/v1/images/edits`
- 使用 `model`、`prompt`、`n`、`quality`、`size` 和 `response_format`
- 编辑请求使用 multipart 上传一个或多个 `image` / `image[]`
- 同步响应使用 `data[].url` 或 `data[].b64_json`

TokenHub 当前扩展和限制如下：

| 能力 | TokenHub 当前行为 |
| --- | --- |
| 对外模型 | `codex-gpt-image-2`、`gpt-image-2` |
| 模型隔离 | 前者只使用 Codex 订阅账号；后者只使用 OpenAI API Provider |
| 单次图片数 | 仅支持 `n=1` |
| 异步调用 | 支持 `Prefer: respond-async` 或 `X-TokenHub-Async: true` |
| 异步任务查询 | 使用 `/v1/image-jobs/{job_id}` |
| 图片 URL | TokenHub 服务器签名 URL，有效期 24 小时 |
| 文件保存 | 输入图、输出图默认保留在 TokenHub 服务器 |
| 遮罩编辑 | `gpt-image-2` 支持；`codex-gpt-image-2` 上传 `mask` 返回 `501 image_mask_not_supported` |
| 流式局部图片 | 暂不支持 |
| 输出格式参数 | 对外暂不支持 `output_format`；OpenAI API 路由请求 PNG，Codex 路由保留上游返回的 PNG、JPEG 或 WebP |
| 幂等键 | 暂不支持；每次 `POST` 都会创建新任务 |

OpenAI 官方 Image API 还支持更多参数和模式。本文只描述 TokenHub 已实现并经过测试的协议。

## 3. 测试前准备

### 3.1 管理员测试并启用 Codex 生图

1. 打开控制台的“Provider 渠道”，编辑目标 OpenAI Codex Provider。
2. 进入“模型”页签，勾选“Codex 订阅生图”。
3. 选择一个健康、已启用的真实 Codex 订阅账号，并确认额度提示。
4. 等待“正在测试生图能力”完成。TokenHub 会发送一次 `quality: "low"`、`size: "1024x1024"` 的真实请求，这会消耗少量订阅额度。
5. 只有收到非空且可解析的图片后，系统才会记录“支持生图”并创建或启用线路。`403` 表示账号不支持生图；认证失效需要重新授权；限流、超时或上游临时故障可在弹窗中重试。

不需要先手工创建默认线路。自动创建的线路仍可在路由策略中调整优先级、权重、项目范围、指定资源和资源分组。取消勾选会停用匹配线路，但保留能力测试结果。升级已有环境时，TokenHub 只会为之前已经确认支持生图的启用账号补齐一次缺失线路，不会重新启用明确停用的线路，也不会反复创建管理员已经删除的线路。

### 3.2 设置环境变量

```bash
export TOKENHUB_BASE_URL="http://localhost:8080"
export TOKENHUB_API_KEY="你的 TokenHub API Key"
```

不要把真实 API Key 写入脚本、Git 或日志。

### 3.3 确认模型可用

```bash
curl -sS \
  -H "Authorization: Bearer ${TOKENHUB_API_KEY}" \
  "${TOKENHUB_BASE_URL}/v1/models" \
  | jq '.data[] | select(.id == "codex-gpt-image-2")'
```

预期能查询到：

```json
{
  "id": "codex-gpt-image-2",
  "object": "model",
  "owned_by": "tokenhub",
  "title": "Codex GPT Image 2"
}
```

如果没有结果，请依次检查：

1. 后端是否已经重启并加载最新模型目录。
2. OpenAI Codex Provider 的“模型”页签中，“Codex 订阅生图”是否已测试通过并勾选。
3. 是否存在从 `codex-gpt-image-2` 到该 Provider、上游模型为 `gpt-image-2` 的启用线路。
4. 该线路覆盖的账号中，是否至少有一个健康、已授权且标记为“支持生图”的 Codex 账号。
5. API Key 的模型白名单是否包含 `codex-gpt-image-2`。

### 3.4 使用 OpenAI API 额度

要改用普通 OpenAI API Provider，只需将下文同一协议中的模型改为：

```json
{
  "model": "gpt-image-2"
}
```

同时确保：

1. TokenHub 已配置健康启用的 `openai` 类型 Provider 和有效 API Key。
2. `gpt-image-2` 存在指向该 Provider 的启用模型路由。
3. TokenHub API Key 的模型白名单包含 `gpt-image-2`。

两种模型的异步任务、服务端文件保存、24 小时签名 URL、提示词日志和响应格式相同。

## 4. 最小同步文生图

同步请求会保持连接，直到生图完成或失败。复杂请求可能持续一分钟以上。

```bash
curl -sS \
  -X POST "${TOKENHUB_BASE_URL}/v1/images/generations" \
  -H "Authorization: Bearer ${TOKENHUB_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "codex-gpt-image-2",
    "prompt": "一只橙色虎斑猫坐在木质书桌前阅读一本蓝色封面的书，温暖自然光，写实摄影，无文字，无水印",
    "n": 1,
    "quality": "low",
    "size": "1024x1024",
    "response_format": "url"
  }' | tee /tmp/tokenhub-image-response.json | jq
```

成功时返回 `200 OK`，结构如下：

```json
{
  "created": 0,
  "job_id": "<IMAGE_JOB_ID>",
  "data": [
    {
      "asset_id": "<IMAGE_ASSET_ID>",
      "url": "<SIGNED_IMAGE_URL>"
    }
  ],
  "usage": {
    "input_tokens": 0,
    "output_tokens": 0,
    "total_tokens": 0
  }
}
```

`created` 和 `usage` 会返回本次真实任务的数据，上面的 `0` 只表示字段类型。

### 4.1 下载同步结果

```bash
IMAGE_URL="$(
  jq -r '.data[0].url' /tmp/tokenhub-image-response.json
)"

curl -fL "${IMAGE_URL}" -o /tmp/tokenhub-generated.png
file /tmp/tokenhub-generated.png
```

## 5. Base64 同步响应

当调用方不方便访问签名 URL 时，可以请求 `b64_json`：

```bash
curl -sS \
  -X POST "${TOKENHUB_BASE_URL}/v1/images/generations" \
  -H "Authorization: Bearer ${TOKENHUB_API_KEY}" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "codex-gpt-image-2",
    "prompt": "极简白色背景上的透明玻璃茶杯，柔和棚拍光线，产品摄影，无文字，无水印",
    "n": 1,
    "quality": "low",
    "size": "1024x1024",
    "response_format": "b64_json"
  }' \
  | jq -r '.data[0].b64_json' \
  | base64 --decode \
  > /tmp/tokenhub-generated-base64.png
```

macOS 自带 `base64` 如果不接受 `--decode`，使用：

```bash
base64 -D
```

同步 `b64_json` 会把整张图片放进 JSON，响应体可能很大。面向 Web 或移动端时，优先使用 URL 模式。

## 6. 推荐的异步文生图

异步模式适合生产环境，可以避免客户端连接长时间占用。

### 6.1 创建任务

```bash
curl -sS \
  -X POST "${TOKENHUB_BASE_URL}/v1/images/generations" \
  -H "Authorization: Bearer ${TOKENHUB_API_KEY}" \
  -H "Content-Type: application/json" \
  -H "Prefer: respond-async" \
  -d '{
    "model": "codex-gpt-image-2",
    "prompt": "未来感企业 AI 网关机房，深色金属机柜，青色和琥珀色数据流汇聚到中央晶体核心，电影级灯光，无人物，无水印",
    "n": 1,
    "quality": "low",
    "size": "1024x1024",
    "response_format": "url"
  }' | tee /tmp/tokenhub-image-job.json | jq
```

成功创建时返回 `202 Accepted`，并在响应头中提供：

```http
Location: /v1/image-jobs/<IMAGE_JOB_ID>
X-Request-ID: <IMAGE_JOB_ID>
```

响应体至少包含：

```json
{
  "id": "<IMAGE_JOB_ID>",
  "object": "image.job",
  "status": "queued",
  "model": "codex-gpt-image-2",
  "action": "generate",
  "created_at": 0
}
```

### 6.2 轮询任务

```bash
IMAGE_JOB_ID="$(
  jq -r '.id' /tmp/tokenhub-image-job.json
)"

while true; do
  JOB="$(
    curl -sS \
      -H "Authorization: Bearer ${TOKENHUB_API_KEY}" \
      "${TOKENHUB_BASE_URL}/v1/image-jobs/${IMAGE_JOB_ID}"
  )"

  STATUS="$(printf '%s' "${JOB}" | jq -r '.status')"
  printf 'status=%s\n' "${STATUS}"

  case "${STATUS}" in
    completed)
      printf '%s' "${JOB}" > /tmp/tokenhub-image-job-completed.json
      break
      ;;
    failed)
      printf '%s\n' "${JOB}" | jq '.error'
      exit 1
      ;;
    queued|running)
      sleep 2
      ;;
    *)
      printf '%s\n' "${JOB}" | jq
      exit 1
      ;;
  esac
done
```

完成后的任务结构包含：

```json
{
  "id": "<IMAGE_JOB_ID>",
  "object": "image.job",
  "status": "completed",
  "model": "codex-gpt-image-2",
  "action": "generate",
  "request_id": "<TOKENHUB_REQUEST_ID>",
  "usage": {
    "input_tokens": 0,
    "cached_input_tokens": 0,
    "output_tokens": 0,
    "total_tokens": 0
  },
  "data": [
    {
      "asset_id": "<IMAGE_ASSET_ID>",
      "url": "<SIGNED_IMAGE_URL>",
      "url_expires_at": 0,
      "content_type": "image/png",
      "bytes": 0,
      "sha256": "<SHA256>"
    }
  ]
}
```

### 6.3 下载异步结果

```bash
IMAGE_URL="$(
  jq -r '.data[0].url' /tmp/tokenhub-image-job-completed.json
)"

curl -fL "${IMAGE_URL}" -o /tmp/tokenhub-async-result.png
```

## 7. 参考图编辑

图片编辑请求必须使用 `multipart/form-data`。

支持的输入图片：

- PNG
- JPEG
- WebP
- 单张不超过 50 MB
- 整个 multipart 请求不超过 128 MB

### 7.1 单张参考图

```bash
curl -sS \
  -X POST "${TOKENHUB_BASE_URL}/v1/images/edits" \
  -H "Authorization: Bearer ${TOKENHUB_API_KEY}" \
  -F "model=codex-gpt-image-2" \
  -F "image=@/absolute/path/reference.png" \
  -F "prompt=保持主体构图和身份特征不变，只把背景替换为下雪的夜晚城市街道，真实摄影，无文字，无水印" \
  -F "n=1" \
  -F "quality=low" \
  -F "size=1024x1024" \
  -F "response_format=url" \
  | tee /tmp/tokenhub-image-edit.json \
  | jq
```

### 7.2 多张参考图

```bash
curl -sS \
  -X POST "${TOKENHUB_BASE_URL}/v1/images/edits" \
  -H "Authorization: Bearer ${TOKENHUB_API_KEY}" \
  -F "model=codex-gpt-image-2" \
  -F "image[]=@/absolute/path/product.png" \
  -F "image[]=@/absolute/path/style-reference.png" \
  -F "prompt=以第一张图的产品为主体，参考第二张图的配色和布光，生成干净的电商产品主图；保持产品结构和标识不变" \
  -F "n=1" \
  -F "quality=low" \
  -F "size=1024x1024" \
  -F "response_format=url" \
  | jq
```

### 7.3 异步编辑

在编辑请求中增加异步请求头：

```bash
-H "Prefer: respond-async"
```

随后使用同一个 `/v1/image-jobs/{job_id}` 轮询流程。

### 7.4 mask 的模型差异

`codex-gpt-image-2` 不要发送：

```bash
-F "mask=@/absolute/path/mask.png"
```

Codex 订阅 Images 接口做整图和参考图编辑时，mask 请求会返回：

```json
{
  "error": {
    "code": "image_mask_not_supported",
    "message": "Masks are not supported through Codex subscription accounts; use reference-image editing without a mask",
    "type": "image_mask_not_supported"
  }
}
```

`gpt-image-2` 会把单个 `mask` 文件转发给标准 OpenAI `/v1/images/edits`，可与一个或多个 `image[]` 一起使用。

## 8. 参数说明

### 8.1 文生图 JSON 参数

| 参数 | 类型 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- | --- |
| `model` | string | 否 | `codex-gpt-image-2` | 推荐显式传入 |
| `prompt` | string | 是 | 无 | 图片提示词，去除首尾空白后不能为空 |
| `n` | integer | 否 | `1` | 当前必须为 `1` |
| `quality` | string | 否 | `auto` | `auto`、`low`、`medium`、`high` |
| `size` | string | 否 | `auto` | `auto` 或合法的 `WIDTHxHEIGHT` |
| `response_format` | string | 否 | `url` | `url` 或 `b64_json` |

### 8.2 size 约束

除 `auto` 外，尺寸必须同时满足：

- 宽和高均大于 0
- 最大边不超过 3840
- 两条边都是 16 的倍数
- 长边与短边比例不超过 3:1
- 总像素数在 655,360 到 8,294,400 之间

常用尺寸：

- `1024x1024`
- `1536x1024`
- `1024x1536`
- `2048x2048`
- `2048x1152`
- `3840x2160`
- `2160x3840`

`size` 和 `quality` 会直接传递给 Codex Images 接口，但最终输出尺寸仍可能存在差异。客户端应读取实际图片尺寸，不要仅依赖请求值。

## 9. 错误处理

TokenHub 使用统一错误结构：

```json
{
  "error": {
    "code": "<ERROR_CODE>",
    "message": "<ERROR_MESSAGE>",
    "type": "<ERROR_CODE>"
  }
}
```

常见错误：

| HTTP | code | 含义 | 建议 |
| --- | --- | --- | --- |
| 400 | `unsupported_image_model` | 模型名不支持 | 使用 `codex-gpt-image-2` 或 `gpt-image-2` |
| 400 | `missing_prompt` | 缺少提示词 | 补充非空 `prompt` |
| 400 | `unsupported_image_count` | `n` 不是 1 | 设置 `n=1` |
| 400 | `invalid_quality` | quality 不合法 | 使用 `auto/low/medium/high` |
| 400 | `invalid_size` | size 不合法 | 使用 `auto` 或满足约束的尺寸 |
| 400 | `invalid_response_format` | 响应格式不合法 | 使用 `url` 或 `b64_json` |
| 400 | `missing_image` | 编辑请求没有图片 | 上传 `image` 或 `image[]` |
| 400 | `invalid_input_image` | 图片格式或内容不合法 | 使用 PNG、JPEG 或 WebP |
| 401 | `invalid_api_key` | API Key 无效 | 检查 Authorization |
| 401 | `codex_account_auth_failed` | Codex 账号凭据不可用 | 重新授权对应账号 |
| 403 | `model_not_allowed` | Key 白名单不允许该模型 | 给 Key 增加 `codex-gpt-image-2` |
| 403 | `codex_image_forbidden` | 命中的账号没有 ImageGen 权限 | 检查账号能力状态 |
| 403 | `image_url_expired` | 图片签名 URL 已过期 | 重新查询任务获取新 URL |
| 413 | `input_image_too_large` | 单张输入图超过 50 MB | 压缩输入图 |
| 415 | `invalid_content_type` | 编辑请求不是 multipart | 使用 `multipart/form-data` |
| 429 | `codex_rate_limited` | Codex 账号限流 | 延迟后重试 |
| 429 | `codex_quota_exhausted` | Codex 账号额度不足 | 切换账号或等待额度恢复 |
| 501 | `image_mask_not_supported` | Codex 订阅模型不支持 mask | 改用无 mask 编辑，或使用配置了 OpenAI API Provider 的 `gpt-image-2` |
| 503 | `codex_image_account_unavailable` | 没有健康、可用的 Codex 生图账号 | 检查账号授权、状态和生图能力 |
| 504 | `image_generation_timeout` | 生图执行超过任务超时 | 创建新任务，或在确认上游正常后调整任务超时 |

重试建议：

- 可以重试：`429`、`502`、`503`、`504`
- 不要原样重试：参数类 `400`、权限类 `401/403`、`501`
- 异步任务如果已经进入 `failed`，应根据 `error.code` 修正后创建新任务

## 10. 异步任务状态机

```text
queued
  ↓
running
  ├─→ completed
  └─→ failed
```

- `queued`：任务已写入数据库，等待处理
- `running`：任务已被服务器领取
- `completed`：图片已保存，可读取 `data`
- `failed`：读取 `error.code` 和 `error.message`

任务只能由同一项目下的 API Key 查询。其他项目查询相同任务 ID 会得到 `404 image_job_not_found`。

异步 Worker 运行在 TokenHub 后端服务内并受并发数与队列容量限制。服务重启时不会恢复此前的 `queued` 或 `running` 任务；这些任务会被标记为 `failed`，错误码为 `image_worker_restarted`，调用方应创建新任务。图片文件保存在 `TOKENHUB_IMAGE_STORAGE_DIR`；服务器部署时必须自行创建该目录、授予后端进程写权限，并将其放在需要的持久化磁盘上。

每个任务从 Worker 开始执行起最多运行 5 分钟，可通过 `TOKENHUB_IMAGE_JOB_TIMEOUT_SECONDS` 调整。超时任务会被标记为 `failed`，错误码为 `image_generation_timeout`。

Codex Images 返回 403 时，账号会被标记为暂不支持生图并在默认 24 小时内跳过。已有启用线路时，`TOKENHUB_IMAGE_CAPABILITY_RETRY_SECONDS` 到期后，模型会重新进入可发现、可路由状态；下一次真实请求会低频复测该账号，成功后恢复为“支持生图”，再次返回 403 则重新进入冷却。首次能力测试失败且没有创建线路时，不会有公开请求触发复测，管理员需要回到 Provider 的“模型”页签手动重试。该恢复机制不会为了探测能力自动生成图片或额外消耗订阅额度。

## 11. Node.js 调用

安装 OpenAI SDK：

```bash
npm install openai
```

同步文生图：

```javascript
import OpenAI from "openai";

const client = new OpenAI({
  apiKey: process.env.TOKENHUB_API_KEY,
  baseURL: `${process.env.TOKENHUB_BASE_URL}/v1`,
});

const result = await client.images.generate({
  model: "codex-gpt-image-2",
  prompt: "一只白色机械鸟停在青铜树枝上，精细产品概念图，无文字，无水印",
  n: 1,
  quality: "low",
  size: "1024x1024",
  response_format: "url",
});

console.log(result.data[0].url);
```

异步任务使用了 TokenHub 扩展协议，建议直接使用 `fetch`：

```javascript
const baseURL = process.env.TOKENHUB_BASE_URL;
const apiKey = process.env.TOKENHUB_API_KEY;

const createResponse = await fetch(`${baseURL}/v1/images/generations`, {
  method: "POST",
  headers: {
    Authorization: `Bearer ${apiKey}`,
    "Content-Type": "application/json",
    Prefer: "respond-async",
  },
  body: JSON.stringify({
    model: "codex-gpt-image-2",
    prompt: "现代建筑大厅中的大型悬浮玻璃球装置，自然光，建筑摄影，无文字",
    n: 1,
    quality: "low",
    size: "1024x1024",
    response_format: "url",
  }),
});

if (createResponse.status !== 202) {
  throw new Error(await createResponse.text());
}

const created = await createResponse.json();

while (true) {
  const response = await fetch(`${baseURL}/v1/image-jobs/${created.id}`, {
    headers: { Authorization: `Bearer ${apiKey}` },
  });
  const job = await response.json();

  if (!response.ok) throw new Error(JSON.stringify(job));
  if (job.status === "completed") {
    console.log(job.data[0].url);
    break;
  }
  if (job.status === "failed") {
    throw new Error(`${job.error.code}: ${job.error.message}`);
  }

  await new Promise((resolve) => setTimeout(resolve, 2000));
}
```

## 12. Python 调用

安装 OpenAI SDK：

```bash
python -m pip install openai
```

同步文生图：

```python
import os
from openai import OpenAI

client = OpenAI(
    api_key=os.environ["TOKENHUB_API_KEY"],
    base_url=f'{os.environ["TOKENHUB_BASE_URL"]}/v1',
)

result = client.images.generate(
    model="codex-gpt-image-2",
    prompt="雨后的城市天台花园，清晨薄雾，写实摄影，无人物，无文字",
    n=1,
    quality="low",
    size="1024x1024",
    response_format="url",
)

print(result.data[0].url)
```

## 13. 建议测试顺序

1. 调用 `/v1/models`，确认 `codex-gpt-image-2` 可见。
2. 使用 `quality=low`、`size=1024x1024` 测试同步 URL 返回。
3. 下载图片并检查实际格式、尺寸和文件大小。
4. 测试 `response_format=b64_json`。
5. 测试 `Prefer: respond-async` 和任务轮询。
6. 使用一张 PNG 测试参考图编辑。
7. 使用多张参考图测试组合场景。
8. 等 URL 过期或修改 `expires` 参数，确认返回 `image_url_expired`。
9. 重新查询任务，确认能获得新的签名 URL。
10. 检查请求日志、任务日志、Token 用量、账号命中和服务器图片文件。

## 14. 官方协议参考

TokenHub 的 Endpoint 形态参考 OpenAI Image API，但模型名、账号能力路由、异步任务和存储行为属于 TokenHub 扩展：

- [OpenAI Image generation guide](https://developers.openai.com/api/docs/guides/image-generation)
- [OpenAI Images API reference](https://developers.openai.com/api/docs/api-reference/images)
