import { api } from "../core/api.js";
import { state, accountStatusTone, cleanStatus } from "../core/state.js";
import { $, esc, formatTime, initialOf } from "../core/utils.js";
import { toast } from "../ui/feedback.js";
import { cancelWechatLogin, completeWechatLogin, getWechatStatus, startWechatLogin } from "../services/wechat.js";

let reloadFn = async () => {};
let accountsSignature = null;
let editingAccount = null;
let dialogMode = "password";
let scanTimer = 0;
let saving = false;
const scan = {
  id: "",
  status: "idle",
  session: null,
  message: "",
};

function campusSummary(name) {
  const campuses = (state.data && state.data.campuses) || [];
  return campuses.find((campus) => campus.name === name) || { name: name || "未设置校区", count: 0 };
}

function accountInitial(account) {
  return initialOf(account.name || account.studentId || "?");
}

function accountStatusText(account) {
  if (!account.lastStatus) return "尚未运行";
  const time = account.lastCheckinAt ? ` · ${formatTime(account.lastCheckinAt)}` : "";
  return `${cleanStatus(account.lastStatus)}${time}`;
}

function accountCard(account) {
  const isWechat = account.loginType === "wechat";
  const campus = campusSummary(account.campus);
  const tone = `tone-${Number(account.id) % 5}`;
  const statusTone = accountStatusTone(account.lastStatus);
  const typeBadge = `<span class="badge ${isWechat ? "wechat" : "neutral"}">${isWechat ? "微信扫码" : "密码登录"}</span>`;
  let sessionBadge = "";
  if (isWechat) {
    sessionBadge = account.rememberValid
      ? '<span class="badge ok">扫码会话有效</span>'
      : '<span class="badge bad">扫码会话已过期</span>';
  } else if (account.hasRememberMe) {
    sessionBadge = account.rememberValid
      ? '<span class="badge ok">免密会话有效</span>'
      : '<span class="badge neutral">免密会话已过期</span>';
  }
  const quickAction =
    isWechat && !account.rememberValid
      ? `<button class="btn tiny primary" type="button" data-action="rescan" data-id="${esc(account.id)}">重新扫码</button>`
      : `<button class="btn tiny ghost" type="button" data-action="run" data-id="${esc(account.id)}">运行</button>`;
  const sessionMeta =
    isWechat && account.rememberExpiresAt
      ? ` · 会话至 ${esc(formatTime(account.rememberExpiresAt))}`
      : "";
  return `<div class="account">
    <div class="avatar ${tone}">${esc(accountInitial(account))}</div>
    <div class="account-main">
      <div class="account-title">
        <strong>${esc(account.name || account.studentId)}</strong>
        <span class="badge neutral">${esc(account.studentId)}</span>
        ${typeBadge}
        ${sessionBadge}
      </div>
      <p class="account-meta">${esc(campus.name)} · ${esc(campus.count)} 个内置点位${sessionMeta}</p>
      <p class="account-status"><span class="mini-dot ${statusTone}"></span><span>${esc(accountStatusText(account))}</span></p>
    </div>
    <div class="account-actions">
      ${quickAction}
      <div class="dropdown">
        <button class="icon-btn small" type="button" data-menu-trigger aria-haspopup="menu" aria-expanded="false" aria-label="更多操作">⋯</button>
        <div class="menu account-menu" role="menu" hidden>
          <button class="menu-item" type="button" role="menuitem" data-action="run" data-id="${esc(account.id)}">立即运行</button>
          <button class="menu-item" type="button" role="menuitem" data-action="edit" data-id="${esc(account.id)}">编辑账号</button>
          ${isWechat ? `<button class="menu-item" type="button" role="menuitem" data-action="rescan" data-id="${esc(account.id)}">重新扫码</button>` : ""}
          <div class="menu-sep"></div>
          <button class="menu-item danger" type="button" role="menuitem" data-action="delete" data-id="${esc(account.id)}">删除账号</button>
        </div>
      </div>
    </div>
  </div>`;
}

export function renderAccounts() {
  const accounts = (state.data && state.data.accounts) || [];
  $("accounts-count").textContent = String(accounts.length);
  const signature = accounts
    .map((account) =>
      [
        account.id,
        account.name,
        account.studentId,
        account.campus,
        account.loginType,
        account.lastStatus,
        account.lastCheckinAt,
        account.hasRememberMe,
        account.rememberValid,
        account.rememberExpiresAt,
      ].join(":"),
    )
    .join("|");
  if (signature === accountsSignature) return;
  accountsSignature = signature;

  const container = $("accounts");
  if (!accounts.length) {
    container.innerHTML = `<div class="empty-state">
      <span class="empty-mark">+</span>
      <strong>还没有打卡账号</strong>
      <p>添加密码账号，或使用微信扫码登录。</p>
      <button class="btn ghost small" type="button" data-action="add">添加第一个账号</button>
    </div>`;
    return;
  }
  container.innerHTML = accounts.map(accountCard).join("");
}

