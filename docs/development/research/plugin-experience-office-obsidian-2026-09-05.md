# TokenHub 插件体验对照研究：Office Add-ins 与 Obsidian

访问日期：2026-09-05
研究范围：插件开发路径、安装与更新、插件详情页、每插件设置页
资料原则：外部事实只引用 Microsoft 与 Obsidian 官方文档、官方帮助站和官方仓库；TokenHub 现状以本仓库源码与文档为准。

## 结论先行

TokenHub 当前的插件开发主路径是成立的，不需要改造成 Office Add-in 或 Obsidian 插件的复制品。更合适的组合是：

- 用 **Office Add-ins** 作为企业插件治理的参考：manifest 契约、兼容性、集中部署、发布审核、更新时重新确认新增权限。
- 用 **Obsidian** 作为管理员交互的参考：安装与启用分离、设置入口按需出现、插件设置集中在宿主应用的设置导航里、每个字段都有名称与说明。
- 保留 TokenHub 自己更严格的服务端安全边界：checksum、签名、权限差异、待重启、回滚、审计和包文件检查。这些能力不应为了“像 Obsidian”而弱化。

当前最明显的不一致不是底层开发路径，而是详情页的信息架构。面向普通管理员的默认页面不应展示“能力”“实现清单”、Hook ID、slot、序列化声明或文件统计。Microsoft 明确要求 Marketplace 摘要避免术语并先说用户价值，Office 首次使用指南也要求只突出核心收益和下一步；Obsidian 同样要求插件说明简短、直接、用动作开头。现有详情页应据此重做，而不是只换视觉样式。

