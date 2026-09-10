export const providerPluginOptionPrefix = "plugin_option:";

export function providerPluginOptionFieldKey(pluginID: string, name: string) {
  return `${providerPluginOptionPrefix}${encodeURIComponent(pluginID)}:${encodeURIComponent(name)}`;
}

export function providerPluginOptionValues(values: Record<string, string>) {
  const options: Record<string, string> = {};
  for (const [key, value] of Object.entries(values)) {
    if (!key.startsWith(providerPluginOptionPrefix)) continue;
    const encoded = key.slice(providerPluginOptionPrefix.length);
    const separator = encoded.indexOf(":");
    if (separator < 1) continue;
    const pluginID = decodePluginOptionComponent(encoded.slice(0, separator))?.trim();
    const optionKey = decodePluginOptionComponent(encoded.slice(separator + 1))?.trim();
    if (!pluginID || !optionKey) continue;
    options[optionKey] = value;
  }
  return options;
}

export function providerPluginOptionValuesForPlugin(values: Record<string, string>, pluginID: string) {
  const options: Record<string, string> = {};
  const prefix = `${providerPluginOptionPrefix}${encodeURIComponent(pluginID)}:`;
  for (const [key, value] of Object.entries(values)) {
    if (!key.startsWith(prefix)) continue;
    const optionKey = decodePluginOptionComponent(key.slice(prefix.length))?.trim();
    if (!optionKey) continue;
    options[optionKey] = value;
  }
  return options;
}

function decodePluginOptionComponent(value: string) {
  try {
    return decodeURIComponent(value);
  } catch {
    return undefined;
  }
}