export async function runAccounts(ids) {
  if (!ids || !ids.length) {
    toast("请选择要运行的账号", "bad");
    return;
  }
  try {
    await api("/api/run", { method: "POST", body: { accountIds: ids } });
    toast("已开始运行，请查看运行状态");
    await reloadFn();
  } catch (err) {
    toast(err.message, "bad");
  }
}

async function deleteAccount(account) {
  const label = account.name || account.studentId;
  if (!window.confirm(`确定删除账号“${label}”？`)) return;
  try {
    await api(`/api/accounts/${account.id}`, { method: "DELETE" });
    accountsSignature = null;
    toast("账号已删除");
    await reloadFn();
  } catch (err) {
    toast(err.message, "bad");
  }
}

async function onAccountClick(event) {
  const button = event.target.closest("button[data-action]");
  if (!button) return;
  const action = button.dataset.action;
  if (action === "add") {
    openAccountDialog(null);
    return;
  }
  const id = Number(button.dataset.id);
  const account = ((state.data && state.data.accounts) || []).find((item) => item.id === id);
  if (!account) return;
  if (action === "run") {
    await runAccounts([id]);
  } else if (action === "edit") {
    openAccountDialog(account);
  } else if (action === "rescan") {
    openAccountDialog(account, { autoScan: true });
  } else if (action === "delete") {
    await deleteAccount(account);
  }
}

function updateCampusHint() {
  const info = campusSummary($("account-campus").value);
  $("campus-hint").textContent = `${info.name}有 ${info.count} 个内置点位；每次打卡随机选择一个，不能单独指定具体点。`;
}

function setScanBadge(text, tone = "neutral") {
  const badge = $("wechat-status-badge");
  badge.textContent = text;
  badge.className = `badge ${tone}`;
}

function setScanPlaceholder(text, kind = "") {
  const placeholder = $("wechat-qr-placeholder");
  placeholder.textContent = text;
  placeholder.className = `qr-placeholder ${kind}`.trim();
  placeholder.hidden = false;
  $("wechat-qr-image").hidden = true;
  $("wechat-qr-image").removeAttribute("src");
}

function syncScanButtons() {
  if (dialogMode !== "wechat") return;
  const active = scan.status === "starting" || scan.status === "waiting" || scan.status === "scanned";
  const start = $("wechat-start");
  const refresh = $("wechat-refresh");
  start.hidden = active || scan.status === "success";
  refresh.hidden = !active;
  start.textContent = editingAccount ? "重新扫码" : "获取二维码";
  refresh.textContent = "刷新二维码";
}

function updateSaveState() {
  const save = $("account-save");
  if (saving) {
    save.disabled = true;
    return;
  }
  if (dialogMode !== "wechat") {
    save.disabled = false;
    return;
  }
  const active = scan.status === "starting" || scan.status === "waiting" || scan.status === "scanned";
  if (active) {
    save.disabled = true;
    return;
  }
  if (editingAccount) {
    save.disabled = false;
    return;
  }
  save.disabled = scan.status !== "success" || !scan.session;
}

function resetScan(cancelRemote = true) {
  window.clearTimeout(scanTimer);
  scanTimer = 0;
  const id = scan.id;
  scan.id = "";
  scan.status = "idle";
  scan.session = null;
  scan.message = "";
  if (cancelRemote && id) void cancelWechatLogin(id);

  setScanBadge("未开始");
  setScanPlaceholder("选择微信扫码后获取二维码");
  $("wechat-status-text").textContent = "扫码成功后会自动获取学号与姓名，扫码凭证只保存在服务端。";
  $("scan-result").hidden = true;
  $("wechat-student").value = "";
  $("wechat-name").value = "";
  syncScanButtons();
  updateSaveState();
}

