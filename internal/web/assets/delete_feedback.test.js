"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const feedback = require("../static/ui_feedback.js");

class FakeElement {
  constructor({ id = "", textContent = "", hidden = false } = {}) {
    this.id = id;
    this.textContent = textContent;
    this.hidden = hidden;
    this.dataset = {};
    this.attributes = new Map();
    this.listeners = new Map();
    this.focusCalls = 0;
    this.focusOptions = undefined;
  }

  addEventListener(name, listener) {
    const listeners = this.listeners.get(name) || [];
    listeners.push(listener);
    this.listeners.set(name, listeners);
  }

  async emit(name, detail = {}) {
    await Promise.all((this.listeners.get(name) || []).map((listener) => listener({ target: this, detail })));
  }

  getAttribute(name) {
    return this.attributes.get(name) || null;
  }

  setAttribute(name, value) {
    this.attributes.set(name, String(value));
  }

  removeAttribute(name) {
    this.attributes.delete(name);
  }

  focus(options) {
    this.focusCalls += 1;
    this.focusOptions = options;
  }
}

class FakeInput extends FakeElement {}
class FakeSelect extends FakeElement {}
class FakeButton extends FakeElement {
  constructor(options = {}) {
    super(options);
    this.disabled = false;
  }
}
class FakeDialog extends FakeElement {}
class FakeForm extends FakeElement {}

function executeApp() {
  const ids = [
    "account-feedback", "project-feedback", "blueprint-feedback",
    "account-delete-status", "project-delete-status", "blueprint-delete-status",
  ];
  const elements = new Map(ids.map((id) => [id, new FakeElement({ id })]));
  const body = new FakeElement();
  const documentListeners = new Map();
  const document = {
    body,
    activeElement: null,
    querySelectorAll() { return []; },
    querySelector() { return null; },
    getElementById(id) { return elements.get(id) || null; },
    addEventListener(name, listener) {
      const listeners = documentListeners.get(name) || [];
      listeners.push(listener);
      documentListeners.set(name, listeners);
    },
  };
  const window = {
    HermesUIFeedback: feedback,
    confirm() { return true; },
  };
  const sandbox = {
    document,
    window,
    navigator: {},
    HTMLElement: FakeElement,
    HTMLInputElement: FakeInput,
    HTMLSelectElement: FakeSelect,
    HTMLButtonElement: FakeButton,
    HTMLDialogElement: FakeDialog,
    HTMLFormElement: FakeForm,
  };
  const appPath = path.join(__dirname, "../static/app.js");
  vm.runInNewContext(fs.readFileSync(appPath, "utf8"), sandbox, { filename: appPath });
  return {
    body,
    document,
    elements,
    async emitDocument(name, event) {
      await Promise.all((documentListeners.get(name) || []).map((listener) => listener(event)));
    },
  };
}

test("successful delete events announce through the matching status region and focus it", async () => {
  const run = executeApp();
  for (const resource of ["account", "project", "blueprint"]) {
    const target = run.elements.get(`${resource}-delete-status`);
    target.innerHTML = "unchanged";
    const message = `<strong>${resource} deleted</strong>`;

    await run.body.emit(`${resource}-delete-success`, { message });

    assert.equal(target.textContent, message);
    assert.equal(target.innerHTML, "unchanged");
    assert.equal(target.hidden, false);
    assert.equal(target.focusCalls, 1);
  }
});

test("a delete error clears an earlier success for the same resource", async () => {
  const run = executeApp();
  for (const resource of ["account", "project", "blueprint"]) {
    const error = run.elements.get(`${resource}-feedback`);
    const status = run.elements.get(`${resource}-delete-status`);

    await run.body.emit(`${resource}-delete-success`, { message: `${resource} deleted` });
    await run.body.emit(`${resource}-delete-error`, { message: `${resource} failed` });

    assert.equal(error.textContent, `${resource} failed`);
    assert.equal(error.hidden, false);
    assert.equal(status.textContent, "");
    assert.equal(status.hidden, true);
  }
});

test("a delete success clears an earlier error for the same resource", async () => {
  const run = executeApp();
  for (const resource of ["account", "project", "blueprint"]) {
    const error = run.elements.get(`${resource}-feedback`);
    const status = run.elements.get(`${resource}-delete-status`);

    await run.body.emit(`${resource}-delete-error`, { message: `${resource} failed` });
    await run.body.emit(`${resource}-delete-success`, { message: `${resource} deleted` });

    assert.equal(error.textContent, "");
    assert.equal(error.hidden, true);
    assert.equal(status.textContent, `${resource} deleted`);
    assert.equal(status.hidden, false);
  }
});

test("job-history polling restores the focused replacement link without moveBefore", async () => {
  assert.equal(typeof FakeElement.prototype.moveBefore, "undefined");
  const run = executeApp();
  const oldHistory = new FakeElement({ id: "job-history" });
  const oldLink = new FakeElement({ id: "job-detail-41" });
  oldHistory.contains = (element) => element === oldLink;
  run.elements.set(oldLink.id, oldLink);
  run.document.activeElement = oldLink;

  await run.emitDocument("htmx:beforeSwap", {
    target: oldHistory,
    detail: { target: oldHistory },
  });

  const replacementHistory = new FakeElement({ id: "job-history" });
  const replacementLink = new FakeElement({ id: oldLink.id });
  replacementHistory.contains = (element) => element === replacementLink;
  run.elements.set(replacementLink.id, replacementLink);
  run.document.activeElement = run.body;

  await run.emitDocument("htmx:afterSwap", {
    target: replacementHistory,
    detail: { target: replacementHistory },
  });

  assert.equal(oldLink.focusCalls, 0);
  assert.equal(replacementLink.focusCalls, 1);
  assert.equal(replacementLink.focusOptions?.preventScroll, true);
});

test("starting a new HTMX delete clears stale success and error feedback without metadata support", async () => {
  const run = executeApp();
  for (const target of run.elements.values()) {
    target.textContent = "stale feedback";
    target.hidden = false;
  }
  const button = new FakeButton();

  await run.emitDocument("htmx:beforeRequest", {
    target: button,
    detail: { elt: button, requestConfig: { verb: "delete" } },
  });

  for (const [id, target] of run.elements) {
    assert.equal(target.textContent, "", `${id} text`);
    assert.equal(target.hidden, true, `${id} visibility`);
  }
});
