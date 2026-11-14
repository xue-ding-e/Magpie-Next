// Keep the large local editor out of every page's initial script budget.
// Unversioned module requests are revalidated by the asset handler's ETag.
let localDictionaryEditor;
function renderLocalDictionary(row) {
  const b = el("button", "text action", t("Manage local dictionary"));
  b.type = "button";
  b.onclick = async () => {
    if (b.disabled) return;
    b.disabled = true;
    try {
      localDictionaryEditor ||= import("./dictionary-editor.js");
      (await localDictionaryEditor).openLocalDictionary();
    } catch {
      localDictionaryEditor = null;
      status(t("Could not open the local dictionary"), "err");
    } finally { b.disabled = false; }
  };
  row(t("Local sensitive dictionary"), t("Exact, case-sensitive literals. Separate from masked words; excluded from setup backup and sync."), b);
}

// Extend the original Privacy renderer after its built-in plugin has added
// Masked words, Never mask, route choices and per-kind actions.
{
  const draw = renderRedact;
  renderRedact = function (...args) {
    draw(...args);
    document.getElementById("localDictionaryRow")?.remove();
    const list = document.getElementById("redactList");
    if (!list) return;
    renderLocalDictionary((name, note, button) => {
      const row = el("div", "row pref");
      row.id = "localDictionaryRow";
      const who = el("div", "who"), value = el("div", "val");
      who.append(el("div", "name", name), el("div", "sub", note));
      value.append(button);
      row.append(who, value);
      list.append(row);
    });
  };
}
