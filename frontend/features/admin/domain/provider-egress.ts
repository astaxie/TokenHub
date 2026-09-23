export function providerEgressTestPayload(providerID: string, values: Record<string, string>) {
  return {
    provider_id: providerID,
    fields: Object.fromEntries(Object.entries(values).filter(([key]) => key.startsWith("provider_"))),
  };
}