function applyScanView(view) {
  scan.status = view.status || "waiting";
  scan.message = view.message || "";
  if (scan.status === "success") {
    scan.session = view;
    setScanBadge("已确认", "ok");
    $("wechat-status-text").textContent = `已获取学号 ${view.studentId || ""}，确认校区后保存。`;
    setScanPlaceholder("微信登录成功", "success");
    const result = $("scan-result");
    result.hidden = false;
    $("wechat-student").value = view.studentId || "";
    $("wechat-name").value = view.name || "";
    $("wechat-qr-image").hidden = true;
  } else if (scan.status === "scanned") {
    scan.session = null;
    setScanBadge("已扫码", "warn");
    $("wechat-status-text").textContent = "已扫码，请在微信中确认授权。";
    if (view.qrCode) {
      $("wechat-qr-image").src = view.qrCode;
      $("wechat-qr-image").hidden = false;
      $("wechat-qr-placeholder").hidden = true;
    }
  } else if (scan.status === "waiting") {
    scan.session = null;
    setScanBadge("等待扫码", "neutral");
    $("wechat-status-text").textContent = "请使用微信扫描二维码，二维码 5 分钟内有效。";
    if (view.qrCode) {
      $("wechat-qr-image").src = view.qrCode;
      $("wechat-qr-image").hidden = false;
      $("wechat-qr-placeholder").hidden = true;
    }
  } else if (scan.status === "expired") {
    scan.session = null;
    setScanBadge("已过期", "bad");
    $("wechat-status-text").textContent = view.message || "二维码已过期，请刷新后重试。";
    setScanPlaceholder("二维码已过期");
  } else {
    scan.session = null;
    setScanBadge("失败", "bad");
    $("wechat-status-text").textContent = view.message || "获取微信二维码失败。";
    setScanPlaceholder("获取二维码失败");
  }
  syncScanButtons();
  updateSaveState();
}

function schedulePoll(delay = 1200) {
  window.clearTimeout(scanTimer);
  scanTimer = window.setTimeout(pollScan, delay);
}

async function pollScan() {
  if (!scan.id || !$("account-dialog").open) return;
  try {
    const view = await getWechatStatus(scan.id);
    if (!$("account-dialog").open || scan.id === "") return;
    applyScanView(view);
    if (view.status === "waiting" || view.status === "scanned") {
      schedulePoll(1200);
    }
  } catch (err) {
    if (!$("account-dialog").open) return;
    scan.status = "error";
    scan.message = err.message || "查询扫码状态失败";
    applyScanView({ status: "error", message: scan.message });
  }
}

async function startScan() {
  if (dialogMode !== "wechat") return;
  window.clearTimeout(scanTimer);
  const oldID = scan.id;
  scan.id = "";
  if (oldID) void cancelWechatLogin(oldID);
  scan.status = "starting";
  scan.session = null;
  scan.message = "";
  setScanBadge("获取中");
  setScanPlaceholder("正在获取二维码…");
  $("wechat-status-text").textContent = "正在向 qfhy 申请微信登录二维码…";
  syncScanButtons();
  updateSaveState();
  try {
    const view = await startWechatLogin();
    if (!$("account-dialog").open || dialogMode !== "wechat") {
      void cancelWechatLogin(view.id);
      return;
    }
    if (!view || !view.qrCode) throw new Error("二维码为空");
    scan.id = view.id;
    applyScanView({ ...view, status: "waiting" });
    schedulePoll(800);
  } catch (err) {
    scan.status = "error";
    scan.message = err.message || "获取微信二维码失败";
    applyScanView({ status: "error", message: scan.message });
  }
}

function applyMode(mode, options = {}) {
  dialogMode = mode;
  const isWechat = mode === "wechat";
  $("password-fields").hidden = isWechat;
  $("wechat-fields").hidden = !isWechat;
  $("account-student").required = !isWechat;
  $("account-password").required = !isWechat && !editingAccount;
  $("account-save").textContent = editingAccount ? "保存修改" : "添加账号";

  if (isWechat) {
    if (editingAccount) {
      $("scan-result").hidden = false;
      $("wechat-student").value = editingAccount.studentId;
      $("wechat-name").value = editingAccount.name || "";
      if (editingAccount.rememberValid) {
        setScanBadge("会话有效", "ok");
        $("wechat-status-text").textContent = `当前扫码会话有效期至 ${formatTime(
          editingAccount.rememberExpiresAt,
        )}；如需更新请重新扫码。`;
        setScanPlaceholder("扫码会话可正常使用");
      } else {
        setScanBadge("会话已过期", "bad");
        $("wechat-status-text").textContent = "当前扫码会话已过期，请重新扫码后保存。";
        setScanPlaceholder("请重新扫码登录");
      }
      scan.status = "idle";
      scan.session = null;
    } else {
      $("scan-result").hidden = true;
      $("wechat-student").value = "";
      $("wechat-name").value = "";
      setScanBadge("未开始");
      $("wechat-status-text").textContent = "扫码成功后会自动获取学号与姓名，扫码凭证只保存在服务端。";
      setScanPlaceholder("正在准备二维码…");
    }
    syncScanButtons();
    updateSaveState();
    if (options.autoScan) startScan();
  } else {
    updateSaveState();
  }
}

