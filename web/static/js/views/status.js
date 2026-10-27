import { state } from "../core/state.js";
import { $, formatTime, shortTime } from "../core/utils.js";

function countdownText(next) {
  const diff = new Date(next).getTime() - Date.now();
  if (!Number.isFinite(diff) || diff <= 0) return "即将执行";
  const totalMinutes = Math.round(diff / 60000);
  const days = Math.floor(totalMinutes / 1440);
  const hours = Math.floor((totalMinutes % 1440) / 60);
  const minutes = totalMinutes % 60;
  const parts = [];
  if (days) parts.push(`${days} 天`);
  if (hours) parts.push(`${hours} 小时`);
  if (!days && minutes) parts.push(`${minutes} 分`);
  if (!parts.length) parts.push("1 分");
  return `距今 ${parts.join(" ")}`;
}

export function renderStatus() {
  const data = state.data;
  if (!data) return;
  const progress = data.progress || {};
  const schedule = data.schedule || {};
  const scheduler = data.scheduler || {};
  const running = !!progress.running;
  const enabled = !!schedule.enabled;

  const pill = $("run-state");
  pill.classList.remove("running", "enabled", "off");
  if (running) {
    pill.textContent = "运行中";
    pill.classList.add("running");
  } else if (enabled) {
    pill.textContent = "已启用";
    pill.classList.add("enabled");
  } else {
    pill.textContent = "未启用";
    pill.classList.add("off");
  }

  const nextText = scheduler.nextRunText || "";
  const parts = nextText.split(" ");
  const datePart = parts.length > 1 ? parts[0] : "";
  const timePart = parts.length > 1 ? parts[1] : parts[0] || "";
  $("next-run").textContent = timePart ? timePart.slice(0, 5) : "—";

  const hints = [];
  if (datePart) hints.push(datePart);
  if (enabled && schedule.timeOfDay) hints.push(`每天 ${shortTime(schedule.timeOfDay)}`);
  if (enabled && scheduler.nextRun) hints.push(countdownText(scheduler.nextRun));
  $("next-run-hint").textContent = hints.join(" · ") || (enabled ? "等待调度" : "定时未启用");
  $("scheduler-message").textContent = scheduler.message || "—";

  const latest = (data.runs || [])[0] || null;
  $("last-result").textContent = (latest && latest.summary) || scheduler.lastResult || "暂无记录";

  const accounts = data.accounts || [];
  const results = (latest && latest.results) || [];
  const failed = results.filter((item) => !["success", "already_checked", "no_task"].includes(item.status)).length;
  $("stat-accounts").textContent = String(accounts.length);
  $("stat-result").textContent = results.length ? `${results.length - failed} 成功 / ${failed} 失败` : "—";
  $("stat-retry").textContent = `${schedule.maxRetries == null ? 0 : schedule.maxRetries} 次 · 间隔 ${
    schedule.retryIntervalMinutes == null ? 0 : schedule.retryIntervalMinutes
  } 分`;
  $("stat-notify").textContent = schedule.notifyEnabled ? schedule.notifyEmail || "已开启" : "未开启";

  const wrap = $("progress-wrap");
  const bar = $("progress-bar");
  if (running) {
    wrap.hidden = false;
    const total = Math.max(1, Number(progress.total) || 0);
    const done = Math.min(Number(progress.done) || 0, total);
    bar.value = (done / total) * 100;
    const current = progress.current ? ` · ${progress.current}` : "";
    $("progress-text").textContent = `进行中 ${done}/${total}${current}`;
  } else {
    wrap.hidden = true;
    bar.value = 0;
  }
  $("run-btn").disabled = running;
}

export function renderMeta() {
  const data = state.data;
  if (!data) return;
  $("meta-time").textContent = `服务器时间 ${formatTime(data.serverTime)}`;
}
