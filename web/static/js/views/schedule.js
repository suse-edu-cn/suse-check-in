import { api } from "../core/api.js";
import { auth } from "../core/session.js";
import { state, accountStatusTone, cleanStatus } from "../core/state.js";
import { $, esc, formatTime } from "../core/utils.js";
import { toast } from "../ui/feedback.js";

let reloadFn = async () => {};
let accountListSignature = "";
let summarySignature = null;
let saving = false;

function collectCheckedAccountIds() {
  return Array.from(document.querySelectorAll('#schedule-accounts input[type="checkbox"]:checked'))
    .map((input) => Number(input.value))
    .filter((id) => Number.isFinite(id) && id > 0);
}

export function scheduledAccountIds() {
  const schedule = (state.data && state.data.schedule) || {};
  return (schedule.accountIds || []).map(Number).filter((id) => Number.isFinite(id) && id > 0);
}

function onScheduleEdited() {
  state.scheduleDirty = true;
  $("schedule-dirty").hidden = false;
}

function renderNotificationSettings(schedule) {
  const available = !!(state.data && state.data.notificationsAvailable);
  const toggle = $("schedule-notify");
  const email = $("schedule-notify-email");
  if (!state.scheduleDirty) {
    toggle.checked = !!schedule.notifyEnabled;
    email.value = schedule.notifyEmail || (auth.user && auth.user.email) || "";
  }
  toggle.disabled = !available && !toggle.checked;
  email.disabled = !available || !toggle.checked;
  $("notify-hint").textContent = available
    ? "定时任务重试后仍失败时发送，共用服务端发信邮箱"
    : "服务端未配置 SMTP 发信邮箱，暂不可用";
}

function renderScheduleAccounts(serverIds) {
  const accounts = (state.data && state.data.accounts) || [];
  const box = $("schedule-accounts");
  const signature = accounts.map((a) => `${a.id}:${a.name || ""}:${a.loginType || ""}`).join("|");
  const selected = state.scheduleDirty ? new Set(collectCheckedAccountIds()) : new Set(serverIds);

  if (!accounts.length) {
    box.innerHTML = '<p class="empty">先添加打卡账号</p>';
    accountListSignature = signature;
    return;
  }
  if (signature === accountListSignature) {
    if (!state.scheduleDirty) {
      for (const input of box.querySelectorAll('input[type="checkbox"]')) {
        input.checked = selected.has(Number(input.value));
      }
    }
    return;
  }
  accountListSignature = signature;
  box.innerHTML = accounts
    .map((account) => {
      const type = account.loginType === "wechat" ? '<span class="mini-tag">扫码</span>' : "";
      return `<label class="check-item">
        <input type="checkbox" value="${esc(account.id)}" ${selected.has(account.id) ? "checked" : ""}>
        <span class="check-copy"><strong>${esc(account.name || account.studentId)}</strong><small>${esc(account.studentId)}</small></span>
        ${type}
      </label>`;
    })
    .join("");
}

function sessionText(account) {
  if (account.loginType === "wechat") return account.rememberValid ? "扫码会话有效" : "扫码会话已过期";
  return account.rememberValid ? "免密会话有效" : "每次需识别验证码";
}

function scheduleItemHTML(account) {
  const isWechat = account.loginType === "wechat";
  const tone = accountStatusTone(account.lastStatus);
  const status = account.lastStatus ? cleanStatus(account.lastStatus) : "尚未运行";
  const time = account.lastCheckinAt ? formatTime(account.lastCheckinAt) : "";
  const badges = [
    `<span class="badge ${isWechat ? "wechat" : "neutral"}">${isWechat ? "微信扫码" : "密码登录"}</span>`,
    `<span class="badge ${account.rememberValid ? "ok" : "warn"}">${esc(sessionText(account))}</span>`,
  ].join("");
  return `<div class="schedule-item">
    <span class="mini-dot ${tone}"></span>
    <div class="schedule-item-main">
      <div class="schedule-item-title">
        <strong>${esc(account.name || account.studentId)}</strong>
        ${badges}
      </div>
      <small>${esc(account.studentId)} · ${esc(account.campus || "未设置校区")} · ${esc(status)}${
        time ? `（${esc(time)}）` : ""
      }</small>
    </div>
  </div>`;
}

function renderScheduleCard(schedule) {
  const accounts = (state.data && state.data.accounts) || [];
  const chosen = new Set(schedule.accountIds || []);
  const rows = accounts.filter((account) => chosen.has(account.id));

  const badge = $("schedule-state");
  badge.textContent = schedule.enabled ? "已启用" : "未启用";
  badge.className = `badge ${schedule.enabled ? "ok" : "neutral"}`;
  $("schedule-list-count").textContent = `${rows.length} 个`;

  const signature = [
    rows
      .map((a) => [a.id, a.name, a.studentId, a.campus, a.loginType, a.lastStatus, a.lastCheckinAt, a.rememberValid].join(":"))
      .join("|"),
    accounts.length,
  ].join("#");
  if (signature === summarySignature) return;
  summarySignature = signature;

  const box = $("schedule-list");
  if (!rows.length) {
    box.innerHTML = `<div class="empty-state">
      <span class="empty-mark">⏱</span>
      <strong>${accounts.length ? "还没有勾选定时账号" : "还没有打卡账号"}</strong>
      <p>${accounts.length ? "点击“编辑定时”勾选需要自动打卡的账号。" : "先在右侧添加密码账号或微信扫码账号。"}</p>
    </div>`;
    return;
  }
  box.innerHTML = rows.map(scheduleItemHTML).join("");
}

