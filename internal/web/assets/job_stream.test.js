"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");

class FakeElement {
  constructor({ id = "", dataset = {}, textContent = "", hidden = false } = {}) {
    this.id = id;
    this.dataset = dataset;
    this.textContent = textContent;
    this.hidden = hidden;
    this.scrollHeight = 0;
    this.scrollTop = 0;
    this.listeners = new Map();
    this.attributes = new Map();
    this.parentElement = null;
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

  focus() {}
}

class FakeInput extends FakeElement {}
class FakeSelect extends FakeElement {
  constructor(options = {}) {
    super(options);
    this.options = [];
  }
}
class FakeButton extends FakeElement {}
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
  clipboardFails = false,
  execCopySucceeds = true,
} = {}) {
  FakeEventSource.instances = [];
  const refreshes = [];
  const copied = [];
  const byID = new Map();
  if (copyTarget) byID.set(copyTarget.id, copyTarget);
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
    return [];
  };
  const body = new FakeElement();
  const document = {
    body,
    querySelectorAll: queryAll,
    querySelector() { return null; },
    getElementById(id) { return byID.get(id) || null; },
    addEventListener() {},
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
  return { refreshes, copied };
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
  assert.equal(streamPanel.textContent, "repeat\nrepeat\n");

  source.emit("error");
  source.emit("open");
  source.emit("message", "repeat");
  source.emit("message", "repeat");
  source.emit("message", "missed while disconnected");
  source.emit("message", "repeat");

  assert.equal(streamPanel.textContent, "repeat\nrepeat\nmissed while disconnected\nrepeat\n");
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
