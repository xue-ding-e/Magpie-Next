// Read only the saved account snapshot; neither discovery nor token renewal is
// part of this editor. Disclosure stays bound to the masked preview's revision.
export function showCodexAuth(user) {
  const ed = el("div", "editor codex-auth-export");
  const head = el("div", "ehead");
  head.append(el("b", "", t("Preview saved auth.json")));
  const warning = el("p", "lib-confirm", t("Saved sign-in snapshot. It may have expired. Reveal, copy and export include credentials; share only with someone you trust. API keys are managed separately in Keys."));
  const preview = el("pre", "codex-auth-preview");
  preview.style.cssText = "white-space:pre-wrap;overflow-wrap:anywhere;max-height:45vh;overflow:auto";
  const note = el("p", "lib-confirm");
  note.setAttribute("role", "status");
  const bar = el("div", "bar");
  const reveal = el("button", "text", t("Reveal credentials"));
  const hide = el("button", "text", t("Hide credentials"));
  const copyButton = el("button", "text", t("Copy auth.json"));
  const save = el("button", "text", t("Export auth.json"));
  const cancel = el("button", "text", t("Close"));
  bar.append(reveal, hide, copyButton, save, el("span", "grow"), cancel);
  ed.append(head, warning, preview, note, bar);
  let disposed = false, busy = false, generation = 0, controller = null, revision = "";
  const buttons = [reveal, copyButton, save];
  const clear = () => {
    generation++;
    controller?.abort();
    controller = null;
    busy = false;
    preview.textContent = "";
    note.textContent = "";
    buttons.forEach(b => { b.disabled = !revision; });
  };
  const dispose = () => {
    disposed = true;
    revision = "";
    clear();
    document.removeEventListener("visibilitychange", visibility);
    if (closeCodexAuth === dispose) closeCodexAuth = null;
    if (confirmAsk === ed) confirmAsk = null;
  };
  const visibility = () => { if (document.hidden) { clear(); } };
  const current = (n) => !disposed && n === generation && ed.isConnected && !$("#modal").hidden && !$("#modal").classList.contains("out") && !document.hidden;
  const generate = async (action) => {
    if (busy || disposed || document.hidden || (action !== "preview" && !revision)) return;
    clear();
    if (action === "preview") revision = "";
    busy = true;
    const n = generation;
    controller = new AbortController();
    const signal = controller.signal;
    buttons.forEach(b => { b.disabled = true; });
    let result;
    try {
      const response = await fetch("/api/login/auth-json", {
        method: "POST", headers: { "Content-Type": "application/json" }, signal,
        body: JSON.stringify({ user, revision, action: action === "copy" ? "reveal" : action }),
      });
      if (!response.ok) throw new Error();
      result = await response.json();
      if (!current(n)) return;
      if (action !== "export" || web) {
        if (typeof result.revision !== "string" || !result.revision || (revision && result.revision !== revision)) throw new Error();
        revision = result.revision;
      }
      if (action === "copy") {
        if (typeof result.content !== "string") throw new Error();
        // The general copy() fallback puts the text into a toast on error.
        // Never use that path for credentials, even when copying fails.
        if (web) await navigator.clipboard.writeText(result.content);
        else {
          const copied = await fetch("/api/copy", {
            method: "POST", headers: { "Content-Type": "application/json" }, signal,
            body: JSON.stringify({ text: result.content }),
          });
          if (!copied.ok) throw new Error();
        }
        if (current(n)) note.textContent = t("auth.json copied");
      } else if (action === "export") {
        if (web) {
          if (typeof result.content !== "string") throw new Error();
          const url = URL.createObjectURL(new Blob([result.content], { type: "application/json" }));
          const link = el("a");
          link.href = url;
          link.download = "auth.json";
          // Let the browser consume the URL before releasing its bytes.
          try { link.click(); } finally { setTimeout(() => URL.revokeObjectURL(url), 0); }
          note.textContent = t("auth.json download requested");
        } else if (typeof result.path === "string") {
          note.textContent = t("Saved to {path}", { path: result.path });
        } else throw new Error();
      } else {
        if (typeof result.content !== "string") throw new Error();
        preview.textContent = result.content;
      }
    } catch {
      if (current(n)) {
        preview.textContent = "";
        note.textContent = t("Could not prepare this saved Codex sign-in");
      }
    } finally {
      if (result && typeof result === "object") result.content = "";
      if (current(n)) {
        controller = null;
        busy = false;
        buttons.forEach(b => { b.disabled = !revision; });
      }
    }
  };
  reveal.onclick = () => generate("reveal");
  hide.onclick = () => { clear(); generate("preview"); };
  copyButton.onclick = () => generate("copy");
  save.onclick = () => generate("export");
  cancel.onclick = () => closeConfirmAsk();
  ed.back = () => { dispose(); renderProviders(); };
  confirmAsk = ed;
  openModal(ed);
  closeCodexAuth = dispose;
  document.addEventListener("visibilitychange", visibility);
  $("#modal").classList.add("lib");
  generate("preview");
}
