(function (root, factory) {
  "use strict";
  const api = factory();
  if (typeof module === "object" && module.exports) module.exports = api;
  if (root) root.HermesBlueprintMetadata = api;
})(typeof globalThis !== "undefined" ? globalThis : this, function () {
  "use strict";

  const descendants = {
    account: ["region", "instanceType", "ami"],
    region: ["instanceType", "ami"],
    instanceType: ["ami"],
  };

  function applySelectionChange(source, isTrusted, value, hints) {
    if (!isTrusted || !descendants[source]) return;
    if (source !== "account" && hints[source]) hints[source].value = value;
    descendants[source].forEach((name) => {
      if (hints[name]) hints[name].value = "";
    });
  }

  function syncHint(name, value, hints) {
    if (hints[name]) hints[name].value = value;
  }

  function continueCascadeAfterSwap(target, hints, createChangeEvent) {
    const step = {
      "region-select": { hint: "region", advance: true },
      "instance-type-select": { hint: "instanceType", advance: true },
      "ami-select": { hint: "ami", advance: false },
    }[target?.id];
    if (!step) return;

    syncHint(step.hint, target.value, hints);
    if (!step.advance) return;
    const event = createChangeEvent
      ? createChangeEvent()
      : new Event("change", { bubbles: true });
    target.dispatchEvent(event);
  }

  function createCascadeCoordinator(hints) {
    let generation = 0;
    const eventGenerations = new WeakMap();
    const requestGenerations = new WeakMap();
    const activeRequests = new Set();

    function remember(map, key, value) {
      if ((typeof key === "object" && key !== null) || typeof key === "function") {
        map.set(key, value);
      }
    }

    function selectionChanged(source, event, value) {
      if (!event?.isTrusted || !descendants[source]) return;
      generation += 1;
      remember(eventGenerations, event, generation);
      applySelectionChange(source, true, value, hints);
      activeRequests.forEach((xhr) => {
        if (typeof xhr.abort === "function") xhr.abort();
      });
      activeRequests.clear();
    }

    function beforeRequest(detail) {
      const xhr = detail?.xhr;
      const source = detail?.elt?.dataset?.metadataSource;
      if (!xhr || !source) return;
      const triggeringEvent = detail.requestConfig?.triggeringEvent;
      const requestGeneration = eventGenerations.get(triggeringEvent) ?? generation;
      remember(requestGenerations, xhr, requestGeneration);
      activeRequests.add(xhr);
    }

    function beforeSwap(detail) {
      const xhr = detail?.xhr;
      if (!xhr || !requestGenerations.has(xhr)) return true;
      return requestGenerations.get(xhr) === generation;
    }

    function afterSwap(target, createChangeEvent) {
      continueCascadeAfterSwap(target, hints, () => {
        const event = createChangeEvent
          ? createChangeEvent()
          : new Event("change", { bubbles: true });
        remember(eventGenerations, event, generation);
        return event;
      });
    }

    function afterRequest(detail) {
      if (detail?.xhr) activeRequests.delete(detail.xhr);
    }

    return { selectionChanged, beforeRequest, beforeSwap, afterSwap, afterRequest };
  }

  function syncRedisAuth(redisEnabled, redisAuth) {
    redisAuth.disabled = !redisEnabled.checked;
    if (redisAuth.disabled) redisAuth.checked = false;
  }

  return { applySelectionChange, continueCascadeAfterSwap, createCascadeCoordinator, syncHint, syncRedisAuth };
});
