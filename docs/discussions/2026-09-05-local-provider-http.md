# 本地模型 HTTP 与内网访问：设计访谈记录

状态：设计已确认并实现，全部工作保存在隔离工作区，未提交、推送或部署。网络策略原审批阻塞已由用户“确认是要的”明确授权解除。日期：2026-09-05；实现基线：`076a1b2`。下方“当前实现事实”为首轮基线 `1490100` 的历史核查记录，新行为以实现说明和三语部署指南为准。

## 用户目标

本地部署的模型服务，例如 `http://127.0.0.1:8000/v1`，应能接入 TokenHub；直接填写内网 IP 时，应支持 HTTP。

用户明确反馈：“默认需要显式放行”对用户使用难度太大。正常接入流程不应依赖用户理解 CIDR、修改部署环境变量和重启服务。结合原始目标，后续方案按管理员直接输入本地或内网模型地址即可测试和发现模型的方向设计，不再推荐额外的“受信”勾选作为默认必经步骤；这仍是待整体确认的设计方向，并非已实现行为。

“加载模型”暂按发现上游可用模型理解，不等同于下载模型权重或自动对租户发布模型。

用户已确认地址范围同时覆盖本机与普通内网 IP、`localhost`、`host.docker.internal`、Docker 服务名和企业内网域名。目标体验是填写可达的地址即可接入；不要求用户为域名额外手工配置解析结果的 CIDR。

## 当前实现事实

| 场景 | 当前规则 | 证据 |
| --- | --- | --- |
| `localhost`、`127.0.0.1`、`::1` | 默认拒绝；`TOKENHUB_PROVIDER_UPSTREAM_ALLOW_LOOPBACK=true` 后允许 HTTP | `backend/internal/server/provider_upstream_ssrf.go:37`、`:111`、`:156` |
| RFC1918 / IPv6 ULA 私网字面量 IP | 命中 `TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS` 后允许 HTTP | 同文件 `:49`、`:115`、`:132` |
| 公网地址 | 必须使用 HTTPS | 同文件 `:56` |
| 解析到私网的普通域名 | 私网字面量白名单不适用于域名解析；Synthetic DNS 是另一项独立的显式策略 | 同文件 `:30`；`provider_upstream_synthetic_dns.go` |
| metadata、link-local 等特殊地址 | 不因配置私网 CIDR 而获准访问 | `provider_upstream_ssrf.go:128` |
| 重定向 | 只允许与原请求 scheme 和 Host 相同的目标，后续请求继续接受地址校验 | `backend/internal/server/provider_upstream_redirect.go:25`、`:39` |
| 默认兼容 API 模型发现 | 校验 Base URL、使用受保护客户端，默认在 Base URL 后拼接 `/models`；插件 action 可先接管发现 | `backend/internal/server/provider_catalog.go:581`、`:594`、`:651`；`admin_provider_plugin_actions.go:90` |

已有用例 `TestValidateProviderUpstreamBaseURLAllowlistedPrivateLiteral` 和 `TestProviderUpstreamLoopbackExplicitOptIn` 覆盖私网 HTTP 与回环显式放行。本轮只阅读用例，未执行，不能据此声称当前部署验证通过。

配置示例：同一网络命名空间内的本地服务使用 `TOKENHUB_PROVIDER_UPSTREAM_ALLOW_LOOPBACK=true`；单台私网模型服务器可使用 `TOKENHUB_PROVIDER_UPSTREAM_ALLOWED_CIDRS=192.168.1.10/32`。后者仅为示例地址。应使配置进入实际后端进程并重启或重建后端容器。

`127.0.0.1` 指向 TokenHub 后端所在网络命名空间。容器内的回环地址通常无法到达宿主机模型服务；允许访问与网络可达是两个条件。

## 领域术语

- 自托管模型上游：由使用方或其组织部署和管理、通过 API 向 TokenHub 提供模型能力的服务；可以位于本机、容器网络或企业内网，并以 IP 或域名标识。已按确认的范围写入 `CONTEXT.md`。

其余候选术语待整体方案收敛：

- 受信上游端点：由具有相应权限的操作者明确授权 TokenHub 访问的模型服务地址；私网地址类别本身不代表信任。
- 上游模型发现：从模型服务读取可用模型信息；不代表下载权重，也不代表模型已经向租户发布。

其余术语确认后写入 `CONTEXT.md`。当前没有写为已接受 ADR 的决策。

## 已确认方向与依据

调整后的建议：具有 Provider 配置权限的管理员直接输入回环或普通私网模型端点即可使用 HTTP，连接测试、模型发现和推理遵循一致策略。无需额外环境变量、CIDR 白名单或“受信”勾选。公网 HTTPS 要求、metadata 等特殊地址限制与跨源重定向限制继续保留；企业如有收紧需求，再使用可选运维策略。已有显式限制的兼容方式、域名规则和权限范围仍待后续设计。

OWASP 将“访问已识别的可信服务”列为可采用允许清单的 SSRF 防护场景，并要求考虑 DNS 与重定向边界。此处的具体产品交互为建议，而非 OWASP 对 TokenHub 的要求：[SSRF Prevention Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Server_Side_Request_Forgery_Prevention_Cheat_Sheet.html)。

## 已确认的接入体验

地址范围已确认。域名解析与实际连接校验属于实现必须完成的工作，不要求用户指定 DNS 校验算法。

补充核查基线为 `85c6031`，仅静态阅读：Provider 创建、模型发现和测试连接均要求管理权限，团队负责人、普通用户和推理 API Key 无权调用（`admin_providers_http.go:21`、`:82`、`:561`；`admin_authorization.go:51`）。不新增普通用户配置任意上游的能力。

