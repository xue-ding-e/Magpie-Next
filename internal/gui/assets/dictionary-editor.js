// Next-only local dictionary. Contents are never stored in browser persistence,
// sent to an external service, or silently joined to legacy settings saves.
let localDictionaryDialog = null;
export function openLocalDictionary() {
  if (localDictionaryDialog) { localDictionaryDialog.focus(); return; }
  const d = document.createElement("dialog");
  d.className = "local-dictionary";
  d.setAttribute("aria-label", t("Local sensitive dictionary"));
  localDictionaryDialog = d;
  const heading = el("h2", "", t("Local sensitive dictionary"));
  const head = el("div", "dictionary-head");
  const dismiss = el("button", "text", "×");
  dismiss.type = "button";
  dismiss.setAttribute("aria-label", t("Close"));
  head.append(heading, dismiss);
  const body = el("div", "dictionary-body");
  const help = el("details", "dictionary-help");
  help.append(el("summary", "", t("Matching rules and limits")));
  const note = el("p", "", t("Local only. Exact case-sensitive matches; spaces and commas are literal. Up to 100,000 entries and 16 MiB. No changes until Save. Closing during a save does not undo it; reopen to verify the result."));
  const boundary = el("p", "", t("Applies only to fields in requests using privacy masking. Does not clean original sessions, logs or telemetry. Large or dense requests over the protection budget are blocked, not sent partially masked."));
  const limits=el("p","",t("Request budget: 32 MiB scanned text, 4,096 candidate matches, 128 KiB placeholder growth."));
  const summary = el("p", "dictionary-summary", t("Loading…"));
  summary.setAttribute("aria-live", "polite");
  const error = el("p", "dictionary-error"); error.setAttribute("role", "alert");
  const choice = (label, options) => {
    const group = el("fieldset", "dictionary-choice"); group.setAttribute("role", "radiogroup"); group.setAttribute("aria-label", t(label)); group.value=options[0][0]; group.choose=(value)=>{group.value=value;for(const x of group.querySelectorAll("button")){x.setAttribute("aria-checked",String(x.dataset.value===value));x.classList.toggle("on",x.dataset.value===value);x.tabIndex=x.dataset.value===value?0:-1;}};
    group.append(el("legend", "", t(label)));
    for(const [value,text] of options) { const b=el("button","text",t(text)); b.type="button";b.setAttribute("role","radio");b.setAttribute("aria-checked",String(group.value===value));
      b.dataset.value=value;b.onclick=()=>{group.choose(value);group.onchoose?.(value);}; group.append(b); }
    group.choose(group.value);
    group.onkeydown=(e)=>{const buttons=[...group.querySelectorAll("button")];const i=buttons.indexOf(document.activeElement);if(i<0||!["ArrowLeft","ArrowRight","Home","End"].includes(e.key))return;e.preventDefault();const next=e.key==="Home"?0:e.key==="End"?buttons.length-1:(i+(e.key==="ArrowRight"?1:-1)+buttons.length)%buttons.length;const value=buttons[next].dataset.value;group.choose(value);group.onchoose?.(value);buttons[next].focus({preventScroll:true});};
    return group;
  };
  const mode = choice("Import mode", [["append", "Append"], ["replace", "Replace all"]]);
  const target = choice("Dictionary list", [["mask", "Mask these literals"], ["allow", "Never mask"]]);
  let selectedTarget = "mask";
  const policy = el("p", "dictionary-policy");
  const describePolicy = () => {
    policy.textContent = t(selectedTarget === "allow"
      ? "These exact literals stay visible. They take priority over masking rules and the masking list; surrounding sensitive text is still masked."
      : "These exact literals are masked in protected requests. Add exceptions to Never mask.");
  };
  describePolicy();
  const format = choice("Import format", [["text", "One literal per line"], ["json", "JSON string array"]]);
  const input = el("textarea"); input.rows = 9; input.spellcheck = false; input.autocomplete = "off"; input.setAttribute("aria-label", t("Dictionary entries"));
  const file = el("input"); file.type = "file"; file.accept = ".txt,.json,text/plain,application/json"; file.setAttribute("aria-label", t("Import dictionary file"));
  file.hidden = true;
  const pickFile = el("button", "text action", t("Choose file"));
  pickFile.type = "button";
  pickFile.onclick = () => file.click();
  const filename = el("span", "dictionary-filename");
  const fileRow = el("div", "dictionary-file");
  fileRow.append(pickFile, filename, file);
  const imports = el("div", "dictionary-imports");
  imports.append(mode, format, fileRow);
  const example = el("details", "dictionary-example");
  example.open = true;
  example.append(el("summary", "", t("Example for this selection")));
  const exampleMode = el("p"), exampleFormat = el("p"), exampleResult = el("p");
  const sample = el("pre");
  const useExample = el("button", "text action", t("Use example"));
  useExample.type = "button";
  example.append(exampleMode, exampleFormat, sample, exampleResult, useExample);
  const describeExample = () => {
    const allow = selectedTarget === "allow";
    const words = allow ? ["public@example.test", "PUBLIC_PROJECT"] : ["DEMO_SECRET_123", "INTERNAL_PROJECT"];
    sample.textContent = format.value === "json" ? JSON.stringify(words, null, 2) : words.join("\n");
    input.placeholder = sample.textContent;
    exampleMode.textContent = t(allow
      ? mode.value === "replace"
        ? "Replace all replaces only Never mask; Mask these literals stays unchanged."
        : "Append adds these entries to Never mask and keeps existing entries."
      : mode.value === "replace"
        ? "Replace all replaces only Mask these literals; Never mask stays unchanged."
        : "Append adds these entries to Mask these literals and keeps existing entries.");
    exampleFormat.textContent = t(format.value === "json"
      ? "Use a JSON string array. Escape quotes, backslashes and line breaks inside an entry."
      : "One entry per line. Spaces and commas stay inside an entry.");
    exampleResult.textContent = t(allow
      ? "These example literals stay visible even when another masking rule matches."
      : "These example literals are masked in protected requests unless Never mask contains an exception.");
  };
  mode.onchoose = format.onchoose = describeExample;
  describeExample();
  const preview = el("pre", "dictionary-preview"); preview.hidden = true;
  const saved = el("section", "dictionary-saved"); saved.hidden = true;
  saved.setAttribute("aria-label", t("Saved entries"));
  const savedHead = el("div", "dictionary-saved-head");
  const pageLabel = el("span", "dictionary-page"); pageLabel.setAttribute("aria-live", "polite");
  const edit = el("button", "text action", t("Edit this page")); edit.type = "button";
  savedHead.append(el("h3", "", t("Saved entries")), pageLabel, edit);
  const pageEditor = el("div", "dictionary-page-editor"); pageEditor.hidden = true;
  const pageHelp = el("p", "", t("Edit or remove entries on this page, then save changes. Other pages and the other list are kept. Empty entries are removed; duplicates are kept once."));
  const rows = el("div", "dictionary-entry-list");
  const pageButtons = el("div", "dictionary-page-buttons");
  const savePage = el("button", "text action primary", t("Save changes"));
  const discardPage = el("button", "text action", t("Discard edits"));
  savePage.type = discardPage.type = "button";
  pageButtons.append(savePage, discardPage);
  pageEditor.append(pageHelp, rows, pageButtons);
  saved.append(savedHead, preview, pageEditor);
  const buttons = el("div", "dictionary-buttons");
  const save = el("button", "text action primary", t("Save")), cancel = el("button", "text action", t("Cancel"));
  const reveal = el("button", "text action", t("View saved entries")), prev = el("button", "text action", t("Previous")), next = el("button", "text action", t("Next"));
  const reload = el("button", "text action", t("Reload"));
  let state = null, revision = null, offset = 0, busy = false, shown = false, request = 0;
  let pageRevision = null, pageCount = 0, editing = false, edits = [];
  const controller = new AbortController();
  const active = () => localDictionaryDialog === d;
  const pageDirty = () => edits.some((x) => x.removed || x.field.value !== x.original);
  const dirty = () => input.value || file.value || pageDirty();
  const lock = (v) => {
    busy = v;
    save.disabled = v || !state || editing;
    pickFile.disabled = file.disabled = mode.disabled = format.disabled = input.disabled = useExample.disabled = v || editing;
    target.disabled = reload.disabled = reveal.disabled = v;
    prev.hidden = next.hidden = !shown;
    prev.disabled = v || !shown || offset === 0;
    next.disabled = v || !shown || offset + 100 >= (state?.count || 0);
    edit.hidden = editing;
    edit.disabled = v || !pageRevision || !pageCount;
    savePage.disabled = v || !pageDirty();
    discardPage.disabled = v;
    for (const x of edits) { x.field.disabled = v || x.removed; x.remove.disabled = v; }
  };
  const describe = () => {
    summary.textContent = `${state.count} ${t("entries")} · ${state.bytes} ${t("bytes")}`;
    pageLabel.textContent = pageCount ? `${offset + 1}–${offset + pageCount} / ${state.count}` : "";
    lock(busy);
  };
  const clearPageEditor = () => {
    for (const x of edits) { x.field.value = x.original = ""; }
    edits = []; rows.replaceChildren(); editing = false;
    pageEditor.hidden = true; preview.hidden = !shown;
  };
  const clearDraft = () => { input.value = file.value = filename.textContent = ""; clearPageEditor(); };
  const load = async (reloadRevision = false) => {
    const id = ++request; lock(true); error.textContent = "";
    pageRevision = null; pageCount = 0; preview.textContent = "";
    try {
      const s = await api(`settings/local-dictionary?offset=${offset}&limit=100&target=${selectedTarget}${shown ? "&reveal=1" : ""}`, undefined, undefined, controller.signal);
      if (!active() || id !== request) return;
      if (shown && offset > 0 && offset >= s.count) {
        offset = Math.max(0, Math.floor((s.count - 1) / 100) * 100);
        await load(reloadRevision);
        return;
      }
      // Paging and reveal may see a newer library, but only an explicit
      // Reload may rebase a draft that will replace another writer's entries.
      if (reloadRevision || revision === null) revision = s.revision;
      state = s;
      if (shown) { pageRevision = s.revision; pageCount = (s.words || []).length; }
      preview.textContent = shown ? pageCount
        ? s.words.map((w, i) => `${offset+i+1}: ${JSON.stringify(w)}`).join("\n")
        : t("No saved entries in this list.") : "";
      preview.hidden = !shown; saved.hidden = !shown; describe();
    } catch (e) { if (active() && e.name !== "AbortError") error.textContent = e.message; }
    finally { if (active() && id === request) lock(false); }
  };
  const close = async () => {
    if (!busy && dirty() && !(await confirmDiscard())) return;
    if (!active()) return;
    controller.abort(); clearDraft(); preview.textContent = "";
    state = revision = pageRevision = null;
    resizeSurface(d, false); d.close(); d.remove(); localDictionaryDialog = null;
  };
  cancel.onclick = close;
  dismiss.onclick = close;
  target.onchoose = async (value) => {
    target.choose(selectedTarget);
    if (busy || value === selectedTarget) return;
    lock(true);
    if (dirty() && !(await confirmDiscard())) { if (active()) lock(false); return; }
    if (!active()) return;
    selectedTarget = value;
    target.choose(value);
    clearDraft(); preview.textContent = "";
    state = revision = null;
    shown = false; offset = 0; preview.hidden = saved.hidden = true;
    reveal.textContent = t("View saved entries");
    describePolicy(); describeExample();
    await load(true);
  };
  d.addEventListener("cancel", (e) => { e.preventDefault(); close(); });
  d.addEventListener("keydown", (e) => { e.stopPropagation(); if (e.key === "Escape") { e.preventDefault(); close(); } });
  const onBackdrop = backdropClick(d, (e) => {
    const r=d.getBoundingClientRect();
    return e.target===d && (e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom);
  });
  d.addEventListener("click", (e) => { if(onBackdrop(e))close(); });
  file.onchange = async () => {
    const f = file.files?.[0]; if (!f) return;
    error.textContent = "";
    if (f.size > 16 * 1024 * 1024) { error.textContent = t("Import exceeds 16 MiB"); file.value = ""; return; }
    lock(true);
    try { const text = new TextDecoder("utf-8", { fatal: true }).decode(await f.arrayBuffer()); if(active()){ input.value=text;filename.textContent=f.name; const kind=f.name.toLowerCase().endsWith(".json")?"json":"text";format.choose(kind);describeExample(); } }
    catch { if(active()) error.textContent=t("Could not read UTF-8 file; no changes saved"); }
    finally { if(active()) lock(false); }
  };
  save.onclick = async () => {
    if (!state || busy || editing) return;
    if (new TextEncoder().encode(input.value).length > 16*1024*1024) { error.textContent=t("Import exceeds 16 MiB"); return; }
    if (mode.value === "replace") {
      lock(true);
      const confirmed = await confirmAction(t("Replace all"), t("Replace all saved entries in the selected list? The other list stays unchanged."), t("Replace all"));
      if (!active()) return;
      if (!confirmed) { lock(false); return; }
    }
    lock(true); error.textContent="";
    try { const result=await api("settings/local-dictionary", {revision,mode:mode.value,format:format.value,content:input.value,target:selectedTarget},undefined,controller.signal); if(active()){state=result;revision=result.revision;clearDraft();preview.textContent="";shown=false;preview.hidden=saved.hidden=true;pageRevision=null;pageCount=0;offset=0;reveal.textContent=t("View saved entries");describe();} }
    catch(e){if(active()&&e.name!=="AbortError")error.textContent=e.message;}
    finally{if(active())lock(false);}
  };
  reveal.onclick = async () => {
    if (busy) return;
    if (pageDirty()) {
      lock(true);
      if (!(await confirmDiscard())) { if (active()) lock(false); return; }
      if (!active()) return;
    }
    clearPageEditor(); shown = !shown; offset = 0;
    reveal.textContent = t(shown ? "Hide entries" : "View saved entries");
    if (!shown) {
      ++request; preview.textContent = ""; preview.hidden = saved.hidden = true;
      pageRevision = null; pageCount = 0; if (state) delete state.words; lock(false); return;
    }
    await load();
  };
  const turnPage = async (step) => {
    if (busy) return;
    lock(true);
    if (pageDirty() && !(await confirmDiscard())) { if (active()) lock(false); return; }
    if (!active()) return;
    clearPageEditor(); offset = Math.max(0, offset + step); await load();
  };
  prev.onclick = () => turnPage(-100); next.onclick = () => turnPage(100);
  reload.onclick=async()=>{
    if(busy)return;
    lock(true);
    if(dirty() && !(await confirmDiscard())) { if(active())lock(false);return; }
    if(!active())return;
    clearDraft();preview.textContent="";offset=0;
    await load(true);
  };
  useExample.onclick = async () => {
    if (busy || editing) return;
    lock(true);
    if ((input.value || file.value) && !(await confirmDiscard())) { if (active()) lock(false); return; }
    if (!active()) return;
    input.value = sample.textContent; file.value = filename.textContent = "";
    lock(false); input.focus({preventScroll: true});
  };
  edit.onclick = async () => {
    if (busy || !pageRevision || !pageCount) return;
    lock(true);
    if ((input.value || file.value) && !(await confirmDiscard())) { if (active()) lock(false); return; }
    if (!active()) return;
    clearDraft(); editing = true; preview.hidden = true; pageEditor.hidden = false;
    edits = (state.words || []).map((word, i) => {
      const row = el("div", "dictionary-entry");
      const label = el("label", "dictionary-entry-label", `${offset + i + 1}`);
      const field = el("textarea"); field.rows = 1; field.spellcheck = false;
      field.autocomplete = "off"; field.value = word;
      field.id = `dictionary-entry-${i}`;
      field.setAttribute("aria-label", t("Saved entry {n}", {n: offset + i + 1}));
      label.htmlFor = field.id;
      const remove = el("button", "text action", t("Remove entry")); remove.type = "button";
      const entry = {original: word, field, remove, removed: false};
      field.oninput = () => lock(busy);
      remove.onclick = () => {
        if (busy) return;
        entry.removed = !entry.removed;
        row.classList.toggle("pending-delete", entry.removed);
        remove.textContent = t(entry.removed ? "Undo" : "Remove entry");
        lock(false);
      };
      row.append(label, field, remove); rows.append(row);
      return entry;
    });
    lock(false); edits[0]?.field.focus({preventScroll: true});
  };
  discardPage.onclick = async () => {
    if (busy) return;
    lock(true);
    if (pageDirty() && !(await confirmDiscard())) { if (active()) lock(false); return; }
    if (!active()) return;
    clearPageEditor(); lock(false);
  };
  savePage.onclick = async () => {
    if (busy || !editing || !pageRevision || !pageDirty()) return;
    const words = edits.filter((x) => !x.removed).map((x) => x.field.value);
    if (words.reduce((n, word) => n + new TextEncoder().encode(word).length, 0) > 16 * 1024 * 1024) {
      error.textContent = t("Import exceeds 16 MiB"); return;
    }
    lock(true); error.textContent = "";
    try {
      const result = await api("settings/local-dictionary", {
        revision: pageRevision, mode: "edit", format: "json", content: "",
        target: selectedTarget, offset, count: pageCount, words,
      }, undefined, controller.signal);
      if (!active()) return;
      state = result; revision = result.revision;
      clearPageEditor();
      await load(true);
    } catch (e) { if (active() && e.name !== "AbortError") error.textContent = e.message; }
    finally { if (active()) lock(false); }
  };
  for (const b of [save, cancel, reload, reveal, prev, next]) b.type = "button";
  buttons.append(save,cancel,reload,reveal,prev,next);
  help.append(note, boundary, limits);
  body.append(target, policy, summary, imports, example, input, error, saved, help);
  d.append(head, body, buttons);resizeSurface(d);document.body.append(d);d.showModal();load();
}