最重要的产品规则是：**插件没有可编辑设置时，不显示“设置”按钮或“设置”页；有设置时，才把该插件列入设置目录。** 这与 Obsidian 官方的已安装插件管理规则完全一致：设置图标只在插件有可配置选项时出现。[来源：Obsidian Community plugins](https://help.obsidian.md/community-plugins)

## 一、三套插件路径的真实结构

### 1. Microsoft Office Add-ins

Office Add-in 的基本单元不是一个直接在 Office 进程内运行的传统插件包，而是“app package + 托管 Web 应用”。app package 包含 manifest、图标和可选配置/本地化文件；业务逻辑与数据通常由 HTTPS Web 应用提供。manifest 声明名称、说明、ID、版本、集成方式、所需权限和数据访问要求。[来源：Office Add-ins platform overview](https://learn.microsoft.com/en-us/office/dev/add-ins/overview/office-add-ins)

开发与交付路径可以概括为：

1. 用 Microsoft 365 Agents Toolkit、Yeoman 或示例工程创建项目。
2. 编写 unified manifest 或 add-in only XML manifest，并开发 task pane、ribbon command、dialog 等 Web UI。
3. 在本地托管和调试，通过 sideload 把 manifest 临时加载到 Office。Microsoft 明确说明 sideload 用于开发测试，不是生产发布方式。[来源：Sideload Office Add-ins for testing](https://learn.microsoft.com/en-us/office/dev/add-ins/testing/sideload-office-add-ins-for-testing)
4. 生产发布有两条主路：提交 Microsoft Marketplace 面向公众分发，或通过 Microsoft 365 管理中心的 Integrated apps 向组织中的用户/群组集中部署。[来源：Deploy and publish Office Add-ins](https://learn.microsoft.com/en-us/office/dev/add-ins/publish/publish)
5. 更新时要区分 Web 应用和 app package：Web 应用代码更新通常无需用户操作；manifest 或 package 文件改变时必须提高版本并更新发布。若新增或改变权限、支持的 Office 应用或事件，组织管理员需要重新同意，用户在同意前会被阻止使用新版。[来源：Update and maintain your Office Add-in](https://learn.microsoft.com/en-us/office/dev/add-ins/publish/maintain-breaking-changes)

Office 给 TokenHub 的核心启示不是“照搬 task pane”，而是把以下三层分开：

- 发布信息：用户价值、说明、图标、截图、开发者、支持渠道。
- 运行契约：manifest、支持的宿主、权限、版本与兼容要求。
- 插件自己的功能界面：由插件在 task pane、ribbon 或 dialog 中提供。

Office 并没有一个统一的“每个 add-in 设置表单”供宿主自动生成。官方设计指南把“settings and configuration”列为 task pane 内适合承载的内容，也就是由插件自己的 Web UI 实现。[来源：Navigation patterns for Office Add-ins](https://learn.microsoft.com/en-us/office/dev/add-ins/design/navigation-patterns) 设置数据可以按文档、用户或在线账户范围保存，但存储 API 与设置页面是两回事。[来源：Persist add-in state and settings](https://learn.microsoft.com/en-us/office/dev/add-ins/develop/persisting-add-in-state-and-settings)

### 2. Obsidian 插件

Obsidian 插件是本地代码插件。开发时把项目放在 vault 的 `.obsidian/plugins/<plugin-id>/` 下，安装依赖并编译出 `main.js`，然后在 **Settings → Community plugins** 中启用。`manifest.json` 提供 ID、名称、版本、最低 Obsidian 版本、说明、作者和平台限制等信息。[来源：Build a plugin](https://docs.obsidian.md/Plugins/Getting+started/Build+a+plugin) [来源：Manifest](https://docs.obsidian.md/Reference/Manifest)

开发与交付路径可以概括为：

1. 从官方 sample plugin 创建项目。
2. 编写 `manifest.json` 和 TypeScript 源码，构建 `main.js`；`styles.css` 可选。
3. 在单独的开发 vault 中加载、启用和反复 reload。
4. 发布 GitHub Release，tag 必须与 `manifest.json.version` 一致，release 附件至少包含 `main.js`、`manifest.json`，可选 `styles.css`。
5. 首次版本提交 Community directory 审核；正式收录后，后续版本由 Obsidian 直接从 GitHub Release 获取。[来源：Submit your plugin](https://docs.obsidian.md/Plugins/Releasing/Submit+your+plugin)
6. 用户在 Community plugins 浏览、打开插件、安装，然后单独启用。出于安全原因，Community plugins 不自动更新；用户可以检查更新、全部更新或只更新某个插件。[来源：Obsidian Community plugins](https://help.obsidian.md/community-plugins)

Obsidian 的每插件设置路径很明确：插件只有在运行时调用 `addSettingTab(...)` 注册设置页后，宿主才显示其设置入口。官方新式 declarative settings API 允许插件声明名称、说明、控件、默认值、验证、条件显示、分组、列表和子页面；设置由 `loadData()` / `saveData()` 持久化。[来源：Obsidian plugin settings](https://docs.obsidian.md/Plugins/User+interface/Settings)

用户提供的截图体现了同一个结构：左侧是宿主统一的 Settings 目录，安装且声明了设置页的 Community plugin 才成为左侧一项；右侧完全属于该插件，插件内部可以再按 General、Claude、Codex 等任务语义分组。值得学习的是“入口条件”和“字段表达”，不是照抄截图的颜色、圆角或 Tab 数量。

### 3. TokenHub 当前路径

TokenHub 当前路径是：

1. 用 `plugin-devkit` 与示例包开发。
2. 在包根目录编写 `plugin.yaml`，声明稳定 ID、语义版本、Plugin API、kind、placement、entry、capabilities、permissions 和 distribution。
3. 用 Devkit 契约测试验证 manifest 和运行时行为。
4. 构建 ZIP，记录 SHA-256；Marketplace 可提供签名、发布说明、下载地址和兼容信息。
5. 管理员从 Marketplace、直接 URL 或 ZIP 安装；直接 URL 要求 checksum，并可先看权限差异。
6. 安装或更新后可能进入 `pending_restart`；启用、禁用、卸载、更新和回滚由管理后台控制。
7. 插件详情和文件检查用于运行状态与审计；声明了可编辑主题值的插件才有 Settings 路由。

仓库依据：[Plugin Development](../../plugin-development/README.md)、[Manifest Reference](../../plugin-development/manifest-reference.md)、[Packaging and Release](../../plugin-development/packaging-and-release.md)、[`plugins.tsx`](../../../frontend/features/admin/views/plugins.tsx)、[`plugin-detail.tsx`](../../../frontend/features/admin/views/plugin-detail.tsx)。

## 二、是否与 Office / Obsidian 保持一致

| 维度 | Office Add-ins | Obsidian | TokenHub 判断 |
| --- | --- | --- | --- |
| 插件身份与契约 | manifest 声明 ID、版本、说明、宿主集成和权限 | `manifest.json` 声明 ID、版本、最低宿主版本、说明、作者与平台 | **一致。** `plugin.yaml` 作为单一入口是正确路径。 |
| 运行载荷 | manifest/package 与远程 HTTPS Web 应用分离 | release 中下发本地 `main.js`、manifest、可选 CSS | **有意不同。** TokenHub ZIP 可含服务端命令和声明式 UI；不能按 Office 的“远程网页即时更新”处理。 |
| 开发加载 | sideload manifest，仅用于测试 | 放进 vault 插件目录并启用/reload | **一致。** Devkit 示例 + 本地 ZIP/目录测试属于同类开发闭环。 |
| 公共发布 | Partner Center 审核、认证、Marketplace | 初次提交 Community directory；后续 GitHub Release | **大体一致。** TokenHub 的远程索引、不可变 ZIP、checksum 和签名更适合企业网关。 |
| 安装与启用 | 用户安装或管理员集中部署；权限/能力影响同意流程 | 安装后单独启用 | **一致且应保留。** 安装不应等同于立即运行；“安装后启用”只能作为明确选项。 |
| 更新 | Web 内容可透明更新；package/manifest 更新要升版，敏感变更需管理员同意 | 安全原因不自动更新，用户检查后更新 | **TokenHub 应偏 Obsidian + Office 管理同意。** 服务器插件不宜静默自动更新。 |
| 设置入口 | 插件在自己的 task pane/UI 中实现，没有统一自动设置目录 | 只有注册 SettingTab 的插件才出现设置入口 | **应采用 Obsidian。** 无设置不显示入口；有设置才显示并进入该插件页面。 |
| 详情页 | Marketplace 强调用户价值、截图、开发者、支持和准确说明 | Overview、Scorecard、Updates、版本、平台、许可、维护/审查信息 | **当前底层数据够，默认呈现不一致。** 技术清单不应占据首屏。 |
| 权限隔离 | manifest 权限、认证/认证审核、管理员再同意 | 官方说明无法可靠限制插件到细粒度权限；插件继承 Obsidian 的访问能力 | **绝不能照搬 Obsidian。** TokenHub 的最小权限、差异预览、签名和审计必须保留。 |
| 回滚与待重启 | 依赖部署渠道与宿主刷新 | 以本地 release 更新/reload 为主 | **TokenHub 需要更强。** 网关是多人共享基础设施，回滚与待重启是合理的企业能力。 |

总体结论：**开发路径约有七成一致，安全与运行模型必须保持 TokenHub 特有设计；真正需要重做的是面向管理员的表达层。**

## 三、安装与更新应该怎样呈现

### 推荐的信息架构

插件管理保留三个顶层目的，而不是把所有操作塞进同一张技术表：

1. **已安装**：搜索、状态筛选、版本、是否有更新，以及启用/禁用、详情、设置、更新、卸载。
2. **插件市场**：发现插件并进入安装前详情页。Marketplace 是首选入口。
3. **手动安装**：URL 与 ZIP 属于管理员高级操作，不应与普通市场浏览并列成默认首屏。

安装前详情页必须先回答：

- 这个插件解决什么问题？
- 谁发布的，是否验证？
- 安装后会出现在哪里，怎样开始使用？
- 支持当前 TokenHub 版本吗？
- 要读取或修改哪些数据？相比已装版本新增了什么权限？
- 当前版本、更新时间、发布说明、截图、许可证和支持链接是什么？
- 是否需要重启？安装完成后是否立即启用？

Microsoft 对 Marketplace 的官方要求与此完全相符：名称和说明首先解释“它解决什么问题”，摘要要把最重要的信息放前面、避免专业术语，详情说明再展开主要功能、用途和目标用户，并用真实截图帮助用户理解。[来源：Create effective listings in Microsoft Marketplace](https://learn.microsoft.com/en-us/partner-center/marketplace-offers/create-effective-office-store-listings)

### 更新策略

建议继续默认手动确认更新，并把更新拆成清晰的三步：

1. **发现**：列表显示“有新版本”，详情显示当前版本、目标版本和发布说明。
2. **复核**：先显示兼容性、签名/checksum 和权限差异；新增权限用自然语言说明影响。
3. **应用**：更新后明确显示“立即生效”或“重启后生效”，并给出回滚入口与目标版本。

不要把“有下载地址”直接等同于“有可用更新”。更新可用性应由 Marketplace 的版本比较与兼容结果决定。对于内置插件，显示“随 TokenHub 更新”，不要放一个永远不可用的更新按钮。

## 四、插件详情页的推荐结构

### 默认 Overview：普通管理员可读

首屏只回答“是什么、能做什么、现在是否可用、下一步去哪”：

1. **Header**
   - 图标、插件名、简短说明。
   - 开发者/发布者与验证状态。
   - 已启用/已禁用/待重启/异常状态。
   - 当前版本；有更新时显示目标版本。
   - 主操作：打开功能或前往使用位置；次操作：设置（仅有设置时）、更新、启用/禁用。

2. **这个插件做什么**
   - 2–5 条管理员能理解的功能说明。
   - 每条按“动作 + 对象 + 结果”写，例如“连接 OpenAI 兼容模型服务”“在模型请求发送前删除敏感字段”。
   - 不显示 capability 名称、kind、slot 或 JSON。

3. **在哪里使用**
   - 直接说明入口，例如“Provider 管理”“路由策略”“管理后台外观”“后台任务”。
   - 能跳转时提供“前往 Provider 管理”一类按钮。只写“运行位置 gateway_chain”对管理员没有帮助。

4. **安全与兼容性**
   - 用结论 + 一句话解释展示来源可信度、签名、兼容状态、权限范围、已知安全提示。
   - 只有异常项需要强视觉强调；正常项保持简洁。

5. **版本与支持**
   - 当前版本、更新状态、最近发布说明、许可证、主页/文档/支持链接。

这个结构同时符合 Office “favor content over chrome”“重要决定易理解、操作可逆”的原则，以及首次使用只讲核心收益和下一步的原则。[来源：Design the UI of Office Add-ins](https://learn.microsoft.com/en-us/office/dev/add-ins/design/add-in-design) [来源：First-run experience patterns](https://learn.microsoft.com/en-us/office/dev/add-ins/design/first-run-experience-patterns)

### Developer information：默认折叠

技术信息仍然有价值，但受众是插件开发者和排障人员，应放在默认折叠的 **开发者信息** 中：

- Plugin ID、Plugin API、manifest schema、Core 兼容范围。
- package 文件数、大小、checksum、签名算法与 key ID。
- capabilities、hooks、UI contributions、actions、background jobs。
- package 文件清单和安全允许的源码预览。

展开后仍不能只堆标识符。每个分类先解释用途，每一项都要有：

- 面向人的名称。
- 一句话说明“何时运行、影响什么”。
- 技术 ID 与原始 metadata，作为次级信息。

原始 JSON 不应出现在 Overview；确有审计需要时放到 Files 或单独的 manifest 预览中。

### Files：保留，但降级为审计工具

Office 和 Obsidian 的普通用户详情页都不会把文件清单放在核心信息里。TokenHub 因为运行服务端插件，保留只读 Files 有企业审计价值，但应：

- 对内置插件隐藏或显示一句“内置实现，无独立安装包”。
- 对外部插件保留 package 清单、大小和安全预览。
- 不把文件数量和包大小放进 Overview 首屏。
- 长期可按角色只对有审计/开发权限的管理员显示。

## 五、每插件设置页的推荐结构

### 入口规则

- 插件未声明可编辑设置：详情页和已安装列表都不出现“设置”；访问旧 `/settings` URL 时回到 Overview，并可显示简短提示。
- 插件声明设置且当前宿主支持渲染：在已安装列表显示设置按钮，在详情页显示 Settings Tab，并在全局设置目录的“插件”分组下列出插件名。
- 插件有运行参数但只能通过其他业务页面配置：不要伪装成插件设置。例如 Provider 凭据应跳到 Provider 管理，而不是复制一套表单。

这直接采用 Obsidian 的规则：已安装插件管理中，Settings 图标只在插件有 configurable options 时出现；插件通过注册 SettingTab 提供页面。[来源：Obsidian Community plugins](https://help.obsidian.md/community-plugins) [来源：Obsidian plugin settings](https://docs.obsidian.md/Plugins/User+interface/Settings)

### 页面规则

- 每个设置项必须有用户可理解的名称和一句效果说明。
- 按用户任务分组，例如“常规”“显示”“连接”“对话”“内容”，不要按 manifest 字段或 capability 分组。
- 控件与数据类型匹配：开关、下拉、文本、数字、滑杆、颜色、文件/目录选择。
- 提供默认值、输入校验、错误说明和“恢复默认值”。
- 依赖项使用条件显示或禁用状态，不要一次把无关选项全铺开。
- 多于 4 个一级分组时优先使用侧栏或折叠分组；窄屏避免横向 Tab 溢出。Office 的导航指南建议 2–4 个同级区块才使用 Tab，更多区块应使用可折叠菜单，并要求当前上下文始终清晰。[来源：Navigation patterns for Office Add-ins](https://learn.microsoft.com/en-us/office/dev/add-ins/design/navigation-patterns)
- 明确设置作用域：仅当前浏览器、当前管理员、全组织、某个 Provider、某个项目。TokenHub 当前主题覆盖保存在浏览器时，页面应明确写出“只影响当前浏览器”，否则管理员会误以为全团队生效。
- 安全敏感值不得使用普通文本或浏览器存储；需要服务端加密、脱敏回显、权限控制和审计。

中长期建议从当前仅围绕 `theme_tokens` 的判断，演进为显式的 `settings` 声明或受控 schema。宿主只渲染允许的控件，插件不能注入任意 React/JavaScript。声明至少应覆盖 section、label、description、control、default、validation、scope、secret、restart requirement。这样既得到 Obsidian 的设置体验，又保留 TokenHub 的企业安全边界。

## 六、不应该照搬的内容

### 不照搬 Office

- 不把插件运行时改成远程任意 Web 页面。Office 的核心假设是 HTTPS Web app；TokenHub 处理模型请求和凭据，需要更受控的进程、协议和数据投影。
- 不采用“Web 资源一上传用户立即看到”的静默更新语义。TokenHub 服务端插件会影响全体调用方，必须版本化、校验、审计并支持回滚。
- 不把每插件配置完全交给任意自绘 UI。Office task pane 很自由，但 TokenHub 应优先 schema-driven、allowlisted 控件。

### 不照搬 Obsidian

- 不采用其权限模型。Obsidian 官方明确说受技术限制，无法可靠把插件限制到特定权限，插件继承 Obsidian 的访问级别。[来源：Obsidian plugin security](https://help.obsidian.md/plugin-security) TokenHub 恰恰需要细粒度最小权限和数据投影。
- 不依赖 GitHub Release 作为唯一企业分发渠道。TokenHub 应继续支持受签名的 Marketplace、镜像、离线包和撤销信息。
- 不把本地 vault 级数据存储模式套到多人共享的网关。TokenHub 设置需要明确租户/组织/项目/用户作用域。
- 不把所有插件都直接运行在主进程并共享宿主权限。

## 七、对当前实现的优先级建议

### P0：立即修正详情页表达

- 用“关于此插件”“这个插件做什么”“在哪里使用”“安全与兼容性”替换首屏“能力”“实现清单”。
- 技术清单默认折叠，分类和每项都补说明。
- Overview 不显示原始 JSON、Hook/slot 标识、文件数量和包大小。
- 有真实 Marketplace `summary` / `description` 时直接使用；缺失时按插件种类给出简短 fallback，而不是展示 ID。

### P0：统一设置入口条件

- 无设置不显示按钮或 Tab。
- 有设置才显示，并保证列表、详情、直接 URL 三处逻辑一致。
- 每个设置字段必须有 label、description、default/validation 语义。

### P1：补“在哪里使用”与主操作

- 把 provider、gateway hook、admin UI、background job、theme/layout 映射为管理员语言。
- 从详情页跳转到真实业务入口，而不是让管理员根据 kind/placement 自己猜。

### P1：把 Marketplace 元数据接入详情页

TokenHub 类型中已经具备 `summary`、`description`、publisher、screenshots、release notes、advisories、compatibility 和 trust 等字段，可直接支撑 Office/Obsidian 风格的详情页。优先重用这些数据，不必先扩展底层 manifest。

### P2：建立通用设置声明

- 在安全允许的控件白名单内支持文本、数字、开关、下拉、颜色、文件/目录和分组。
- 支持设置作用域、服务端持久化、secret、校验、重启提示和权限审计。
- 继续禁止 arbitrary frontend code，把复杂业务配置引导到对应的 TokenHub 业务页面。

## 官方来源清单

以下页面均于 2026-09-05 访问。

### Microsoft

- [Office Add-ins platform overview](https://learn.microsoft.com/en-us/office/dev/add-ins/overview/office-add-ins)
- [Office Add-ins manifest](https://learn.microsoft.com/en-us/office/dev/add-ins/develop/add-in-manifests)
- [Sideload Office Add-ins for testing](https://learn.microsoft.com/en-us/office/dev/add-ins/testing/sideload-office-add-ins-for-testing)
- [Deploy and publish Office Add-ins](https://learn.microsoft.com/en-us/office/dev/add-ins/publish/publish)
- [Publish your Office Add-in to Microsoft Marketplace](https://learn.microsoft.com/en-us/office/dev/add-ins/publish/publish-office-add-ins-to-appsource)
- [Update and maintain your Office Add-in](https://learn.microsoft.com/en-us/office/dev/add-ins/publish/maintain-breaking-changes)
- [Create effective listings in Microsoft Marketplace and Microsoft 365 app stores](https://learn.microsoft.com/en-us/partner-center/marketplace-offers/create-effective-office-store-listings)
- [Design the UI of Office Add-ins](https://learn.microsoft.com/en-us/office/dev/add-ins/design/add-in-design)
- [Navigation patterns for Office Add-ins](https://learn.microsoft.com/en-us/office/dev/add-ins/design/navigation-patterns)
- [First-run experience patterns](https://learn.microsoft.com/en-us/office/dev/add-ins/design/first-run-experience-patterns)
- [Persist add-in state and settings](https://learn.microsoft.com/en-us/office/dev/add-ins/develop/persisting-add-in-state-and-settings)
- [Privacy and security for Office Add-ins](https://learn.microsoft.com/en-us/office/dev/add-ins/concepts/privacy-and-security)

研究时核对的 Microsoft 官方文档仓库快照：[`OfficeDev/office-js-docs-pr@9180810`](https://github.com/OfficeDev/office-js-docs-pr/tree/9180810c75ba99fbc755a0e956b60803687db25b)。

### Obsidian

- [Community plugins](https://help.obsidian.md/community-plugins)
- [Community directory listing page](https://help.obsidian.md/community-directory)
- [Plugin security](https://help.obsidian.md/plugin-security)
- [Build a plugin](https://docs.obsidian.md/Plugins/Getting+started/Build+a+plugin)
- [Plugin anatomy and lifecycle](https://docs.obsidian.md/Plugins/Getting+started/Anatomy+of+a+plugin)
- [Plugin settings](https://docs.obsidian.md/Plugins/User+interface/Settings)
- [Plugin manifest](https://docs.obsidian.md/Reference/Manifest)
- [Submit your plugin](https://docs.obsidian.md/Plugins/Releasing/Submit+your+plugin)
- [Release your plugin with GitHub Actions](https://docs.obsidian.md/Plugins/Releasing/Release+your+plugin+with+GitHub+Actions)
- [Official sample plugin](https://github.com/obsidianmd/obsidian-sample-plugin)
- [Official community plugin index](https://github.com/obsidianmd/obsidian-releases)

研究时核对的官方仓库快照：[`obsidian-developer-docs@c56c7e7`](https://github.com/obsidianmd/obsidian-developer-docs/tree/c56c7e770ba25dd0ea392aacf4588f9425970d36)、[`obsidian-help@b2bbfc1`](https://github.com/obsidianmd/obsidian-help/tree/b2bbfc141948816ed46f3360cef38ce59f118013)、[`obsidian-sample-plugin@07ceb81`](https://github.com/obsidianmd/obsidian-sample-plugin/tree/07ceb81d1fb3384af611ebf665a1ec42a7e5926d)、[`obsidian-releases@142f6c5`](https://github.com/obsidianmd/obsidian-releases/tree/142f6c5d175ba1b3fbcd868ebdf40404486a6075)。
