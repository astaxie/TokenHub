import type { AdminUser, ApiContext, AppData, ModalState, ViewKey } from "./types";

export type ResourceAction<T> = {
  label: string;
  title?: string;
  visible?: (item: T, currentUser: AdminUser | null, data: AppData) => boolean;
  href?: (item: T) => string;
  navigate?: (item: T) => ViewKey;
  open?: (item: T) => void;
  confirmation?: { title: string; message: (item: T) => string; confirmLabel: string };
  run?: (ctx: ApiContext, item: T, data: AppData) => Promise<void>;
  modal?: (item: T, data: AppData) => ModalState<any>;
  doneMessage?: (item: T) => string;
};
