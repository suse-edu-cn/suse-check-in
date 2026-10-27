import { rawRequest, readJSON } from "../core/api.js";
import { state, statusClass, statusLabel } from "../core/state.js";
import { $, esc, formatTime } from "../core/utils.js";

let runsSignature = null;

function sampleURL(path) {
  if (!path) return "";
  const parts = String(path).split("/");
  if (parts.length !== 3 || parts[0] !== "captchas") return "";
  return `/api/samples/${encodeURIComponent(parts[1])}/${encodeURIComponent(parts[2])}`;
}

function resultHTML(result) {
  const label = statusLabel(result.status);
  const message = String(result.message || "").trim();
  const detail = message && message !== label ? message : "";
  const task = result.taskName ? `任务：${result.taskName}` : "";
  const line = [detail, task].filter(Boolean).join(" · ");
  const coords = result.locationLatitude
    ? `${Number(result.locationLatitude).toFixed(6)}, ${Number(result.locationLongitude).toFixed(6)}`
    : "";
  const ocr = result.ocrText ? `${result.ocrText}${result.ocrMillis ? ` · ${result.ocrMillis}ms` : ""}` : "";
  const sample = sampleURL(result.captchaSample);
  return `<div class="result">
    <div class="result-head">
      <strong>${esc(result.accountName || result.studentId || result.accountId)}</strong>
      <span class="badge status ${statusClass(result.status)}">${esc(label)}</span>
      ${ocr ? `<span class="result-code">${esc(ocr)}</span>` : ""}
    </div>
    ${line ? `<p class="result-line">${esc(line)}</p>` : ""}
    ${
      result.locationAddress
        ? `<p class="result-location">${esc(result.locationAddress)}${coords ? `<span> · ${esc(coords)}</span>` : ""}</p>`
        : ""
    }
    ${sample ? `<button class="sample-link" type="button" data-sample="${esc(result.captchaSample)}">查看验证码样本</button>` : ""}
  </div>`;
}

export function renderRuns() {
  const runs = (state.data && state.data.runs) || [];
  const signature = runs.map((run) => `${run.id}:${run.finishedAt}:${(run.results || []).length}`).join("|");
  if (signature === runsSignature) return;
  runsSignature = signature;

  const box = $("runs");
  if (!runs.length) {
    box.innerHTML = '<p class="empty">还没有运行记录。</p>';
    return;
  }
  box.innerHTML = runs
    .map((run) => {
      const results = run.results || [];
      const failed = results.filter((result) => statusClass(result.status) === "bad").length;
      return `<details class="run">
        <summary>
          <span class="run-dot ${failed ? "bad" : "ok"}"></span>
          <div class="run-summary-main">
            <strong>${esc(run.summary || "运行完成")}</strong>
            <span class="muted">${esc(formatTime(run.finishedAt || run.startedAt))}</span>
          </div>
          <span class="badge neutral">${run.trigger === "scheduled" ? "定时" : "手动"}</span>
        </summary>
        <div class="run-body">${results.map(resultHTML).join("") || '<p class="empty">无结果</p>'}</div>
      </details>`;
    })
    .join("");
}

async function openSample(path) {
  const url = sampleURL(path);
  if (!url) return;
  const dialog = $("sample-dialog");
  const image = $("sample-image");
  const error = $("sample-error");
  image.hidden = true;
  image.removeAttribute("src");
  error.textContent = "";
  dialog.showModal();
  try {
    const res = await rawRequest(url);
    if (!res.ok) {
      const data = await readJSON(res);
      throw new Error((data && data.error) || `加载样本失败（HTTP ${res.status}）`);
    }
    const blob = await res.blob();
    const dataURL = await new Promise((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result || ""));
      reader.onerror = () => reject(new Error("读取验证码样本失败"));
      reader.readAsDataURL(blob);
    });
    image.src = dataURL;
    image.hidden = false;
  } catch (err) {
    error.textContent = err.message || "加载验证码样本失败";
  }
}

export function initRuns() {
  $("runs").addEventListener("click", (event) => {
    const button = event.target.closest("button[data-sample]");
    if (button) openSample(button.dataset.sample);
  });
  $("sample-close").addEventListener("click", () => $("sample-dialog").close());
  $("sample-dialog").addEventListener("close", () => {
    const image = $("sample-image");
    image.hidden = true;
    image.removeAttribute("src");
  });
}
