"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const metadata = require("../static/blueprint_metadata.js");

function hints() {
  return {
    region: { value: "legacy-region" },
    instanceType: { value: "legacy.large" },
    ami: { value: "ami-legacy" },
  };
}

function fakeSelect(id, value) {
  const listeners = new Map();
  return {
    id,
    value,
    addEventListener(name, listener) {
      const handlers = listeners.get(name) || [];
      handlers.push(listener);
      listeners.set(name, handlers);
    },
    dispatchEvent(event) {
      (listeners.get(event.type) || []).forEach((listener) => listener({ ...event, target: this }));
      return true;
    },
  };
}

const changeEvent = () => ({ type: "change", bubbles: true, isTrusted: false });

function fakeXHR() {
  return {
    aborted: 0,
    abort() {
      this.aborted += 1;
    },
  };
}

function requestDetail(xhr, source, triggeringEvent) {
  return {
    xhr,
    elt: { dataset: { metadataSource: source } },
    requestConfig: { triggeringEvent },
  };
}

test("trusted account change clears all dependent selection hints", () => {
  const values = hints();
  metadata.applySelectionChange("account", true, "", values);
  assert.deepEqual(values, {
    region: { value: "" },
    instanceType: { value: "" },
    ami: { value: "" },
  });
});

test("trusted region and instance changes clear only incompatible descendants", () => {
  const values = hints();
  metadata.applySelectionChange("region", true, "eu-west-1", values);
  assert.equal(values.region.value, "eu-west-1");
  assert.equal(values.instanceType.value, "");
  assert.equal(values.ami.value, "");

  values.ami.value = "ami-old";
  metadata.applySelectionChange("instanceType", true, "m7g.large", values);
  assert.equal(values.instanceType.value, "m7g.large");
  assert.equal(values.ami.value, "");
});

test("programmatic hydration retains hints until swaps synchronize actual values", () => {
  const values = hints();
  metadata.applySelectionChange("region", false, "ap-southeast-1", values);
  assert.equal(values.instanceType.value, "legacy.large");
  assert.equal(values.ami.value, "ami-legacy");

  metadata.syncHint("region", "ap-southeast-1", values);
  metadata.syncHint("instanceType", "t3.micro", values);
  metadata.syncHint("ami", "ami-current", values);
  assert.deepEqual(values, {
    region: { value: "ap-southeast-1" },
    instanceType: { value: "t3.micro" },
    ami: { value: "ami-current" },
  });
});

test("metadata swaps continue the htmx cascade through the AMI request", () => {
  assert.equal(typeof metadata.continueCascadeAfterSwap, "function");

  const values = hints();
  const requests = [];
  const region = fakeSelect("region-select", "legacy-region");
  region.addEventListener("change", () => {
    requests.push(`/blueprints/instance-types?region=${region.value}&selected_instance_type=${values.instanceType.value}`);
  });
  const instanceType = fakeSelect("instance-type-select", "legacy.large");
  instanceType.addEventListener("change", () => {
    requests.push(`/blueprints/amis?instance_type=${instanceType.value}&selected_ami=${values.ami.value}`);
  });
  const ami = fakeSelect("ami-select", "ami-legacy");
  ami.addEventListener("change", () => requests.push("unexpected extra request"));

  metadata.applySelectionChange("account", false, "1", values);
  metadata.continueCascadeAfterSwap(region, values, changeEvent);
  metadata.continueCascadeAfterSwap(instanceType, values, changeEvent);
  metadata.continueCascadeAfterSwap(ami, values, changeEvent);

  assert.deepEqual(requests, [
    "/blueprints/instance-types?region=legacy-region&selected_instance_type=legacy.large",
    "/blueprints/amis?instance_type=legacy.large&selected_ami=ami-legacy",
  ]);
});