export function renderSchedule() {
  const schedule = (state.data && state.data.schedule) || {};
  renderScheduleCard(schedule);
  if (!$("schedule-dialog").open) return;
  if (!state.scheduleDirty) {
    $("schedule-enabled").checked = !!schedule.enabled;
    const timeInput = $("schedule-time");
    timeInput.value = schedule.timeOfDay || "07:00:00";
    if (!timeInput.value) timeInput.value = (schedule.timeOfDay || "07:00:00").slice(0, 5);
    $("schedule-retries").value = schedule.maxRetries == null ? 3 : schedule.maxRetries;
    $("schedule-interval").value = schedule.retryIntervalMinutes == null ? 5 : schedule.retryIntervalMinutes;
    $("schedule-dirty").hidden = true;
  }
  renderNotificationSettings(schedule);
  renderScheduleAccounts(schedule.accountIds || []);
}

function openScheduleDialog() {
  state.scheduleDirty = false;
  summarySignature = null;
  $("schedule-dialog").showModal();
  renderSchedule();
  $("schedule-time").focus();
}

function closeScheduleDialog() {
  // 浏览器的 close 事件是异步派发的，主动关闭时先把脏状态清掉，
  // 否则下次打开弹窗会残留“未保存”提示。
  state.scheduleDirty = false;
  $("schedule-dirty").hidden = true;
  $("schedule-dialog").close();
}

async function saveSchedule(event) {
  event.preventDefault();
  if (saving) return;
  const accountIds = collectCheckedAccountIds();
  const enabled = $("schedule-enabled").checked;
  const timeOfDay = $("schedule-time").value;
  const maxRetries = Number($("schedule-retries").value);
  const retryIntervalMinutes = Number($("schedule-interval").value);
  const notifyEnabled = $("schedule-notify").checked;
  const notifyEmail = $("schedule-notify-email").value.trim();

  if (!timeOfDay) {
    toast("请填写执行时间", "bad");
    return;
  }
  if (!Number.isInteger(maxRetries) || maxRetries < 0 || maxRetries > 10) {
    toast("失败重试次数需在 0 到 10 之间", "bad");
    return;
  }
  if (!Number.isInteger(retryIntervalMinutes) || retryIntervalMinutes < 1 || retryIntervalMinutes > 60) {
    toast("重试间隔需在 1 到 60 分钟之间", "bad");
    return;
  }
  if (enabled && accountIds.length === 0) {
    toast("启用定时前请至少选择一个账号", "bad");
    return;
  }
  if (notifyEnabled) {
    if (!(state.data && state.data.notificationsAvailable)) {
      toast("服务端未配置 SMTP 发信邮箱，暂不能开启通知", "bad");
      return;
    }
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(notifyEmail)) {
      toast("请填写有效的通知邮箱", "bad");
      return;
    }
  }

  const saveButton = $("schedule-save");
  const originalText = saveButton.textContent;
  saving = true;
  saveButton.disabled = true;
  saveButton.textContent = "保存中…";
  try {
    await api("/api/schedule", {
      method: "PUT",
      body: { enabled, timeOfDay, accountIds, maxRetries, retryIntervalMinutes, notifyEnabled, notifyEmail },
    });
    state.scheduleDirty = false;
    $("schedule-dirty").hidden = true;
    closeScheduleDialog();
    toast("定时设置已保存");
    await reloadFn();
  } catch (err) {
    toast(err.message, "bad");
  } finally {
    saving = false;
    saveButton.disabled = false;
    saveButton.textContent = originalText;
  }
}

export function initSchedule(options = {}) {
  reloadFn = options.reload || reloadFn;
  $("schedule-edit-btn").addEventListener("click", openScheduleDialog);
  $("schedule-cancel").addEventListener("click", closeScheduleDialog);
  $("schedule-close-x").addEventListener("click", closeScheduleDialog);
  $("schedule-form").addEventListener("input", onScheduleEdited);
  $("schedule-form").addEventListener("change", onScheduleEdited);
  $("schedule-notify").addEventListener("change", () => {
    const toggle = $("schedule-notify");
    const email = $("schedule-notify-email");
    email.disabled = !toggle.checked || !(state.data && state.data.notificationsAvailable);
    if (toggle.checked && !email.value && auth.user && auth.user.email) {
      email.value = auth.user.email;
    }
  });
  $("schedule-form").addEventListener("submit", saveSchedule);
  $("schedule-dialog").addEventListener("close", () => {
    state.scheduleDirty = false;
    $("schedule-dirty").hidden = true;
  });
}
