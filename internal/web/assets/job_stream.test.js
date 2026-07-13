"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

class FakeText {
  constructor(data) {
    this.data = String(data);
    this.parentElement = null;
    this.appendDataCalls = 0;
  }

  get textContent() {
    return this.data;
  }

  appendData(value) {
    this.data += String(value);
    this.appendDataCalls += 1;
    if (this.parentElement) {
      this.parentElement.scrollHeight += this.parentElement.appendScrollHeight;
    }
  }
}

class FakeElement {
  constructor({ id = "", dataset = {}, textContent = "", hidden = false } = {}) {
    this.id = id;
    this.dataset = dataset;
    this._textContent = String(textContent);
    this.children = [];
    this.textContentWrites = 0;
    this.hidden = hidden;
    this.scrollHeight = 0;
    this.scrollTop = 0;
    this.clientHeight = 0;
    this.appendScrollHeight = 0;
    this.listeners = new Map();
    this.attributes = new Map();
    this.parentElement = null;
  }

  get textContent() {
    if (this.children.length > 0) {
      return this.children.map((child) => child.textContent).join("");
    }
    return this._textContent;
  }

  set textContent(value) {
    this._textContent = String(value);
    this.children = [];
    this.textContentWrites += 1;
  }

  get firstChild() {
    return this.children[0] || null;
  }

  replaceChildren(...children) {
    this._textContent = "";
    this.children = children;
    children.forEach((child) => {
      child.parentElement = this;
    });
  }

  addEventListener(name, listener) {
    const listeners = this.listeners.get(name) || [];
    listeners.push(listener);
    this.listeners.set(name, listeners);
  }

  async emit(name, event = {}) {
    const listeners = this.listeners.get(name) || [];
    await Promise.all(listeners.map((listener) => listener({ target: this, ...event })));
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

  focus() {}
}

class FakeInput extends FakeElement {}
class FakeSelect extends FakeElement {
  constructor(options = {}) {
    super(options);
    this.disabled = false;
    this.options = [];
  }
}
class FakeButton extends FakeElement {
  constructor(options = {}) {
    super(options);
    this.disabled = false;
  }
}
class FakeDialog extends FakeElement {
  close() {}
}
class FakeForm extends FakeElement {
  submit() {}
}

class FakeEventSource {
  static instances = [];

  constructor(url) {
    this.url = url;
    this.closed = false;
    this.listeners = new Map();
    FakeEventSource.instances.push(this);
  }

  addEventListener(name, listener) {
    this.listeners.set(name, listener);
  }

  emit(name, data = "") {
    if (name === "message") {
      this.onmessage?.({ data });
      return;
    }
    this.listeners.get(name)?.({ data });
  }

