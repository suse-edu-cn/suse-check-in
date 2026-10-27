import { state } from "./state.js";

export const $ = (id) => document.getElementById(id);

const ESC_MAP = {
  "&": "&amp;",
  "<": "&lt;",
  ">": "&gt;",
  '"': "&quot;",
  "'": "&#39;",
};

export function esc(value) {
  return String(value == null ? "" : value).replace(/[&<>"']/g, (ch) => ESC_MAP[ch]);
}

export function formatTime(value) {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  const timezone = (state.data && state.data.timezone) || "Asia/Shanghai";
  try {
    return new Intl.DateTimeFormat("zh-CN", {
      timeZone: timezone,
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
      hour12: false,
    }).format(date);
  } catch (_) {
    return date.toLocaleString();
  }
}

export function shortTime(value) {
  const text = String(value || "");
  return /^\d{2}:\d{2}:\d{2}$/.test(text) ? text.slice(0, 5) : text;
}

export function initialOf(value) {
  const text = String(value || "?").trim();
  return text ? Array.from(text)[0].toUpperCase() : "?";
}

export function countdown(expiresAt) {
  if (!expiresAt) return "";
  const seconds = Math.max(0, Math.floor((new Date(expiresAt).getTime() - Date.now()) / 1000));
  const minutes = Math.floor(seconds / 60);
  const rest = String(seconds % 60).padStart(2, "0");
  return `${minutes}:${rest}`;
}
