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
    toggle.hidden = false;
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
  const accountFeedback = document.getElementById("account-feedback");
  const projectFeedback = document.getElementById("project-feedback");
  const blueprintDeleteStatus = document.getElementById("blueprint-delete-status");
  const accountDeleteStatus = document.getElementById("account-delete-status");
  const projectDeleteStatus = document.getElementById("project-delete-status");
  const deleteFeedbackTargets = [
    blueprintFeedback,
    accountFeedback,
    projectFeedback,
    blueprintDeleteStatus,
    accountDeleteStatus,
    projectDeleteStatus,
  ];
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
    });
    document.addEventListener("htmx:beforeSwap", (event) => {
      if (!cascade.beforeSwap(event.detail)) event.preventDefault();
    });
    document.addEventListener("htmx:afterSwap", (event) => {
      cascade.afterSwap(event.target);
    });
    document.addEventListener("htmx:afterRequest", (event) => cascade.afterRequest(event.detail));
  }

  const jobHistorySwapTarget = (event) => {
    const target = event.detail?.target || event.target;
    return target instanceof HTMLElement && target.id === "job-history" ? target : null;
  };
  let focusedJobDetailID = "";
  document.addEventListener("htmx:beforeSwap", (event) => {
    const target = jobHistorySwapTarget(event);
    if (!target) return;
    const active = document.activeElement;
    focusedJobDetailID = active instanceof HTMLElement
      && /^job-detail-[0-9]+$/.test(active.id)
      && target.contains(active)
      ? active.id
      : "";
  });
  document.addEventListener("htmx:afterSwap", (event) => {
    if (!jobHistorySwapTarget(event) || !focusedJobDetailID) return;
    const replacement = document.getElementById(focusedJobDetailID);
    focusedJobDetailID = "";
    if (replacement instanceof HTMLElement) replacement.focus({ preventScroll: true });
  });

  document.addEventListener("htmx:beforeRequest", (event) => {
    if (String(event.detail?.requestConfig?.verb).toLowerCase() !== "delete") return;
    deleteFeedbackTargets.forEach((target) => feedbackUI?.clearTextAlert(target));
  });

  const announceDeleteError = (target, status, message) => {
    if (!feedbackUI) return;
    feedbackUI.clearTextAlert(status);
    feedbackUI.showTextAlert(target, message);
  };

  const announceDeleteSuccess = (target, error, message) => {
    if (!feedbackUI) return;
    feedbackUI.clearTextAlert(error);
    if (!target) return;
    feedbackUI.showTextAlert(target, message);
    target.focus();
  };

  document.body.addEventListener("blueprint-delete-error", (event) => {
    announceDeleteError(blueprintFeedback, blueprintDeleteStatus, event.detail?.message);
  });

  document.body.addEventListener("account-delete-error", (event) => {
    announceDeleteError(accountFeedback, accountDeleteStatus, event.detail?.message);
  });

  document.body.addEventListener("project-delete-error", (event) => {
    announceDeleteError(projectFeedback, projectDeleteStatus, event.detail?.message);
  });

  document.body.addEventListener("blueprint-delete-success", (event) => {
    announceDeleteSuccess(blueprintDeleteStatus, blueprintFeedback, event.detail?.message);
  });

  document.body.addEventListener("account-delete-success", (event) => {
    announceDeleteSuccess(accountDeleteStatus, accountFeedback, event.detail?.message);
  });

  document.body.addEventListener("project-delete-success", (event) => {
    announceDeleteSuccess(projectDeleteStatus, projectFeedback, event.detail?.message);
  });

  document.querySelectorAll("[data-disclosure]").forEach((group) => {
    const toggle = group.querySelector(".disclosure-toggle");
    const panel = toggle && document.getElementById(toggle.getAttribute("aria-controls"));
    if (!(toggle instanceof HTMLButtonElement) || !(panel instanceof HTMLElement)) return;
    const fallback = group.querySelector("[data-disclosure-fallback]");
    const setExpanded = (expanded) => {
      toggle.setAttribute("aria-expanded", String(expanded));
      panel.hidden = !expanded;
    };
    toggle.hidden = false;
    if (fallback instanceof HTMLElement) fallback.hidden = true;
    const enhancedExpanded = toggle.dataset.enhancedExpanded === "true";
    setExpanded(enhancedExpanded);
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
    const logText = document.createTextNode(log.textContent);
    log.replaceChildren(logText);
    let streamEnded = false;
    let opened = false;
    let unseenLogs = false;

    const isNearBottom = () => log.scrollHeight - log.scrollTop - log.clientHeight <= 24;

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
      const shouldFollow = isNearBottom();
      logText.appendData(event.data + "\n");
      if (shouldFollow) {
        log.scrollTop = log.scrollHeight;
        if (unseenLogs && status) status.textContent = "";
        unseenLogs = false;
      } else {
        unseenLogs = true;
        if (status) status.textContent = "有新日志，向下滚动查看。";
      }
    };
    stream.addEventListener("open", () => {
      if (opened) {
        // The endpoint replays the complete broker history for every new
        // connection. Replace the prior snapshot so a reconnect neither
        // duplicates the backlog nor removes legitimate repeated lines.
        logText.data = "";
        log.scrollTop = 0;
        unseenLogs = false;
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
      setBusy(button, true);
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
      } finally {
        setBusy(button, false);
      }
    });
  });

  const dialog = document.getElementById("confirm-dialog");
  const message = document.getElementById("confirm-message");
  const cancel = document.getElementById("confirm-cancel");
  const confirm = document.getElementById("confirm-submit");
  let pendingAction = null;
  let confirmationTrigger = null;

  const busyStates = new WeakMap();

  function busyControl(element, submitter) {
    if (submitter instanceof HTMLElement) return submitter;
    if (element instanceof HTMLFormElement) {
      return element.querySelector("button[type='submit'], input[type='submit'], button:not([type])");
    }
    return element instanceof HTMLElement ? element : null;
  }

  function setBusy(element, busy, submitter = null) {
    const control = busyControl(element, submitter);
    if (!control) return;

    const disablesWhileBusy = control instanceof HTMLButtonElement
      || (control instanceof HTMLInputElement && ["button", "submit", "image"].includes(control.type));

    if (busy) {
      if (!busyStates.has(control)) {
        busyStates.set(control, {
          ariaBusy: control.getAttribute("aria-busy"),
          ariaLabel: control.getAttribute("aria-label"),
          disabled: "disabled" in control ? control.disabled : undefined,
        });
      }
      if (!control.getAttribute("data-loading-label")) {
        control.setAttribute("data-loading-label", "处理中…");
      }
      control.setAttribute("aria-busy", "true");
      control.setAttribute("aria-label", control.getAttribute("data-loading-label"));
      if (disablesWhileBusy) control.disabled = true;
      return;
    }

    const prior = busyStates.get(control);
    if (!prior) return;
    if (prior.ariaBusy === null) control.removeAttribute("aria-busy");
    else control.setAttribute("aria-busy", prior.ariaBusy);
    if (prior.ariaLabel === null) control.removeAttribute("aria-label");
    else control.setAttribute("aria-label", prior.ariaLabel);
    if (prior.disabled !== undefined) control.disabled = prior.disabled;
    busyStates.delete(control);
  }

  document.addEventListener("htmx:beforeRequest", (event) => {
    setBusy(event.detail?.elt || event.target, true);
  });

  document.addEventListener("htmx:afterRequest", (event) => {
    setBusy(event.detail?.elt || event.target, false);
  });

  function askForConfirmation(question, action) {
    if (!(dialog instanceof HTMLDialogElement) || typeof dialog.showModal !== "function") {
      if (window.confirm(question)) action();
      return;
    }
    message.textContent = question;
    pendingAction = action;
    confirmationTrigger = document.activeElement instanceof HTMLElement ? document.activeElement : null;
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

  dialog?.addEventListener("close", () => {
    confirmationTrigger?.focus();
    confirmationTrigger = null;
  });

  document.body.addEventListener("htmx:confirm", (event) => {
    const question = event.detail?.question;
    if (!question) return;
    event.preventDefault();
    askForConfirmation(question, () => event.detail.issueRequest(true));
  });

  document.addEventListener("submit", (event) => {
    const form = event.target;
    if (!(form instanceof HTMLFormElement)) return;
    if (form.dataset.confirm) {
      event.preventDefault();
      askForConfirmation(form.dataset.confirm, () => {
        setBusy(form, true, event.submitter);
        form.submit();
      });
      return;
    }
    setBusy(form, true, event.submitter);
  });
})();
