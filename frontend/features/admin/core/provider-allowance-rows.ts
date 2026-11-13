export type AllowanceFieldRow<T> = { id: string; name: string; value: T };

export function allowanceFieldRows<T>(
  values?: Record<string, T>,
): AllowanceFieldRow<T>[] {
  return Object.entries(values ?? {}).map(([name, value]) => ({
    id: crypto.randomUUID(),
    name,
    value,
  }));
}
