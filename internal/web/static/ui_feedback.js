(function (root, factory) {
  "use strict";
  const api = factory();
  if (typeof module === "object" && module.exports) module.exports = api;
  if (root) root.HermesUIFeedback = api;
})(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  function showTextAlert(target, message) {
    if (!target || typeof message !== "string") return;
    target.textContent = message;
    target.hidden = false;
  }

  function clearTextAlert(target) {
    if (!target) return;
    target.textContent = "";
    target.hidden = true;
  }

  return { showTextAlert, clearTextAlert };
});
