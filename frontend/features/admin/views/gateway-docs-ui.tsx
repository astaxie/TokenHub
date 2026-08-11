import { Check, Copy } from "lucide-react";
import { useState } from "react";
import { copyText } from "../domain/clipboard";
import { type AppLanguage, tx } from "../i18n/runtime";

export function gatewayLanguageLabel(language: AppLanguage) {
  if (language === "zh-CN") return "中文";
  if (language === "ja") return "日本語";
  return "English";
}

export function apiMethodClass(method?: string) {
  const normalized = (method || "").toLowerCase();
  if (normalized.includes("/") || normalized.includes(",")) return "mixed";
  return normalized || "muted";
}

export function GatewayCopyCard({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);
  async function copyValue() {
    const success = await copyText(value);
    setCopied(success);
    if (success) window.setTimeout(() => setCopied(false), 1400);
  }
  return (
    <article className="gateway-copy-card">
      <span>{label}</span>
      <strong>{value}</strong>
      <button className="icon-button subtle" onClick={() => void copyValue()} type="button" title={tx("复制")}>
        {copied ? <Check size={15} /> : <Copy size={15} />}
      </button>
    </article>
  );
}

export function GatewayCodeBlock({ code }: { code: string }) {
  const [copied, setCopied] = useState(false);
  async function copyCode() {
    const success = await copyText(code);
    setCopied(success);
    if (success) window.setTimeout(() => setCopied(false), 1400);
  }
  return (
    <div className="gateway-code-block">
      <button className="icon-button subtle" onClick={() => void copyCode()} type="button" title={tx("复制代码")}>
        {copied ? <Check size={15} /> : <Copy size={15} />}
      </button>
      <pre><code>{code}</code></pre>
    </div>
  );
}