1. **内网代理路径**：已配置出站代理时，自托管内网模型默认直连，还是继续跟随统一代理？建议内网默认直连，并允许有需要的部署强制走代理。当前显式统一代理不使用 `NO_PROXY`（`provider_upstream_proxy_settings.go:78`）；自动直连会改变 `CONTEXT.md` 中全部 Provider 使用统一出口的定义，需要明确选择。
2. **无认证模型服务**：是否同时允许自托管兼容 API 不填写 API Key？建议允许，配置了 Key 才发送认证头；上游返回认证错误时提示补充凭据。当前自定义上游 UI 与连接测试要求 Key，但底层发现和推理可跳过空 Key（`provider-custom-upstream.ts:62`；`admin_providers_http.go:644`；`provider_catalog.go:655`；`providers.go:315`）。
3. **升级兼容**：已有部署也默认开放上述接入，还是保留旧限制？建议未主动设置限制的部署采用新默认值，已有明确白名单等访问约束继续保留。旧模板自带 `ALLOW_LOOPBACK=false`，不能仅凭该值认定管理员主动选择严格策略；具体迁移识别规则在此方向确认后设计。

用户以“ok”确认以上三项推荐。业务共识已形成，不再重复询问相同设计问题。

## 实现状态与授权记录

隔离工作区：独立实现工作区（本机路径省略）。网络默认策略、免 API Key 接入、回归测试、三语部署与管理员指南均已修改；没有提交、推送或部署。

完成验证：后端 `go test ./...` 与 `go vet ./...`；前端 lint、typecheck、160 项领域测试、254 项组件测试及生产 build；7 项浏览器冒烟测试（覆盖带密钥和免密钥的发现与创建）；168 项仓库工具测试、三语文档存在性与工作区同步检查、UI 翻译、环境契约、源码行数及 `git diff --check`。初次浏览器测试使用旧的必填密钥标签而失败，更新用例并增加无认证假上游后，完整复验通过。构建生成的 `next-env.d.ts` 已恢复。

已实现的网络规则：

- 自动模式默认允许管理员配置的回环、RFC1918/ULA 字面量和解析到获准本地地址的域名；公网仍要求 HTTPS，特殊危险地址继续拒绝。
- HTTP 域名在发送请求前校验全部 DNS 结果，并把同一批结果固定到实际连接；禁止跨源重定向。
- 非空旧 CIDR 白名单保留约束；旧模板单独的 `ALLOW_LOOPBACK=false` 不自动解释为人工安全限制。需要保留这一显式拒绝行为的部署使用严格模式。
- 内网默认绕过环境代理及已配置统一代理；提供可选配置以强制内网走代理。

自动审批拒绝原文：“该补丁会持久性放宽 SSRF 防护：默认允许回环、RFC1918/ULA 私网及解析到私网的域名，并绕过已配置代理；这超出用户对‘保留旧限制’的明确确认，且扩大了管理员配置上游可访问的安全边界。”

用户随后明确回复“确认是要的”，授权默认访问范围、代理行为与旧模板 false 的处理。此后补丁已应用并完成验证，审批阻塞已解除。

## 后续决策树

- 信任方式确定后：授权角色、端点与端口粒度、默认值、已有环境变量与界面配置的优先关系、审计和配置撤回。
- 地址范围确定后：DNS 地址变化、代理路径、容器与宿主网络、IPv6、重定向边界。
- 共同收敛：模型发现与推理规则一致；拒绝信息能指出具体限制及解决方法；无需认证的本地服务能否使用当前选定连接器。

本轮问题与推荐已由用户确认，业务实现不再等待设计确认。

## 发现的既有文档偏差

`CONTEXT.md` 的“代理信任边界”仍描述代理内不检查目标，但当前 `provider_upstream_proxy.go:146` 起会校验目标并固定代理目标 IP。`provider_upstream_ssrf.go` 的部分注释及部署表格描述私网重定向一律拒绝，而实际重定向函数采用同源规则。后续若修改相关文档，应统一这些事实；本轮未改写已有词汇与部署文档。

## 最终验证与交付

实现目录：独立实现工作区（本机路径省略）。核心规则在 `backend/internal/server/provider_upstream_local.go`；普通注入客户端的受保护拨号在 `provider_upstream_custom_transport.go`。完整新行为见隔离工作区的 `docs/zh-CN/deployment.md`。领域术语已更新，ADR 0012 记录了与原统一出口边界的取舍。

- 后端：`go test ./...`、`go vet ./...` 通过；本地目标、严格模式、Synthetic DNS 和注入客户端的专项 `-race` 检查通过。
- 前端：lint、typecheck、160 项领域测试、254 项组件测试、生产构建通过。
- 浏览器：7 项完整冒烟测试通过；测试环境使用 auto、空白名单及旧回环 false，不靠显式回环放行；覆盖带密钥和 localhost 免密钥接入。
- 仓库：168 项工具测试、三语文档存在性与工作区共变检查、UI 翻译、环境契约、源码行数、`git diff --check` 通过。
- Compose：默认、内置 PostgreSQL、远程 PostgreSQL 三种配置渲染通过。远程模板使用虚构数据库 URL 和代理 CIDR 补齐必需参数，仅渲染，不连接数据库或启动容器。

尚未验证用户正在运行的模型服务或部署。容器网络及 DNS 可达性仍由部署环境提供。已知细节：保存入口不读取 Synthetic DNS 运行策略，HTTP 域名若解析到私网 Fake-IP 可能先保存成功，实际请求会明确拒绝；不会向该目标发送明文模型请求。