  close() {
    this.closed = true;
  }
}

function executeApp({
  streamPanel = null,
  streamStatus = null,
  copyButton = null,
  copyTarget = null,
  disclosureGroup = null,
  disclosurePanel = null,
  clipboardFails = false,
  deferClipboard = false,
  execCopySucceeds = true,
} = {}) {
  FakeEventSource.instances = [];
  const refreshes = [];
  const copied = [];
  const documentListeners = new Map();
  let releaseClipboard = null;
  const byID = new Map();
  if (copyTarget) byID.set(copyTarget.id, copyTarget);
  if (disclosurePanel) byID.set(disclosurePanel.id, disclosurePanel);
  if (streamPanel) {
    streamPanel.parentElement = {
      querySelector(selector) {
        return selector === "[data-job-stream-status]" ? streamStatus : null;
      },
    };
  }

  const queryAll = (selector) => {
    if (selector === "[data-job-stream-url]") return streamPanel ? [streamPanel] : [];
    if (selector === "[data-copy-log]") return copyButton ? [copyButton] : [];
    if (selector === "[data-disclosure]") return disclosureGroup ? [disclosureGroup] : [];
    return [];
  };
  const body = new FakeElement();
  const document = {
    body,
    querySelectorAll: queryAll,
    querySelector() { return null; },
    getElementById(id) { return byID.get(id) || null; },
    addEventListener(name, listener) {
      const listeners = documentListeners.get(name) || [];
      listeners.push(listener);
      documentListeners.set(name, listeners);
    },
    createTextNode(value) { return new FakeText(value); },
    createRange() { return { selectNodeContents() {} }; },
    execCommand() { return execCopySucceeds; },
  };
  const htmx = {
    ajax(method, url, options) {
      refreshes.push({ method, url, options });
    },
  };
  const window = {
    htmx,
    confirm() { return true; },
    getSelection() { return { removeAllRanges() {}, addRange() {} }; },
  };
  const sandbox = {
    document,
    window,
    navigator: {
      clipboard: {
        async writeText(value) {
          if (clipboardFails) throw new Error("clipboard denied");
          if (deferClipboard) await new Promise((resolve) => { releaseClipboard = resolve; });
          copied.push(value);
        },
      },
    },
    htmx,
    EventSource: FakeEventSource,
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
    refreshes,
    copied,
    releaseClipboard: () => releaseClipboard?.(),
    async emitDocument(name, event) {
      await Promise.all((documentListeners.get(name) || []).map((listener) => listener(event)));
    },
  };
}

test("active Job stream appends logs, closes on completion, and refreshes status and history", () => {
  const streamStatus = new FakeElement();
  const streamPanel = new FakeElement({
    dataset: {
      jobStreamUrl: "/jobs/42/logs/stream",
      jobStatusUrl: "/environments/7/status",
      jobHistoryUrl: "/environments/7/jobs",
    },
  });
  streamPanel.scrollHeight = 240;
  streamPanel.clientHeight = 240;

  const { refreshes } = executeApp({ streamPanel, streamStatus });
  assert.equal(FakeEventSource.instances.length, 1);
  const source = FakeEventSource.instances[0];
  assert.equal(source.url, "/jobs/42/logs/stream");

  source.emit("message", "first live line");
  assert.equal(streamPanel.textContent, "first live line\n");
  assert.equal(streamPanel.scrollTop, 240);

  source.emit("done", "end");
  assert.equal(source.closed, true);
  assert.match(streamStatus.textContent, /任务已完成/);
  assert.equal(JSON.stringify(refreshes), JSON.stringify([
    { method: "GET", url: "/environments/7/status", options: { target: "#status", swap: "outerHTML" } },
    { method: "GET", url: "/environments/7/jobs", options: { target: "#job-history", swap: "outerHTML" } },
  ]));
});

test("Job stream appends through one stable text node instead of rewriting the accumulated log", () => {
  const streamPanel = new FakeElement({
    dataset: { jobStreamUrl: "/jobs/42/logs/stream" },
  });
  executeApp({ streamPanel, streamStatus: new FakeElement() });
  const source = FakeEventSource.instances[0];

  source.emit("message", "first line");
  const logText = streamPanel.firstChild;
  source.emit("message", "second line");

  assert.ok(logText instanceof FakeText);
  assert.equal(streamPanel.firstChild, logText);
  assert.equal(logText.appendDataCalls, 2);
  assert.equal(streamPanel.textContentWrites, 0);
  assert.equal(streamPanel.textContent, "first line\nsecond line\n");
});

test("Job stream follows when the reader was near the bottom before append", () => {
  const streamPanel = new FakeElement({
    dataset: { jobStreamUrl: "/jobs/42/logs/stream" },
  });
  streamPanel.scrollHeight = 300;
  streamPanel.clientHeight = 200;
  streamPanel.scrollTop = 100;
  streamPanel.appendScrollHeight = 48;
  executeApp({ streamPanel, streamStatus: new FakeElement() });

  FakeEventSource.instances[0].emit("message", "new line");

  assert.equal(streamPanel.scrollHeight, 348);
  assert.equal(streamPanel.scrollTop, 348);
});

test("Job stream preserves an older reading position and announces unseen logs", () => {
  const streamStatus = new FakeElement();
  const streamPanel = new FakeElement({
    dataset: { jobStreamUrl: "/jobs/42/logs/stream" },
  });
  streamPanel.scrollHeight = 1000;
  streamPanel.clientHeight = 200;
  streamPanel.scrollTop = 320;
  streamPanel.appendScrollHeight = 32;
  executeApp({ streamPanel, streamStatus });

  FakeEventSource.instances[0].emit("message", "new line");

  assert.equal(streamPanel.scrollTop, 320);
  assert.match(streamStatus.textContent, /新日志/);
});

test("terminal pages without a stream data attribute never create EventSource", () => {
  executeApp();
  assert.equal(FakeEventSource.instances.length, 0);
});

test("EventSource reconnect replaces replayed snapshot without removing real duplicate log lines", () => {
  const streamPanel = new FakeElement({
    dataset: { jobStreamUrl: "/jobs/42/logs/stream" },
  });
  executeApp({ streamPanel, streamStatus: new FakeElement() });
  const source = FakeEventSource.instances[0];

  source.emit("open");
  source.emit("message", "repeat");
  source.emit("message", "repeat");
  const logText = streamPanel.firstChild;
  assert.equal(streamPanel.textContent, "repeat\nrepeat\n");

  source.emit("error");
  source.emit("open");
  source.emit("message", "repeat");
  source.emit("message", "repeat");
  source.emit("message", "missed while disconnected");
  source.emit("message", "repeat");

  assert.equal(streamPanel.textContent, "repeat\nrepeat\nmissed while disconnected\nrepeat\n");
  assert.equal(streamPanel.firstChild, logText);
});

test("stream interruption closes deliberately without announcing completion and refreshes polling fragments", () => {
  const streamStatus = new FakeElement();
  const streamPanel = new FakeElement({
    dataset: {
      jobStreamUrl: "/jobs/42/logs/stream",
      jobStatusUrl: "/environments/7/status",
      jobHistoryUrl: "/environments/7/jobs",
    },
  });
  const { refreshes } = executeApp({ streamPanel, streamStatus });
  const source = FakeEventSource.instances[0];

  source.emit("interrupted", "stream interrupted");
  assert.equal(source.closed, true);
  assert.match(streamStatus.textContent, /中断/);
  assert.doesNotMatch(streamStatus.textContent, /完成/);
  assert.equal(JSON.stringify(refreshes), JSON.stringify([
    { method: "GET", url: "/environments/7/status", options: { target: "#status", swap: "outerHTML" } },
    { method: "GET", url: "/environments/7/jobs", options: { target: "#job-history", swap: "outerHTML" } },
  ]));

  source.emit("done", "end");
  assert.doesNotMatch(streamStatus.textContent, /完成/);
  assert.equal(refreshes.length, 2);
});

test("copy-log enhancement reveals the command and copies the complete log", async () => {
  const copyTarget = new FakeElement({ id: "job-log", textContent: "line one\nline two" });
  const copyButton = new FakeButton({ dataset: { copyTarget: "job-log" }, hidden: true });
  const copyStatus = new FakeElement();
  copyButton.parentElement = {
    querySelector(selector) {
      return selector === "[data-copy-status]" ? copyStatus : null;
    },
  };

  const { copied } = executeApp({ copyButton, copyTarget });
  assert.equal(copyButton.hidden, false);
  await copyButton.emit("click");
  assert.deepEqual(copied, ["line one\nline two"]);
  assert.equal(copyStatus.textContent, "日志已复制");
});

test("copy-log fallback selects the log when automatic copy is unavailable", async () => {
  const copyTarget = new FakeElement({ id: "job-log", textContent: "diagnostic text" });
  const copyButton = new FakeButton({ dataset: { copyTarget: "job-log" }, hidden: true });
  const copyStatus = new FakeElement();
  copyButton.parentElement = {
    querySelector(selector) {
      return selector === "[data-copy-status]" ? copyStatus : null;
    },
  };

  executeApp({ copyButton, copyTarget, clipboardFails: true, execCopySucceeds: false });
  await copyButton.emit("click");
  assert.equal(copyStatus.textContent, "无法自动复制，日志已选中");
});

test("copy-log exposes and clears a stable busy state while the clipboard request is pending", async () => {
  const copyTarget = new FakeElement({ id: "job-log", textContent: "diagnostic text" });
  const copyButton = new FakeButton({ dataset: { copyTarget: "job-log" }, hidden: true });
  copyButton.setAttribute("data-loading-label", "复制中…");
  copyButton.parentElement = { querySelector() { return new FakeElement(); } };

  const run = executeApp({ copyButton, copyTarget, deferClipboard: true });
  const copying = copyButton.emit("click");
  await Promise.resolve();

  assert.equal(copyButton.getAttribute("aria-busy"), "true");
  assert.equal(copyButton.disabled, true);

  run.releaseClipboard();
  await copying;
  assert.equal(copyButton.getAttribute("aria-busy"), null);
  assert.equal(copyButton.disabled, false);
});

test("disclosure enhancement replaces the no-JavaScript label and synchronizes ARIA with visibility", async () => {
  const panel = new FakeElement({ id: "network-fields" });
  const toggle = new FakeButton({ dataset: { enhancedExpanded: "false" }, hidden: true });
  toggle.setAttribute("aria-controls", panel.id);
  toggle.setAttribute("aria-expanded", "true");
  const fallback = new FakeElement();
  const group = new FakeElement();
  group.querySelector = (selector) => selector === ".disclosure-toggle" ? toggle : fallback;

  executeApp({ disclosureGroup: group, disclosurePanel: panel });

  assert.equal(toggle.hidden, false);
  assert.equal(fallback.hidden, true);
  assert.equal(toggle.getAttribute("aria-expanded"), "false");
  assert.equal(panel.hidden, true);

  await toggle.emit("click");
  assert.equal(toggle.getAttribute("aria-expanded"), "true");
  assert.equal(panel.hidden, false);
});

test("htmx busy feedback keeps metadata source controls successful for cascade serialization", async () => {
  const region = new FakeSelect({ dataset: { metadataSource: "region" } });
  region.value = "ap-southeast-1";
  const run = executeApp();

  await run.emitDocument("htmx:beforeRequest", { target: region, detail: { elt: region } });

  assert.equal(region.getAttribute("aria-busy"), "true");
  assert.equal(region.disabled, false);
  const included = region.disabled ? new URLSearchParams() : new URLSearchParams({ region: region.value });
  assert.equal(included.get("region"), "ap-southeast-1");

  await run.emitDocument("htmx:afterRequest", { target: region, detail: { elt: region } });
  assert.equal(region.getAttribute("aria-busy"), null);
  assert.equal(region.disabled, false);
});

test("native form submissions still disable their submit button to prevent duplicates", async () => {
  const form = new FakeForm();
  const submit = new FakeButton();
  const run = executeApp();

  await run.emitDocument("submit", { target: form, submitter: submit });

  assert.equal(submit.getAttribute("aria-busy"), "true");
  assert.equal(submit.disabled, true);
});

test("busy feedback preserves visible button copy and restores its accessible name", async () => {
  const button = new FakeButton({ textContent: "删除" });
  button.setAttribute("data-loading-label", "删除中…");
  button.setAttribute("aria-label", "删除账号 prod");
  const run = executeApp();

  await run.emitDocument("htmx:beforeRequest", { target: button, detail: { elt: button } });

  assert.equal(button.textContent, "删除");
  assert.equal(button.getAttribute("aria-label"), "删除中…");
  assert.equal(button.getAttribute("aria-busy"), "true");
  assert.equal(button.disabled, true);

  await run.emitDocument("htmx:afterRequest", { target: button, detail: { elt: button } });

  assert.equal(button.textContent, "删除");
  assert.equal(button.getAttribute("aria-label"), "删除账号 prod");
  assert.equal(button.getAttribute("aria-busy"), null);
  assert.equal(button.disabled, false);
});
