(() => {
  "use strict";

  document.querySelectorAll("[data-password-toggle]").forEach((toggle) => {
    const input = document.getElementById(toggle.getAttribute("aria-controls"));
    if (!(input instanceof HTMLInputElement)) return;

    toggle.addEventListener("click", () => {
      const showing = input.type === "text";
      input.type = showing ? "password" : "text";
      toggle.textContent = showing ? "显示" : "隐藏";
      toggle.setAttribute("aria-pressed", String(!showing));
      input.focus();
    });
  });

  window.filterSelectOptions = (input) => {
    const select = document.querySelector(input.dataset.filterSelect);
    if (!(select instanceof HTMLSelectElement)) return;
    const query = input.value.trim().toLowerCase();
    Array.from(select.options).forEach((option) => {
      option.hidden = Boolean(query) && !(option.textContent + " " + option.value).toLowerCase().includes(query);
    });
  };

  const metadata = window.HermesBlueprintMetadata;
  const feedbackUI = window.HermesUIFeedback;
  const blueprintFeedback = document.getElementById("blueprint-feedback");
  const selectionHints = {
    region: document.querySelector('[data-selection-hint="region"]'),
    instanceType: document.querySelector('[data-selection-hint="instanceType"]'),
    ami: document.querySelector('[data-selection-hint="ami"]'),
  };
  if (metadata) {
    const cascade = metadata.createCascadeCoordinator(selectionHints);
    document.addEventListener("change", (event) => {
      const source = event.target?.dataset?.metadataSource;
      if (!source) return;
      cascade.selectionChanged(source, event, event.target.value);
    }, true);

    document.addEventListener("htmx:beforeRequest", (event) => {
      cascade.beforeRequest(event.detail);
      if (event.detail?.requestConfig?.verb === "delete") feedbackUI?.clearTextAlert(blueprintFeedback);
    });
    document.addEventListener("htmx:beforeSwap", (event) => {
      if (!cascade.beforeSwap(event.detail)) event.preventDefault();
    });
    document.addEventListener("htmx:afterSwap", (event) => {
      cascade.afterSwap(event.target);
    });
    document.addEventListener("htmx:afterRequest", (event) => cascade.afterRequest(event.detail));
  }

  document.body.addEventListener("blueprint-delete-error", (event) => {
    feedbackUI?.showTextAlert(blueprintFeedback, event.detail?.message);
  });

  document.querySelectorAll("[data-disclosure]").forEach((group) => {
    const toggle = group.querySelector(".disclosure-toggle");
    const panel = toggle && document.getElementById(toggle.getAttribute("aria-controls"));
    if (!(toggle instanceof HTMLButtonElement) || !(panel instanceof HTMLElement)) return;
    const setExpanded = (expanded) => {
      toggle.setAttribute("aria-expanded", String(expanded));
      panel.hidden = !expanded;
    };
    setExpanded(toggle.getAttribute("aria-expanded") === "true");
    toggle.addEventListener("click", () => setExpanded(toggle.getAttribute("aria-expanded") !== "true"));
  });

  const redisEnabled = document.querySelector("[data-redis-enabled]");
  const redisAuth = document.querySelector("[data-redis-auth]");
  if (redisEnabled instanceof HTMLInputElement && redisAuth instanceof HTMLInputElement) {
    const syncRedisAuth = () => metadata.syncRedisAuth(redisEnabled, redisAuth);
    redisEnabled.addEventListener("change", syncRedisAuth);
    syncRedisAuth();
  }

  document.querySelectorAll("[data-job-stream-url]").forEach((log) => {
    const streamURL = log.dataset.jobStreamUrl;
    if (!streamURL || typeof EventSource !== "function") return;
    const status = log.parentElement?.querySelector("[data-job-stream-status]");
    const stream = new EventSource(streamURL);
    let streamEnded = false;
    let opened = false;

    const refreshEnvironmentFragments = () => {
      if (!window.htmx) return;
      if (log.dataset.jobStatusUrl) {
        window.htmx.ajax("GET", log.dataset.jobStatusUrl, { target: "#status", swap: "outerHTML" });
      }
      if (log.dataset.jobHistoryUrl) {
        window.htmx.ajax("GET", log.dataset.jobHistoryUrl, { target: "#job-history", swap: "outerHTML" });
      }
    };

    stream.onmessage = (event) => {
      log.textContent += event.data + "\n";
      log.scrollTop = log.scrollHeight;
    };
    stream.addEventListener("open", () => {
      if (opened) {
        // The endpoint replays the complete broker history for every new
        // connection. Replace the prior snapshot so a reconnect neither
        // duplicates the backlog nor removes legitimate repeated lines.
        log.textContent = "";
        log.scrollTop = 0;
        if (status) status.textContent = "实时日志已重新连接。";
      }
      opened = true;
    });
    stream.addEventListener("done", () => {
      if (streamEnded) return;
      streamEnded = true;
      stream.close();
      if (status) status.textContent = "任务已完成，完整日志可在任务详情中查看。";
      refreshEnvironmentFragments();
    });
    stream.addEventListener("interrupted", () => {
      if (streamEnded) return;
      streamEnded = true;
      stream.close();
      if (status) status.textContent = "实时日志已中断，任务状态仍在确认中。";
      refreshEnvironmentFragments();
    });
    stream.addEventListener("error", () => {
      if (!streamEnded && status) status.textContent = "实时日志连接中断，正在重试。";
    });
  });

  document.querySelectorAll("[data-copy-log]").forEach((button) => {
    const target = document.getElementById(button.dataset.copyTarget || button.getAttribute("aria-controls"));
    if (!(button instanceof HTMLButtonElement) || !(target instanceof HTMLElement)) return;
    const status = button.parentElement?.querySelector("[data-copy-status]");
    button.hidden = false;
    button.addEventListener("click", async () => {
      try {
        if (!navigator.clipboard?.writeText) throw new Error("clipboard unavailable");
        await navigator.clipboard.writeText(target.textContent);
        if (status) status.textContent = "日志已复制";
      } catch (_) {
        const selection = window.getSelection?.();
        const range = document.createRange?.();
        if (selection && range) {
          range.selectNodeContents(target);
          selection.removeAllRanges();
          selection.addRange(range);
        }
        const copied = document.execCommand?.("copy") === true;
        if (status) status.textContent = copied ? "日志已复制" : "无法自动复制，日志已选中";
      }
    });
  });

  const dialog = document.getElementById("confirm-dialog");
  const message = document.getElementById("confirm-message");
  const cancel = document.getElementById("confirm-cancel");
  const confirm = document.getElementById("confirm-submit");
  let pendingAction = null;

  function askForConfirmation(question, action) {
    if (!(dialog instanceof HTMLDialogElement) || typeof dialog.showModal !== "function") {
      if (window.confirm(question)) action();
      return;
    }
    message.textContent = question;
    pendingAction = action;
    dialog.showModal();
    cancel.focus();
  }

  confirm?.addEventListener("click", () => {
    const action = pendingAction;
    pendingAction = null;
    dialog.close("confirm");
    action?.();
  });

  cancel?.addEventListener("click", () => {
    pendingAction = null;
    dialog.close("cancel");
  });

  dialog?.addEventListener("cancel", () => {
    pendingAction = null;
  });

  document.body.addEventListener("htmx:confirm", (event) => {
    const question = event.detail?.question;
    if (!question) return;
    event.preventDefault();
    askForConfirmation(question, () => event.detail.issueRequest(true));
  });

  document.addEventListener("submit", (event) => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement) || !form.dataset.confirm) return;
    event.preventDefault();
    askForConfirmation(form.dataset.confirm, () => form.submit());
  });
})();
