export type ResourceCreateTarget =
  | "provider-modal"
  | "project-workspace"
  | "notification-channel-modal"
  | "api-key-wizard"
  | "resource-modal";

export function resourceCreateTarget(view: string): ResourceCreateTarget {
  switch (view) {
    case "providers":
      return "provider-modal";
    case "projects":
      return "project-workspace";
    case "notification-channels":
      return "notification-channel-modal";
    case "api-keys":
      return "api-key-wizard";
    default:
      return "resource-modal";
  }
}
