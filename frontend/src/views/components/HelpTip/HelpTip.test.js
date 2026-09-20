import assert from "node:assert/strict";
import test from "node:test";
import { placeTooltip } from "./HelpTipPosition.js";
import { useHelpTip } from "./HelpTip.js";
import { mountComposable } from "../../../test-support/mount.js";

const viewport = { width: 1366, height: 768 };
const popup = { width: 340, height: 70 };

test("tooltip stays below the trigger when space is available", () => {
  assert.deepEqual(placeTooltip({ left: 100, top: 100, bottom: 117 }, popup, viewport), {
    left: 100, top: 125,
  });
});

test("tooltip flips above the trigger near the bottom edge", () => {
  assert.deepEqual(placeTooltip({ left: 100, top: 720, bottom: 737 }, popup, viewport), {
    left: 100, top: 642,
  });
});

test("tooltip clamps at both horizontal edges", () => {
  assert.equal(placeTooltip({ left: 1320, top: 100, bottom: 117 }, popup, viewport).left, 1014);
  assert.equal(placeTooltip({ left: -10, top: 100, bottom: 117 }, popup, viewport).left, 12);
});

test("tooltip handles a viewport smaller than its measured content", () => {
  assert.deepEqual(placeTooltip({ left: 0, top: 0, bottom: 17 }, popup, { width: 100, height: 50 }), {
    left: 12, top: 12,
  });
});

async function setup(t) {
  const events = new Map();
  function addEventListener(name, callback) {
    events.set(name, callback);
  }
  function removeEventListener(name, callback) {
    assert.equal(events.get(name), callback);
    events.delete(name);
  }
  const mounted = await mountComposable(t, () => {
    Object.assign(window, { ...viewport, innerWidth: viewport.width, innerHeight: viewport.height, addEventListener, removeEventListener });
    Object.assign(document, { activeElement: null, addEventListener, removeEventListener });
    return useHelpTip();
  });
  mounted.state.trigger.value = {
    getBoundingClientRect: () => ({ left: 100, top: 100, bottom: 117 }),
    contains: () => false,
  };
  mounted.state.tooltip.value = {
    getBoundingClientRect: () => popup,
    contains: () => false,
  };
  return { ...mounted, events };
}

test("tooltip opens, positions and closes on Escape", async (t) => {
  const { state, events } = await setup(t);
  assert.equal(state.visible.value, false);
  await state.show();
  assert.equal(state.visible.value, true);
  assert.deepEqual(state.position.value, { left: "100px", top: "125px" });
  events.get("keydown")({ key: "Escape" });
  assert.equal(state.visible.value, false);
});

test("tooltip can be reached by pointer without closing", async (t) => {
  const { state } = await setup(t);
  await state.show();
  state.scheduleHide();
  state.cancelHide();
  await new Promise(resolve => setTimeout(resolve, 180));
  assert.equal(state.visible.value, true);
  state.scheduleHide();
  await new Promise(resolve => setTimeout(resolve, 180));
  assert.equal(state.visible.value, false);
});

test("focused tooltip remains open on pointer leave", async (t) => {
  const { state } = await setup(t);
  document.activeElement = state.trigger.value;
  await state.show();
  state.scheduleHide();
  await new Promise(resolve => setTimeout(resolve, 180));
  assert.equal(state.visible.value, true);
  state.hide();
  assert.equal(state.visible.value, false);
});

test("outside click and viewport changes dismiss the tooltip", async (t) => {
  const { state, events } = await setup(t);
  for (const event of ["pointerdown", "resize", "scroll"]) {
    await state.show();
    events.get(event)({ target: {} });
    assert.equal(state.visible.value, false);
  }
});

test("unmount cancels pending work and removes all event listeners", async (t) => {
  const { state, events, unmount } = await setup(t);
  const pending = state.show();
  state.scheduleHide();
  unmount();
  await pending;
  assert.equal(state.visible.value, false);
  assert.equal(events.size, 0);
});