test("trusted parent changes clear legacy hints before the continued AMI request", () => {
  assert.equal(typeof metadata.continueCascadeAfterSwap, "function");

  const values = hints();
  const requests = [];
  metadata.applySelectionChange("region", true, "eu-west-1", values);

  const instanceType = fakeSelect("instance-type-select", "m7g.large");
  instanceType.addEventListener("change", () => {
    requests.push(`/blueprints/amis?instance_type=${instanceType.value}&selected_ami=${values.ami.value}`);
  });
  metadata.continueCascadeAfterSwap(instanceType, values, changeEvent);

  assert.equal(values.instanceType.value, "m7g.large");
  assert.equal(values.ami.value, "");
  assert.deepEqual(requests, ["/blueprints/amis?instance_type=m7g.large&selected_ami="]);
});

test("trusted parent change rejects an in-flight hydration response", () => {
  assert.equal(typeof metadata.createCascadeCoordinator, "function");

  const values = hints();
  const coordinator = metadata.createCascadeCoordinator(values);
  const hydrationXHR = fakeXHR();
  const hydration = requestDetail(hydrationXHR, "account", { type: "load" });
  coordinator.beforeRequest(hydration);

  const userChange = { type: "change", isTrusted: true };
  coordinator.selectionChanged("account", userChange, "2");

  const region = fakeSelect("region-select", "legacy-region");
  let downstreamRequests = 0;
  region.addEventListener("change", () => downstreamRequests += 1);
  if (coordinator.beforeSwap(hydration)) coordinator.afterSwap(region, changeEvent);

  assert.equal(hydrationXHR.aborted, 1);
  assert.deepEqual(values, {
    region: { value: "" },
    instanceType: { value: "" },
    ami: { value: "" },
  });
  assert.equal(downstreamRequests, 0);
});

test("rapid metadata changes accept only the newest htmx response", () => {
  assert.equal(typeof metadata.createCascadeCoordinator, "function");

  const values = hints();
  const coordinator = metadata.createCascadeCoordinator(values);
  const requests = [];
  const responseDetails = [];
  for (const value of ["region-a", "region-b", "region-c"]) {
    const event = { type: "change", isTrusted: true };
    coordinator.selectionChanged("region", event, value);
    const xhr = fakeXHR();
    const detail = requestDetail(xhr, "region", event);
    coordinator.beforeRequest(detail);
    responseDetails.push(detail);
  }

  for (const [index, detail] of [responseDetails[1], responseDetails[0], responseDetails[2]].entries()) {
    const instanceType = fakeSelect("instance-type-select", `type-${index}`);
    instanceType.addEventListener("change", () => requests.push(instanceType.value));
    if (coordinator.beforeSwap(detail)) coordinator.afterSwap(instanceType, changeEvent);
    coordinator.afterRequest(detail);
  }

  assert.deepEqual(requests, ["type-2"]);
  assert.equal(values.instanceType.value, "type-2");
});

test("queued request retains the generation of its original triggering event", () => {
  assert.equal(typeof metadata.createCascadeCoordinator, "function");

  const coordinator = metadata.createCascadeCoordinator(hints());
  const queuedEvent = { type: "change", isTrusted: true };
  coordinator.selectionChanged("region", queuedEvent, "region-old");
  coordinator.selectionChanged("region", { type: "change", isTrusted: true }, "region-new");

  const queued = requestDetail(fakeXHR(), "region", queuedEvent);
  coordinator.beforeRequest(queued);
  assert.equal(coordinator.beforeSwap(queued), false);
});

test("Redis AUTH remains server-functional and enhancement disables it when Redis is off", () => {
  const redisEnabled = { checked: false };
  const redisAuth = { checked: true, disabled: false };
  metadata.syncRedisAuth(redisEnabled, redisAuth);
  assert.equal(redisAuth.disabled, true);
  assert.equal(redisAuth.checked, false);

  redisEnabled.checked = true;
  metadata.syncRedisAuth(redisEnabled, redisAuth);
  assert.equal(redisAuth.disabled, false);
});
