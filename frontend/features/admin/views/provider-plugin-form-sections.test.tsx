import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { providerPluginOptionFieldKey } from "../domain/provider-plugin-options";
import { providerPayload } from "../resources/payloads";
import { ProviderPluginFormSections } from "./provider-plugin-form-sections";

describe("ProviderPluginFormSections", () => {
  it("renders matching provider form sections and preserves existing provider options", async () => {
    const user = userEvent.setup();
    const updateSpy = vi.fn();
    const tenantKey = providerPluginOptionFieldKey("tokenhub.provider.plugin", "tenant_id");
    const enabledKey = providerPluginOptionFieldKey("tokenhub.provider.plugin", "cache_enabled");

    function Harness() {
      const [values, setValues] = useState<Record<string, string>>({ type: "plugin_provider" });
      function onUpdate(key: string, value: string) {
        updateSpy(key, value);
        setValues((current) => ({ ...current, [key]: value }));
      }
      return (
        <ProviderPluginFormSections
          contributions={[
            {
              plugin_id: "tokenhub.provider.plugin",
              id: "account",
              slot: "provider.form.section",
              title: "Plugin Account",
              provider_types: ["plugin_provider"],
              schema: {
                fields: [
                  { name: "tenant_id", type: "text", label: "Tenant ID", required: true },
                  { name: "cache_enabled", type: "switch", label: "Cache Enabled" },
                ],
              },
            },
            {
              plugin_id: "tokenhub.provider.other",
              id: "account",
              slot: "provider.form.section",
              title: "Other Account",
              provider_types: ["other_provider"],
              schema: { fields: [{ name: "tenant_id", type: "text", label: "Other Tenant" }] },
            },
          ]}
          onUpdate={onUpdate}
          provider={{
            id: "prv_plugin",
            name: "Plugin Provider",
            type: "plugin_provider",
            status: "active",
            healthy: true,
            priority: 10,
            options: { tenant_id: "tenant-existing", cache_enabled: "true" },
          }}
          values={values}
        />
      );
    }

    render(<Harness />);

    expect(screen.getByText("Plugin Account")).toBeInTheDocument();
    expect(screen.queryByText("Other Account")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Tenant ID")).toHaveValue("tenant-existing");
    expect(screen.getByText("开启").closest("button")).toHaveAttribute("aria-checked", "true");

    await waitFor(() => {
      expect(updateSpy).toHaveBeenCalledWith(tenantKey, "tenant-existing");
      expect(updateSpy).toHaveBeenCalledWith(enabledKey, "true");
    });

    await user.clear(screen.getByLabelText("Tenant ID"));
    await user.type(screen.getByLabelText("Tenant ID"), "tenant-updated");
    expect(updateSpy).toHaveBeenLastCalledWith(tenantKey, "tenant-updated");
  });

  it("writes plugin form values into the provider options payload", () => {
    const tenantKey = providerPluginOptionFieldKey("tokenhub.provider.plugin", "tenant_id");
    const cacheKey = providerPluginOptionFieldKey("tokenhub.provider.plugin", "cache_enabled");
    const payload = providerPayload({
      id: "prv_plugin",
      name: "Plugin Provider",
      type: "plugin_provider",
      status: "active",
      healthy: "true",
      priority: "10",
      base_url: "https://provider.example/v1",
      api_key: "secret",
      [tenantKey]: "tenant-updated",
      [cacheKey]: "false",
    });

    expect(payload.options).toMatchObject({
      tenant_id: "tenant-updated",
      cache_enabled: "false",
    });
  });

  it("hydrates plugin form defaults into the provider options payload", async () => {
    const updateSpy = vi.fn();
    const tenantKey = providerPluginOptionFieldKey("tokenhub.provider.plugin", "tenant_id");
    const modeKey = providerPluginOptionFieldKey("tokenhub.provider.plugin", "routing_mode");
    const enabledKey = providerPluginOptionFieldKey("tokenhub.provider.plugin", "cache_enabled");

    function Harness() {
      const [values, setValues] = useState<Record<string, string>>({ type: "plugin_provider" });
      function onUpdate(key: string, value: string) {
        updateSpy(key, value);
        setValues((current) => ({ ...current, [key]: value }));
      }
      return (
        <ProviderPluginFormSections
          contributions={[{
            plugin_id: "tokenhub.provider.plugin",
            id: "defaults",
            slot: "provider.form.section",
            title: "Plugin Defaults",
            provider_types: ["plugin_provider"],
            schema: {
              fields: [
                { name: "tenant_id", type: "text", label: "Tenant ID", default: "tenant-default" },
                { name: "routing_mode", type: "select", label: "Routing Mode", options: ["balanced", "strict"], default_value: "balanced" },
                { name: "cache_enabled", type: "switch", label: "Cache Enabled", default: true },
              ],
            },
          }]}
          onUpdate={onUpdate}
          values={values}
        />
      );
    }

    render(<Harness />);

    await waitFor(() => {
      expect(updateSpy).toHaveBeenCalledWith(tenantKey, "tenant-default");
      expect(updateSpy).toHaveBeenCalledWith(modeKey, "balanced");
      expect(updateSpy).toHaveBeenCalledWith(enabledKey, "true");
    });

    const payload = providerPayload({
      id: "prv_plugin",
      name: "Plugin Provider",
      type: "plugin_provider",
      status: "active",
      healthy: "true",
      priority: "10",
      base_url: "https://provider.example/v1",
      api_key: "secret",
      [tenantKey]: "tenant-default",
      [modeKey]: "balanced",
      [enabledKey]: "true",
    });
    expect(payload.options).toMatchObject({
      tenant_id: "tenant-default",
      routing_mode: "balanced",
      cache_enabled: "true",
    });
  });
});
