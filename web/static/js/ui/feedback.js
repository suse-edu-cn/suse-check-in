import { $ } from "../core/utils.js";

let toastTimer = 0;
let lastErrorAt = 0;

export function toast(message, kind = "ok") {
  const el = $("toast");
  if (!el) return;
  el.textContent = message;
  el.className = kind === "bad" ? "toast bad" : "toast";
  el.hidden = false;
  window.clearTimeout(toastTimer);
  toastTimer = window.setTimeout(() => {
    el.hidden = true;
  }, 3800);
}

export function notifyError(message) {
  const now = Date.now();
  if (now - lastErrorAt < 6000) return;
  lastErrorAt = now;
  toast(message || "请求失败", "bad");
}
