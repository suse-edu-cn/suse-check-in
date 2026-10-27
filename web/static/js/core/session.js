const STORAGE_KEYS = {
  access: "suse_checkin_access_token",
  refresh: "suse_checkin_refresh_token",
  device: "suse_checkin_device",
  user: "suse_checkin_user",
};

export const auth = {
  accessToken: "",
  refreshToken: "",
  device: "",
  user: null,
  ready: false,
  refreshing: null,
};

function storageGet(key) {
  try {
    return window.localStorage.getItem(key) || "";
  } catch (_) {
    return "";
  }
}

function storageSet(key, value) {
  try {
    window.localStorage.setItem(key, value);
  } catch (_) {
    /* 仅保留内存会话 */
  }
}

function storageRemove(key) {
  try {
    window.localStorage.removeItem(key);
  } catch (_) {
    /* ignore */
  }
}

function newDeviceId() {
  const bytes = new Uint8Array(5);
  if (window.crypto && window.crypto.getRandomValues) {
    window.crypto.getRandomValues(bytes);
  } else {
    for (let i = 0; i < bytes.length; i += 1) bytes[i] = Math.floor(Math.random() * 256);
  }
  return "checkin-" + Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
}

export function loadSession() {
  auth.accessToken = storageGet(STORAGE_KEYS.access);
  auth.refreshToken = storageGet(STORAGE_KEYS.refresh);
  auth.device = storageGet(STORAGE_KEYS.device) || newDeviceId();
  storageSet(STORAGE_KEYS.device, auth.device);
  const cachedUser = storageGet(STORAGE_KEYS.user);
  if (cachedUser) {
    try {
      auth.user = JSON.parse(cachedUser);
    } catch (_) {
      auth.user = null;
    }
  }
}

export function saveTokens(data) {
  if (data && data.token) {
    auth.accessToken = data.token;
    storageSet(STORAGE_KEYS.access, data.token);
  }
  if (data && data.refresh_token) {
    auth.refreshToken = data.refresh_token;
    storageSet(STORAGE_KEYS.refresh, data.refresh_token);
  }
}

export function saveUser(user) {
  auth.user = user || null;
  if (user) storageSet(STORAGE_KEYS.user, JSON.stringify(user));
  else storageRemove(STORAGE_KEYS.user);
}

export function clearSession() {
  auth.accessToken = "";
  auth.refreshToken = "";
  auth.user = null;
  auth.ready = false;
  storageRemove(STORAGE_KEYS.access);
  storageRemove(STORAGE_KEYS.refresh);
  storageRemove(STORAGE_KEYS.user);
}
