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
