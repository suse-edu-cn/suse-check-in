import { auth, clearSession, saveTokens } from "./session.js";
import { notifyError } from "../ui/feedback.js";

export class ApiError extends Error {
  constructor(message, status = 0) {
    super(message);
    this.name = "ApiError";
    this.status = status;
  }
}

let sessionExpiredHandler = null;

export function setSessionExpiredHandler(handler) {
  sessionExpiredHandler = handler;
}

function isAuthCall(path) {
  return path === "/api/auth/login" || path === "/api/auth/refresh" || path === "/api/auth/logout";
}

export async function readJSON(res) {
  const text = await res.text();
  if (!text) return null;
  try {
    return JSON.parse(text);
  } catch (_) {
    return null;
  }
}

function expireSession() {
  clearSession();
  if (sessionExpiredHandler) sessionExpiredHandler();
}

export async function refreshSession() {
  if (!auth.refreshToken) return "invalid";
  if (auth.refreshing) return auth.refreshing;
  auth.refreshing = (async () => {
    try {
      const res = await fetch("/api/auth/refresh", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ refresh_token: auth.refreshToken }),
      });
      const data = await readJSON(res);
      if (res.status === 400 || res.status === 401) {
        clearSession();
        return "invalid";
      }
      if (!res.ok) {
        throw new Error((data && data.error) || `续期登录状态失败（HTTP ${res.status}）`);
      }
      saveTokens(data);
      return "ok";
    } catch (err) {
      notifyError(err.message || "续期登录状态失败，请稍后重试");
      return "error";
    } finally {
      auth.refreshing = null;
    }
  })();
  return auth.refreshing;
}

export async function rawRequest(path, options = {}, retry = true) {
  const headers = { ...(options.headers || {}) };
  if (auth.accessToken) headers.Authorization = `Bearer ${auth.accessToken}`;
  const opts = {
    method: options.method || "GET",
    headers,
  };
  if (options.body !== undefined) {
    headers["Content-Type"] = "application/json";
    opts.body = typeof options.body === "string" ? options.body : JSON.stringify(options.body);
  }
  const res = await fetch(path, opts);
  if (res.status === 401 && retry && !isAuthCall(path)) {
    const result = await refreshSession();
    if (result === "ok") return rawRequest(path, options, false);
    if (result === "invalid") {
      expireSession();
      throw new ApiError("登录已过期，请重新登录", 401);
    }
    throw new ApiError("续期登录状态失败，请稍后重试", 0);
  }
  return res;
}

export async function api(path, options = {}) {
  const res = await rawRequest(path, options);
  const data = await readJSON(res);
  if (!res.ok) {
    if (res.status === 401) expireSession();
    throw new ApiError((data && data.error) || `请求失败（HTTP ${res.status}）`, res.status);
  }
  return data;
}
