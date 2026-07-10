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
