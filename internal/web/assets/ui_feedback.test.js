"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");

let feedback = {};
try {
  feedback = require("../static/ui_feedback.js");
} catch (_) {
  // The assertion below provides the intentional RED when the helper is absent.
}

test("delete failure feedback renders untrusted-looking text without HTML injection", () => {
  assert.equal(typeof feedback.showTextAlert, "function");

  const target = { textContent: "", innerHTML: "unchanged", hidden: true };
  const message = '<img src=x onerror="alert(1)"> 无法删除';
  feedback.showTextAlert(target, message);

  assert.equal(target.textContent, message);
  assert.equal(target.innerHTML, "unchanged");
  assert.equal(target.hidden, false);

  feedback.clearTextAlert(target);
  assert.equal(target.textContent, "");
  assert.equal(target.hidden, true);
});
