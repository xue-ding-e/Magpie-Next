// The native CSS resize grip is not interactive in every WebKit host. Keep
// one pointer/keyboard handle for both app dialogs and the local dictionary.
const surfaceResizeHandles = new WeakMap();
export function resizeSurface(surface, enabled = true) {
  const existing = surfaceResizeHandles.get(surface);
  if (!enabled) {
    existing?.close();
    surfaceResizeHandles.delete(surface);
    return;
  }
  if (existing) {
    // A quota redraw keeps this node connected, including an active capture.
    if (existing.handle.parentElement !== surface) surface.append(existing.handle);
    return;
  }
  const handle = document.createElement("button");
  handle.type = "button";
  handle.className = "surface-resize";
  handle.setAttribute("aria-label", t("Resize dialog"));
  handle.title = t("Drag to resize. Arrow keys resize; Home resets.");
  const abort = new AbortController();
  const modal = surface.closest(".modal");
  if (modal) {
    bindModalResize(modal, surface, abort.signal);
    surface.addEventListener("wheel", forwardModelWheel, { passive: false, signal: abort.signal });
  }
  let drag = null, frame = 0, sizing = false;
  const keys = new Set();
  const begin = () => {
    if (sizing) return;
    sizing = true;
    surface.classList.add("surface-sizing");
    surface.dispatchEvent(new Event("surface-resize-start"));
  };
  const end = () => {
    if (!sizing) return;
    sizing = false;
    surface.classList.remove("surface-sizing");
    surface.dispatchEvent(new Event("surface-resize-end"));
  };
  const size = (width, height) => {
    const rect = surface.getBoundingClientRect();
    if (!rect.width || !rect.height) return;
    const scaleX = rect.width / surface.offsetWidth || 1;
    const scaleY = rect.height / surface.offsetHeight || 1;
    const parent = surface.closest(".modal");
    const room = parent?.getBoundingClientRect();
    const style = parent && getComputedStyle(parent);
    const maxWidth = parent
      ? (room.width / scaleX - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight))
      : (innerWidth / scaleX - 32);
    const maxHeight = parent
      ? (room.height / scaleY - parseFloat(style.paddingTop) - parseFloat(style.paddingBottom))
      : (innerHeight / scaleY - 48);
    surface.style.width = Math.max(1, Math.min(maxWidth, Math.max(Math.min(360, maxWidth), width))) + "px";
    surface.style.height = Math.max(1, Math.min(maxHeight, Math.max(Math.min(300, maxHeight), height))) + "px";
    surface.dispatchEvent(new Event("surface-resize"));
  };
  const stop = () => {
    const pointer = drag?.pointer;
    drag = null; keys.clear(); end();
    if (pointer !== undefined && handle.hasPointerCapture(pointer)) handle.releasePointerCapture(pointer);
  };
  handle.onpointerdown = (event) => {
    if (event.button !== 0) return;
    event.preventDefault(); event.stopPropagation();
    // The moving corner must not leave a field as the modal's held anchor.
    // Keyboard resizing has the same focus and position behavior.
    begin();
    handle.focus({ preventScroll: true });
    const rect = surface.getBoundingClientRect();
    drag = { pointer: event.pointerId, x: event.clientX, y: event.clientY, width: surface.offsetWidth, height: surface.offsetHeight,
      scaleX: rect.width / surface.offsetWidth || 1, scaleY: rect.height / surface.offsetHeight || 1 };
    handle.setPointerCapture(event.pointerId);
  };
  handle.onpointermove = (event) => {
    if (drag) size(drag.width + (event.clientX - drag.x) / drag.scaleX, drag.height + (event.clientY - drag.y) / drag.scaleY);
  };
  handle.onpointerup = handle.onpointercancel = handle.onlostpointercapture = stop;
  handle.onkeydown = (event) => {
    if (!["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home"].includes(event.key)) return;
    event.preventDefault(); event.stopPropagation();
    if (event.key === "Home") {
      stop();
      begin();
      surface.style.removeProperty("width"); surface.style.removeProperty("height");
      surface.dispatchEvent(new Event("surface-reset"));
      end();
      return;
    }
    begin(); keys.add(event.key);
    const step = event.shiftKey ? 40 : 10;
    size(surface.offsetWidth + (event.key === "ArrowRight" ? step : event.key === "ArrowLeft" ? -step : 0),
      surface.offsetHeight + (event.key === "ArrowDown" ? step : event.key === "ArrowUp" ? -step : 0));
  };
  handle.onkeyup = (event) => {
    keys.delete(event.key);
    if (!keys.size && !drag) end();
  };
  handle.onblur = () => { if (!drag) { keys.clear(); end(); } };
  addEventListener("blur", stop, { signal: abort.signal });
  addEventListener("resize", () => {
    cancelAnimationFrame(frame);
    frame = requestAnimationFrame(() => {
      if (surface.getClientRects().length && (surface.style.width || surface.style.height)) {
        const active = sizing;
        begin(); size(surface.offsetWidth, surface.offsetHeight);
        if (!active) end();
      }
    });
  }, { signal: abort.signal });
  surfaceResizeHandles.set(surface, { handle, close() {
    abort.abort(); cancelAnimationFrame(frame); stop(); handle.remove();
  } });
  surface.append(handle);
}

