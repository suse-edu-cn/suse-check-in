import { api, readJSON, refreshSession, setSessionExpiredHandler } from "../core/api.js";
import { auth, clearSession, loadSession, saveTokens, saveUser } from "../core/session.js";
import { state } from "../core/state.js";
import { $, initialOf } from "../core/utils.js";
import { closeAllMenus } from "../ui/menus.js";

let onAuthenticated = async () => {};

function resetClientState() {
  state.data = null;
  state.scheduleDirty = false;
  state.accountSignature = null;
  state.accountsSignature = null;
  state.runsSignature = null;
}

function closeDialogs() {
  for (const id of ["account-dialog", "sample-dialog"]) {
    const dialog = $(id);
    if (dialog && dialog.open) dialog.close();
  }
}

export function showLogin(message) {
  auth.ready = false;
  resetClientState();
  closeAllMenus();
  closeDialogs();
  $("boot").hidden = true;
  $("app").hidden = true;
  $("login-screen").hidden = false;
  $("login-password").value = "";
  const error = $("login-error");
  error.textContent = message || "";
  error.hidden = !message;
  window.setTimeout(() => $("login-account").focus(), 0);
}

export function showApp() {
  auth.ready = true;
  $("boot").hidden = true;
  $("login-screen").hidden = true;
  $("app").hidden = false;
  renderUser();
}

export function renderUser() {
  const user = auth.user;
  const name = (user && (user.name || user.username || user.student_id)) || "OAA 用户";
  const role = (user && user.role) || "已登录";
  $("user-avatar").textContent = initialOf(name);
  $("user-name").textContent = name;
  $("user-role").textContent = role;
  $("user-menu-name").textContent = name;
  const sub = [user && user.student_id, user && user.department, user && user.email].filter(Boolean).join(" · ");
  $("user-menu-sub").textContent = sub || "SUSE OAA";
  const trigger = $("user-menu-btn");
  trigger.title = sub ? `${name} · ${sub}` : name;
}

async function handleLogin(event) {
  event.preventDefault();
  const account = $("login-account").value.trim();
  const password = $("login-password").value;
  const error = $("login-error");
  error.hidden = true;
  if (!account || !password) {
    error.textContent = "请输入账号和密码";
    error.hidden = false;
    return;
  }
  const button = $("login-submit");
  button.disabled = true;
  button.textContent = "登录中…";
  try {
    const res = await fetch("/api/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ account, password, device: auth.device }),
    });
    const data = await readJSON(res);
    if (!res.ok) {
      error.textContent = (data && data.error) || `登录失败（HTTP ${res.status}）`;
      error.hidden = false;
      return;
    }
    saveTokens(data);
    saveUser(data.user);
    $("login-password").value = "";
    resetClientState();
    showApp();
    await onAuthenticated();
  } catch (err) {
    error.textContent = err.message || "无法连接 OAA 认证服务，请稍后重试";
    error.hidden = false;
  } finally {
    button.disabled = false;
    button.textContent = "登录";
  }
}

function logout() {
  const refreshToken = auth.refreshToken;
  const device = auth.device;
  clearSession();
  resetClientState();
  closeAllMenus();
  showLogin("已退出登录");
  if (!refreshToken) return;
  void fetch("/api/auth/logout", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ refresh_token: refreshToken, device }),
  }).catch(() => {
    /* 本地会话已清除 */
  });
}

async function boot() {
  loadSession();
  renderUser();
  if (!auth.accessToken && !auth.refreshToken) {
    showLogin();
    return;
  }
  try {
    if (!auth.accessToken && auth.refreshToken) {
      const result = await refreshSession();
      if (result === "invalid") {
        showLogin("登录已过期，请重新登录");
        return;
      }
      if (result !== "ok") {
        showLogin("续期登录状态失败，请稍后重试");
        return;
      }
    }
    const me = await api("/api/auth/me");
    if (me && me.user) saveUser(me.user);
    showApp();
    await onAuthenticated();
  } catch (err) {
    if (!auth.ready) showLogin(err.message || "验证登录状态失败，请稍后重试");
  }
}

export function initAuth(options = {}) {
  onAuthenticated = options.onAuthenticated || onAuthenticated;
  setSessionExpiredHandler(() => {
    showLogin("登录已过期，请重新登录");
  });
  $("login-form").addEventListener("submit", handleLogin);
  $("logout-btn").addEventListener("click", logout);
  boot();
}
