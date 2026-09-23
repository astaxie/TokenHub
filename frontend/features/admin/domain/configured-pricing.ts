export function configuredPriceFormValue(value?: number, configured?: boolean) {
  if (configured || (value ?? 0) !== 0) return String(value ?? 0);
  return "";
}

export function configuredPriceEntered(value?: string) {
  return value?.trim() !== "";
}