// The provider dialog shares the app shell's field and scroll anchors.
function bindModalResize(m, d, signal) {
  d.addEventListener("surface-resize-start", () => {
    if (m.hidden) return;
    // Explicit sizing owns the geometry until release. Holding a field (or
    // the moving corner) here would cap growth at a previous footer position.
    modalHold.off = d.offsetTop - parseFloat(getComputedStyle(m).paddingTop);
    modalHold.at = modalHold.cap = modalHold.copy = modalHold.screen = modalHold.ways = null;
    modalHold.touched = true;
    d.style.maxHeight = modalHold.capSet = "100%";
  }, { signal });
  d.addEventListener("surface-resize", () => {
    if (m.hidden) return;
    const c = getComputedStyle(m);
    const space = m.clientHeight - parseFloat(c.paddingTop) - parseFloat(c.paddingBottom);
    // Keep the top still until the requested height needs the room above it.
    modalHold.off = Math.max(0, Math.min(modalHold.off, space - d.offsetHeight));
    d.style.top = modalHold.off + "px";
  }, { signal });
  d.addEventListener("surface-resize-end", () => {
    if (m.hidden) return;
    // A later menu or allowance refresh starts from the reader's new size,
    // never from a field depth or a redraw copy measured before the resize.
    modalHold.off = d.offsetTop - parseFloat(getComputedStyle(m).paddingTop);
    modalHold.at = modalHold.cap = modalHold.copy = modalHold.screen = modalHold.ways = null;
    modalHold.y = 0;
    d.style.maxHeight = modalHold.capSet = modalCap();
    modalMark(document.activeElement);
    holdModal();
  }, { signal });
  d.addEventListener("surface-reset", () => {
    if (m.hidden) return;
    centerModal(m, d);
    holdModal();
  }, { signal });
}

// Short WebKit windows can stop a wheel at the nested list's boundary.
// Continue that wheel inside the editor, leaving normal list scrolling intact.
function forwardModelWheel(event) {
  if (innerHeight > 640 || !event.cancelable || event.ctrlKey || event.shiftKey ||
      !event.deltaY || Math.abs(event.deltaX) >= Math.abs(event.deltaY)) return;
  const list = event.target.closest?.(".mchips"), body = list?.closest(".ebody");
  if (!body || list.scrollHeight <= list.clientHeight ||
      !/^(auto|scroll)$/.test(getComputedStyle(list).overflowY)) return;
  const end = list.scrollHeight - list.clientHeight;
  if (event.deltaY > 0 ? list.scrollTop < end - 1 : list.scrollTop > 1) return;
  const unit = event.deltaMode === 1 ? parseFloat(getComputedStyle(body).lineHeight) || 16
    : event.deltaMode === 2 ? body.clientHeight : 1;
  event.preventDefault();
  body.scrollTop += event.deltaY * unit;
}
