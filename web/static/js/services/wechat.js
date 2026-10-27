import { api } from "../core/api.js";
import { auth } from "../core/session.js";

export function startWechatLogin() {
  return api("/api/wechat/login", { method: "POST" });
}

export function getWechatStatus(id) {
  return api(`/api/wechat/login/${encodeURIComponent(id)}`);
}

export function completeWechatLogin(id, payload) {
  return api(`/api/wechat/login/${encodeURIComponent(id)}/complete`, {
    method: "POST",
    body: payload,
  });
}

export async function cancelWechatLogin(id) {
  if (!id || (!auth.accessToken && !auth.refreshToken)) return;
  try {
    await api(`/api/wechat/login/${encodeURIComponent(id)}`, { method: "DELETE" });
  } catch (_) {
    /* 关闭弹窗时取消远端会话失败不影响本地 */
  }
}
