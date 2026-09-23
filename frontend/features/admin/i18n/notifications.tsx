export const notificationTranslations: Record<"en" | "ja", Record<string, string>> = {
  en: {
    "SMTP 加密": "SMTP Encryption",
    "auto：按服务器能力机会式升级 STARTTLS（legacy）；starttls：强制 STARTTLS，服务器不支持时拒绝发送（端口 587）；ssl：从第一个字节即隐式 TLS（端口 465）。": "auto: opportunistic STARTTLS upgrade when the server supports it (legacy); starttls: require STARTTLS and refuse to send if the server does not support it (port 587); ssl: implicit TLS from the first byte (port 465).",
  },
  ja: {
    "SMTP 加密": "SMTP 暗号化",
    "auto：按服务器能力机会式升级 STARTTLS（legacy）；starttls：强制 STARTTLS，服务器不支持时拒绝发送（端口 587）；ssl：从第一个字节即隐式 TLS（端口 465）。": "auto: サーバーが対応している場合に STARTTLS へ機会的にアップグレード（legacy）；starttls: STARTTLS を必須とし、サーバーが非対応の場合は送信を拒否（ポート 587）；ssl: 最初のバイトから暗黙的 TLS（ポート 465）。",
  },
};