function openAccountDialog(account, options = {}) {
  resetScan(true);
  editingAccount = account || null;
  dialogMode = account && account.loginType === "wechat" ? "wechat" : "password";
  $("account-dialog-title").textContent = account ? "编辑打卡账号" : "添加打卡账号";
  $("account-login-type").value = dialogMode;
  $("account-login-type").disabled = !!account;
  $("account-student").value = account ? account.studentId : "";
  $("account-password").value = "";
  $("account-password").placeholder = account ? "留空表示不修改密码" : "请输入 UIAS 密码";
  $("account-name").value = account && account.loginType !== "wechat" ? account.name || "" : "";
  $("wechat-student").value = account && account.loginType === "wechat" ? account.studentId : "";
  $("wechat-name").value = account && account.loginType === "wechat" ? account.name || "" : "";
  $("account-error").hidden = true;
  $("account-error").textContent = "";
  const campus = $("account-campus");
  const optionsList = Array.from(campus.options).map((option) => option.value);
  campus.value = account && optionsList.includes(account.campus) ? account.campus : "宜宾";
  updateCampusHint();
  applyMode(dialogMode, { autoScan: !!options.autoScan });
  $("account-dialog").showModal();
  if (!account && dialogMode === "password") window.setTimeout(() => $("account-student").focus(), 0);
}

function closeAccountDialog() {
  $("account-dialog").close();
}

function onLoginTypeChange() {
  const value = $("account-login-type").value;
  if (editingAccount) {
    $("account-login-type").value = dialogMode;
    return;
  }
  resetScan(true);
  applyMode(value, { autoScan: value === "wechat" });
}

function showAccountError(message) {
  const el = $("account-error");
  el.textContent = message || "保存失败";
  el.hidden = false;
}

async function saveAccount(event) {
  event.preventDefault();
  if (saving) return;
  $("account-error").hidden = true;
  const campus = $("account-campus").value;
  const wasEditing = !!editingAccount;
  const saveButton = $("account-save");
  const originalText = saveButton.textContent;
  saving = true;
  saveButton.disabled = true;
  saveButton.textContent = "保存中…";
  try {
    if (dialogMode === "password") {
      const studentId = $("account-student").value.trim();
      const password = $("account-password").value;
      const name = $("account-name").value.trim();
      if (!studentId) {
        showAccountError("请输入学号");
        return;
      }
      if (!editingAccount && !password) {
        showAccountError("请输入 UIAS 密码");
        return;
      }
      const body = { studentId, password, name, campus, loginType: "password" };
      await api(editingAccount ? `/api/accounts/${editingAccount.id}` : "/api/accounts", {
        method: editingAccount ? "PUT" : "POST",
        body,
      });
    } else {
      const name = $("wechat-name").value.trim();
      if (editingAccount) {
        if (scan.status === "success" && scan.session) {
          await completeWechatLogin(scan.id, { accountId: editingAccount.id, name, campus });
          scan.id = "";
        } else {
          await api(`/api/accounts/${editingAccount.id}`, {
            method: "PUT",
            body: {
              studentId: editingAccount.studentId,
              password: "",
              name,
              campus,
              loginType: "wechat",
            },
          });
        }
      } else {
        if (scan.status !== "success" || !scan.session) {
          showAccountError("请先完成微信扫码登录");
          return;
        }
        await completeWechatLogin(scan.id, { name, campus });
        scan.id = "";
      }
    }
    closeAccountDialog();
    accountsSignature = null;
    toast(wasEditing ? "账号已更新" : "账号已添加");
    await reloadFn();
  } catch (err) {
    showAccountError(err.message || "保存失败");
  } finally {
    saving = false;
    saveButton.textContent = originalText;
    if ($("account-dialog").open) updateSaveState();
  }
}

export function initAccounts(options = {}) {
  reloadFn = options.reload || reloadFn;
  $("add-account-btn").addEventListener("click", () => openAccountDialog(null));
  $("accounts").addEventListener("click", onAccountClick);
  $("account-form").addEventListener("submit", saveAccount);
  $("account-login-type").addEventListener("change", onLoginTypeChange);
  $("account-campus").addEventListener("change", updateCampusHint);
  $("wechat-start").addEventListener("click", startScan);
  $("wechat-refresh").addEventListener("click", startScan);
  $("account-cancel").addEventListener("click", closeAccountDialog);
  $("account-cancel-x").addEventListener("click", closeAccountDialog);
  $("account-dialog").addEventListener("cancel", (event) => {
    event.preventDefault();
    closeAccountDialog();
  });
  $("account-dialog").addEventListener("close", () => {
    resetScan(true);
    editingAccount = null;
    $("account-login-type").disabled = false;
  });
}
