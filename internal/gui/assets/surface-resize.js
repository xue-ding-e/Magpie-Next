// The native CSS resize grip is not interactive in every WebKit host. Keep
// one pointer/keyboard handle for both app dialogs and the local dictionary.
const surfaceResizeHandles = new WeakMap();
function resizeSurface(surface, enabled = true) {
  const existing = surfaceResizeHandles.get(surface);
  if (!enabled) {
    existing?.close();
    surfaceResizeHandles.delete(surface);
    return;
  }
  if (existing) {
    surface.append(existing.handle);
    return;
  }
  const handle = document.createElement("button");
  handle.type = "button";
  handle.className = "surface-resize";
  handle.setAttribute("aria-label", t("Resize dialog"));
  handle.title = t("Drag to resize. Arrow keys resize; Home resets.");
  const abort = new AbortController();
  let drag = null, frame = 0;
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
  };
  const stop = () => { drag = null; surface.classList.remove("surface-sizing"); };
  handle.onpointerdown = (event) => {
    if (event.button !== 0) return;
    event.preventDefault(); event.stopPropagation();
    const rect = surface.getBoundingClientRect();
    drag = { x: event.clientX, y: event.clientY, width: surface.offsetWidth, height: surface.offsetHeight,
      scaleX: rect.width / surface.offsetWidth || 1, scaleY: rect.height / surface.offsetHeight || 1 };
    surface.classList.add("surface-sizing");
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
      surface.style.removeProperty("width"); surface.style.removeProperty("height");
      return;
    }
    const step = event.shiftKey ? 40 : 10;
    size(surface.offsetWidth + (event.key === "ArrowRight" ? step : event.key === "ArrowLeft" ? -step : 0),
      surface.offsetHeight + (event.key === "ArrowDown" ? step : event.key === "ArrowUp" ? -step : 0));
  };
  addEventListener("resize", () => {
    cancelAnimationFrame(frame);
    frame = requestAnimationFrame(() => {
      if (surface.getClientRects().length && (surface.style.width || surface.style.height)) size(surface.offsetWidth, surface.offsetHeight);
    });
  }, { signal: abort.signal });
  surfaceResizeHandles.set(surface, { handle, close() {
    abort.abort(); cancelAnimationFrame(frame); stop(); handle.remove();
  } });
  surface.append(handle);
}
