export const state = {
  data: null,
  scheduleDirty: false,
  accountSignature: null,
  accountsSignature: null,
  runsSignature: null,
  editingId: null,
  polling: false,
};

export const STATUS_LABELS = {
  success: "签到成功",
  already_checked: "已签到",
  no_task: "无任务",
  captcha_failed: "验证码失败",
  login_failed: "登录失败",
  session_expired: "扫码会话已过期",
  sms_required: "需短信验证",
  checkin_failed: "签到失败",
  location_missing: "缺少定位",
  error: "异常",
};

export function statusLabel(status) {
  return STATUS_LABELS[status] || status || "未知";
}

export function statusClass(status) {
  return ["success", "already_checked", "no_task"].includes(status) ? "ok" : "bad";
}

export function accountStatusTone(status) {
  if (!status) return "idle";
  if (status.includes("失败") || status.includes("异常") || status.includes("过期")) return "bad";
  if (status.includes("成功")) return "ok";
  return "idle";
}

export function cleanStatus(status) {
  return String(status || "")
    .replace(/^(成功|失败)[：:]\s*/, "")
    .trim();
}
