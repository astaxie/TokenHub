import { describe, expect, it } from "vitest";
import { viewFromPath } from "./formatting";
import { pluginDetailPath, pluginDetailRouteFromPath } from "./plugin-detail-route";

describe("plugin detail routes", () => {
  it("parses overview and secondary pages", () => {
    expect(pluginDetailRouteFromPath("/plugins/example.plugin")).toEqual({ pluginID: "example.plugin", section: "overview" });
    expect(pluginDetailRouteFromPath("/plugins/example.plugin/files")).toEqual({ pluginID: "example.plugin", section: "files" });
    expect(pluginDetailRouteFromPath("/plugins/example.plugin/settings/")).toEqual({ pluginID: "example.plugin", section: "settings" });
  });

  it("encodes and decodes plugin identifiers", () => {
    expect(pluginDetailPath("example plugin", "files")).toBe("/plugins/example%20plugin/files");
    expect(pluginDetailRouteFromPath("/plugins/example%20plugin/files")?.pluginID).toBe("example plugin");
  });

  it("rejects unrelated and malformed paths", () => {
    expect(pluginDetailRouteFromPath("/providers/example.plugin")).toBeNull();
    expect(pluginDetailRouteFromPath("/plugins/example.plugin/unknown")).toBeNull();
    expect(pluginDetailRouteFromPath("/plugins/%E0%A4%A")).toBeNull();
  });

  it("keeps plugin detail pages under the plugins navigation view", () => {
    expect(viewFromPath("/plugins/example.plugin")).toBe("plugins");
    expect(viewFromPath("/plugins/example.plugin/files")).toBe("plugins");
  });
});
