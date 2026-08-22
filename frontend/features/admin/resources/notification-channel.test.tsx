import { describe, expect, it } from "vitest";
import { notificationChannelDefaults, notificationChannelPayload } from "./payloads";
import { notificationChannelConfig } from "./settings-config";

const emailValues = (overrides: Record<string, string> = {}) => ({
  name: "Email Alert",
  type: "email",
  smtp_host: "smtp.example.com",
  smtp_port: "587",
  smtp_encryption: "auto",
  smtp_username: "tokenhub@example.com",
  smtp_password: "",
  smtp_from: "tokenhub@example.com",
  email_to: "ops@example.com",
  ...overrides,
});

function payloadFields(values: Record<string, string>, existing?: Record<string, unknown>) {
  return (notificationChannelPayload(emailValues(values), existing as never).fields ?? {}) as Record<string, unknown>;
}

describe("notification channel SMTP encryption round trip", () => {
  it("defaults new email channels to explicit starttls", () => {
    const defaults = notificationChannelDefaults("email");
    expect(defaults.smtp_encryption).toBe("starttls");
  });

  it("persists starttls so the backend can fail closed", () => {
    const fields = payloadFields({ smtp_encryption: "starttls" });
    expect(fields.smtp_encryption).toBe("starttls");
  });

  it("persists ssl for implicit TLS", () => {
    const fields = payloadFields({ smtp_encryption: "ssl" });
    expect(fields.smtp_encryption).toBe("ssl");
  });

  it("leaves smtp_encryption unset for legacy auto mode", () => {
    const fields = payloadFields({ smtp_encryption: "auto" });
    expect(fields).not.toHaveProperty("smtp_encryption");
  });
});

describe("notificationChannelConfig().toForm", () => {
  it("maps a missing stored field back to auto", () => {
    const toForm = notificationChannelConfig().toForm;
    if (!toForm) throw new Error("expected toForm");
    const form = toForm({ fields: {} } as never);
    expect(form.smtp_encryption).toBe("auto");
  });

  it("round-trips explicit starttls", () => {
    const toForm = notificationChannelConfig().toForm;
    if (!toForm) throw new Error("expected toForm");
    const form = toForm({ fields: { smtp_encryption: "starttls" } } as never);
    expect(form.smtp_encryption).toBe("starttls");
  });

  it("round-trips explicit ssl", () => {
    const toForm = notificationChannelConfig().toForm;
    if (!toForm) throw new Error("expected toForm");
    const form = toForm({ fields: { smtp_encryption: "ssl" } } as never);
    expect(form.smtp_encryption).toBe("ssl");
  });
});
