export const notificationTranslations: Record<"en" | "ja", Record<string, string>> = {
  en: {
    "测试通知发送中…": "Sending test notification…",
    "发送测试通知": "Send test notification",
    "使用已保存的配置向目标发送一条测试通知": "Send a test notification to the destination using the saved configuration",
    "测试通知发送失败": "Failed to send test notification",
    "测试通知发送失败：{error}": "Failed to send test notification: {error}",
    "测试通知已提交，请到收件箱或目标渠道确认接收。": "Test notification submitted. Check the inbox or destination channel to confirm receipt.",
    "SMTP 加密": "SMTP Encryption",
    "auto：按服务器能力机会式升级 STARTTLS（legacy）；starttls：强制 STARTTLS，服务器不支持时拒绝发送（端口 587）；ssl：从第一个字节即隐式 TLS（端口 465）。": "auto: opportunistic STARTTLS upgrade when the server supports it (legacy); starttls: require STARTTLS and refuse to send if the server does not support it (port 587); ssl: implicit TLS from the first byte (port 465).",
  },
  ja: {
    "测试通知发送中…": "テスト通知を送信中…",
    "发送测试通知": "テスト通知を送信",
    "使用已保存的配置向目标发送一条测试通知": "保存済みの設定を使用して送信先にテスト通知を送信",
    "测试通知发送失败": "テスト通知の送信に失敗しました",
    "测试通知发送失败：{error}": "テスト通知の送信に失敗しました：{error}",
    "测试通知已提交，请到收件箱或目标渠道确认接收。": "テスト通知を送信しました。受信トレイまたは送信先チャンネルで受信を確認してください。",
    "SMTP 加密": "SMTP 暗号化",
    "auto：按服务器能力机会式升级 STARTTLS（legacy）；starttls：强制 STARTTLS，服务器不支持时拒绝发送（端口 587）；ssl：从第一个字节即隐式 TLS（端口 465）。": "auto: サーバーが対応している場合に STARTTLS へ機会的にアップグレード（legacy）；starttls: STARTTLS を必須とし、サーバーが非対応の場合は送信を拒否（ポート 587）；ssl: 最初のバイトから暗黙的 TLS（ポート 465）。",
  },
};
