import { retrievalSettings, type RetrievalKind } from "../domain/retrieval-settings";
import { tx } from "../i18n/runtime";

export function ProviderRetrievalGuide({ values, kind }: { values: Record<string, string>; kind: RetrievalKind }) {
  const settings = retrievalSettings(values, kind);
  const exampleBase = settings.protocol === "tei" ? "http://model-host:8000" : settings.protocol === "cohere" ? "https://model.example/v2" : settings.protocol === "dashscope" ? "https://model.example/api/v1" : "http://model-host:8000/v1";
  return <div className="retrieval-connection-guide">
    {settings.managed ? <p className="field-help">{tx("该渠道由原生或插件适配器处理接口，请按渠道说明配置。")}</p> : settings.protocol ? <p className="field-help"><span>{tx("保存后使用的协议")}</span> <code>{settings.protocol}</code></p> : <p className="field-help">{tx("无法自动确定该渠道的重排协议，请按上游服务选择。")}</p>}
    {settings.endpoint ? <div className="retrieval-endpoint-preview"><span>{tx("渠道级请求地址预览")}</span><code>{settings.endpoint}</code><small>{tx("资源账号可覆盖渠道连接配置；预览不代表连通性验证。")}</small></div> : null}
    <details className="retrieval-examples">
      <summary>{tx("查看配置示例")}</summary>
      <p>{tx("以下仅演示地址拼接，请使用实际部署的上游地址。")}</p>
      <dl><dt>Base URL</dt><dd><code>{exampleBase}</code></dd><dt>{tx("接口路径（可留空）")}</dt><dd><code>{settings.defaultPath}</code></dd><dt>{tx("拼接结果")}</dt><dd><code>{exampleBase + settings.defaultPath}</code></dd></dl>
      <p>{tx("上游若直接提供根路径接口，Base URL 不加 /v1；不要在两处重复填写 /v1。")}</p>
    </details>
  </div>;
}
