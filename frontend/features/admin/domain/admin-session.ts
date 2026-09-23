export type StoredAdminSession<TUser> = {
  baseURL: string;
  token: string;
  user: TUser;
  expiresAt: string;
};

export type SessionStorage = Pick<Storage, "getItem" | "removeItem" | "setItem">;

export function readTransientAdminSession<TUser>(
  storageKey: string,
  sessionStore: SessionStorage,
  legacyPersistentStore: SessionStorage,
  now = Date.now(),
): StoredAdminSession<TUser> | null {
  legacyPersistentStore.removeItem(storageKey);
  const raw = sessionStore.getItem(storageKey);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as StoredAdminSession<TUser>;
    if (!parsed.token || !parsed.user || new Date(parsed.expiresAt).getTime() <= now) {
      sessionStore.removeItem(storageKey);
      return null;
    }
    return parsed;
  } catch {
    sessionStore.removeItem(storageKey);
    return null;
  }
}

export function saveTransientAdminSession<TUser>(
  storageKey: string,
  session: StoredAdminSession<TUser>,
  sessionStore: SessionStorage,
  legacyPersistentStore: SessionStorage,
) {
  legacyPersistentStore.removeItem(storageKey);
  sessionStore.setItem(storageKey, JSON.stringify(session));
}

export function clearTransientAdminSession(
  storageKey: string,
  sessionStore: SessionStorage,
  legacyPersistentStore: SessionStorage,
) {
  sessionStore.removeItem(storageKey);
  legacyPersistentStore.removeItem(storageKey);
}
