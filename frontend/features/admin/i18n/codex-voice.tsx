export const codexVoiceTranslations: Record<"en" | "ja", Record<string, string>> = {
  en: {
    "显式能力": "Explicit Capabilities",
    "敏感能力默认关闭；项目与 Key 必须同时显式允许。": "Sensitive capabilities are disabled by default and must be explicitly allowed by both the project and the Key.",
    "只有项目和此 Key 都允许时才能使用；新 Key 默认关闭。": "The capability is available only when both the project and this Key allow it. New Keys default to disabled.",
    "未启用": "Disabled",
  },
  ja: {
    "显式能力": "明示的な機能",
    "敏感能力默认关闭；项目与 Key 必须同时显式允许。": "機密性の高い機能は既定で無効になり、Project と Key の両方で明示的な許可が必要です。",
    "只有项目和此 Key 都允许时才能使用；新 Key 默认关闭。": "Project とこの Key の両方で許可した場合のみ利用できます。新しい Key では既定で無効です。",
    "未启用": "無効",
  },
};
