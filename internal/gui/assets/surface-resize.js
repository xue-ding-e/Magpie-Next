// Keep the shared pointer/keyboard controller out of the initial page load.
// A disabled or replaced surface invalidates a pending import before it can
// attach a handle; closing an existing one disposes all of its listeners.
let surfaceResizeEditor, surfaceResizeLoading;
const surfaceResizePending = new WeakMap();
function resizeSurface(surface, enabled = true) {
  surfaceResizePending.delete(surface);
  if (surfaceResizeEditor) { surfaceResizeEditor.resizeSurface(surface, enabled); return; }
  if (!enabled) return;
  const pending = {};
  surfaceResizePending.set(surface, pending);
  surfaceResizeLoading ||= import("./surface-resize-editor.js").catch(error => { surfaceResizeLoading = null; throw error; });
  surfaceResizeLoading.then(editor => {
    surfaceResizeEditor = editor;
    if (surfaceResizePending.get(surface) !== pending) return;
    surfaceResizePending.delete(surface);
    if (surface.isConnected) editor.resizeSurface(surface);
  }, () => {
    if (surfaceResizePending.get(surface) !== pending) return;
    surfaceResizePending.delete(surface);
    if (surface.isConnected) status(t("Could not load dialog resizing. Please reopen the editor."), "err");
  });
}

// A key toggle can reorder rows. Keep the reader's scroll instead of
// holding the screen position of a control in the old slot.
function releaseMovedKeyAnchor() {
  modalHold.at = modalHold.copy = modalHold.ways = modalHold.screen = null;
  modalHold.y = 0;
}
