// Load the saved-sign-in editor only when an account's auth.json is opened.
// Modal close and replacement dispose its controller before changing the DOM.
let closeCodexAuth = null, codexAuthEditor;
function codexAuthButton(user) {
  const b = el("button", "text quiet", t("auth.json"));
  b.type = "button";
  b.title = t("Preview saved auth.json");
  b.onclick = async () => {
    if (b.disabled) return;
    b.disabled = true;
    const modal = $("#modal"), owner = modal.firstElementChild?.firstElementChild;
    // An import finishing after dismissal must not open an old account again.
    const current = () => b.isConnected && !document.hidden && !modal.hidden && !modal.classList.contains("out") && modal.firstElementChild?.firstElementChild === owner;
    try {
      codexAuthEditor ||= import("./codex-auth-editor.js");
      const editor = await codexAuthEditor;
      if (current()) editor.showCodexAuth(user);
    } catch {
      codexAuthEditor = null;
      if (current()) status(t("Could not prepare this saved Codex sign-in"), "err");
    } finally { b.disabled = false; }
  };
  return b;
}
