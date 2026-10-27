import { api } from "./core/api.js";
import { auth } from "./core/session.js";
import { state } from "./core/state.js";
import { $ } from "./core/utils.js";
import { notifyError, toast } from "./ui/feedback.js";
import { initMenus } from "./ui/menus.js";
import { initAccounts, renderAccounts, runAccounts } from "./views/accounts.js";
import { initAuth } from "./views/auth.js";
import { initRuns, renderRuns } from "./views/runs.js";
import { initSchedule, renderSchedule, scheduledAccountIds } from "./views/schedule.js";
import { renderMeta, renderStatus } from "./views/status.js";

async function loadState() {
  if (!auth.ready || state.polling) return;
  state.polling = true;
  try {
    state.data = await api("/api/state");
    renderStatus();
    renderMeta();
    renderAccounts();
    renderSchedule();
    renderRuns();
  } catch (err) {
    if (auth.ready) notifyError(err.message);
  } finally {
    state.polling = false;
  }
}

function init() {
  initMenus();
  initAccounts({ reload: loadState });
  initSchedule({ reload: loadState });
  initRuns();

  $("refresh-btn").addEventListener("click", loadState);
  $("run-btn").addEventListener("click", () => {
    const ids = scheduledAccountIds();
    if (!ids.length) {
      toast("请先在定时设置里勾选账号", "bad");
      return;
    }
    void runAccounts(ids);
  });

  initAuth({ onAuthenticated: loadState });

  window.setInterval(() => {
    if (auth.ready && !document.hidden) void loadState();
  }, 2500);
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", init);
} else {
  init();
}
