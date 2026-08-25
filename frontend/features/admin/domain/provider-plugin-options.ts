export const providerPluginOptionPrefix = "plugin_option:";

export function providerPluginOptionFieldKey(pluginID: string, name: string) {
  return `${providerPluginOptionPrefix}${pluginID}:${name}`;
}

export function providerPluginOptionValues(values: Record<string, string>) {
  const options: Record<string, string> = {};
  for (const [key, value] of Object.entries(values)) {
    if (!key.startsWith(providerPluginOptionPrefix)) continue;
    const optionKey = key.slice(providerPluginOptionPrefix.length).split(":").slice(1).join(":").trim();
    if (!optionKey) continue;
    options[optionKey] = value;
  }
  return options;
}

export function providerPluginOptionValuesForPlugin(values: Record<string, string>, pluginID: string) {
  const options: Record<string, string> = {};
  const prefix = `${providerPluginOptionPrefix}${pluginID}:`;
  for (const [key, value] of Object.entries(values)) {
    if (!key.startsWith(prefix)) continue;
    const optionKey = key.slice(prefix.length).trim();
    if (!optionKey) continue;
    options[optionKey] = value;
  }
  return options;
}
