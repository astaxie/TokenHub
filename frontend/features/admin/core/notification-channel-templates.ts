export type NotificationChannelTemplateOwner = "core_extension" | "plugin";

export type NotificationChannelTemplate = {
  type: string;
  label: string;
  description: string;
  urlPlaceholder: string;
  owner: NotificationChannelTemplateOwner;
};

export const notificationChannelTemplates: NotificationChannelTemplate[] = [
  {
    type: "webhook",
    label: "Webhook",
    description: "通用 Webhook 告警通知",
    urlPlaceholder: "http://localhost:8081/tokenhub-alert",
    owner: "core_extension",
  },
  {
    type: "slack",
    label: "Slack",
    description: "Slack Bot 告警通知",
    urlPlaceholder: "https://hooks.slack.com/services/T00000000/B00000000/XXXXXXXXXXXXXXXXXXXXXXXX",
    owner: "core_extension",
  },
  {
    type: "discord",
    label: "Discord",
    description: "Discord Webhook 告警通知",
    urlPlaceholder: "https://discord.com/api/webhooks/000000000000000000/XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
    owner: "core_extension",
  },
  {
    type: "telegram",
    label: "Telegram",
    description: "Telegram Bot 告警通知",
    urlPlaceholder: "Telegram Bot Token + Chat ID",
    owner: "core_extension",
  },
  {
    type: "whatsapp",
    label: "WhatsApp",
    description: "WhatsApp Cloud API 告警通知",
    urlPlaceholder: "WhatsApp Phone Number ID + Access Token",
    owner: "core_extension",
  },
  {
    type: "feishu",
    label: "Feishu",
    description: "飞书机器人告警通知",
    urlPlaceholder: "https://open.feishu.cn/open-apis/bot/v2/hook/xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
    owner: "core_extension",
  },
  {
    type: "dingtalk",
    label: "DingTalk",
    description: "钉钉机器人告警通知",
    urlPlaceholder: "https://oapi.dingtalk.com/robot/send?access_token=xxxxxxxx",
    owner: "core_extension",
  },
  {
    type: "wecom",
    label: "WeCom",
    description: "企业微信机器人告警通知",
    urlPlaceholder: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx",
    owner: "core_extension",
  },
  {
    type: "email",
    label: "Email",
    description: "SMTP 告警通知",
    urlPlaceholder: "smtp.example.com",
    owner: "core_extension",
  },
];

export const notificationChannelTypes = notificationChannelTemplates.map((template) => template.type);
export const notificationChannelDefaultType = notificationChannelTemplates[0]?.type ?? "webhook";
