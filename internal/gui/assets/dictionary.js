// Next-only local dictionary. Contents are never stored in browser persistence,
// sent to an external service, or silently joined to legacy settings saves.
function renderLocalDictionary(row) {
  const b = el("button", "btn", t("Manage local dictionary"));
  b.type = "button";
  b.onclick = openLocalDictionary;
  row(t("Local sensitive dictionary"), t("Exact, case-sensitive literals. Separate from masked words; excluded from setup backup and sync."), b);
}
let localDictionaryDialog = null;
function openLocalDictionary() {
  if (localDictionaryDialog) { localDictionaryDialog.focus(); return; }
  const d = document.createElement("dialog");
  d.className = "local-dictionary";
  d.setAttribute("aria-label", t("Local sensitive dictionary"));
  localDictionaryDialog = d;
  const heading = el("h2", "", t("Local sensitive dictionary"));
  const note = el("p", "", t("Local only. Exact case-sensitive matches; spaces and commas are literal. Up to 100,000 entries and 16 MiB. No changes until Save. Closing during a save does not undo it; reopen to verify the result."));
  const boundary = el("p", "", t("Applies only to fields in requests using privacy masking. Does not clean original sessions, logs or telemetry. Large or dense requests over the protection budget are blocked, not sent partially masked."));
  const limits=el("p","",t("Request budget: 32 MiB scanned text, 4,096 candidate matches, 128 KiB placeholder growth."));
  const summary = el("p", "", t("Loading…"));
  const error = el("p", "dictionary-error"); error.setAttribute("role", "alert");
  const choice = (label, options) => {
    const group = el("fieldset"); group.setAttribute("role", "radiogroup"); group.setAttribute("aria-label", t(label)); group.value=options[0][0]; group.choose=(value)=>{group.value=value;for(const x of group.querySelectorAll("button")){x.setAttribute("aria-checked",String(x.dataset.value===value));x.tabIndex=x.dataset.value===value?0:-1;}};
    group.append(el("legend", "", t(label)));
    for(const [value,text] of options) { const b=el("button","btn",t(text)); b.type="button";b.setAttribute("role","radio");b.setAttribute("aria-checked",String(group.value===value));
      b.dataset.value=value;b.onclick=()=>group.choose(value); group.append(b); }
    group.choose(group.value);
    group.onkeydown=(e)=>{const buttons=[...group.querySelectorAll("button")];const i=buttons.indexOf(document.activeElement);if(i<0||!["ArrowLeft","ArrowRight","Home","End"].includes(e.key))return;e.preventDefault();const next=e.key==="Home"?0:e.key==="End"?buttons.length-1:(i+(e.key==="ArrowRight"?1:-1)+buttons.length)%buttons.length;group.choose(buttons[next].dataset.value);buttons[next].focus({preventScroll:true});};
    return group;
  };
  const mode = choice("Import mode", [["append", "Append"], ["replace", "Replace all"]]);
  const format = choice("Import format", [["text", "One literal per line"], ["json", "JSON string array"]]);
  const input = el("textarea"); input.rows = 9; input.spellcheck = false; input.autocomplete = "off"; input.setAttribute("aria-label", t("Dictionary entries"));
  const file = el("input"); file.type = "file"; file.accept = ".txt,.json,text/plain,application/json"; file.setAttribute("aria-label", t("Import dictionary file"));
  const preview = el("pre", "dictionary-preview"); preview.hidden = true;
  const buttons = el("div", "dictionary-buttons");
  const save = el("button", "btn primary", t("Save")), cancel = el("button", "btn", t("Cancel"));
  const reveal = el("button", "btn", t("Reveal entries")), prev = el("button", "btn", t("Previous")), next = el("button", "btn", t("Next"));
  const reload = el("button", "btn", t("Reload"));
  let state = null, offset = 0, busy = false, shown = false, request = 0;
  const controller = new AbortController();
  const active = () => localDictionaryDialog === d;
  const lock = (v) => { busy = v; save.disabled = v || !state; file.disabled = mode.disabled = format.disabled = input.disabled = v; reload.disabled = reveal.disabled = v; prev.disabled = v || !shown || offset === 0; next.disabled = v || !shown || offset + 100 >= (state?.count || 0); };
  const describe = () => { summary.textContent = `${state.count} ${t("entries")} · ${state.bytes} ${t("bytes")}`; lock(busy); };
  const load = async () => {
    const id = ++request; lock(true); error.textContent = "";
    try {
      const s = await api(`settings/local-dictionary?offset=${offset}&limit=100${shown ? "&reveal=1" : ""}`, undefined, undefined, controller.signal);
      if (!active() || id !== request) return;
      state = s; preview.textContent = shown ? (s.words || []).map((w, i) => `${offset+i+1}: ${JSON.stringify(w)}`).join("\n") : ""; preview.hidden = !shown; describe();
    } catch (e) { if (active() && e.name !== "AbortError") error.textContent = e.message; }
    finally { if (active() && id === request) lock(false); }
  };
  const close = () => { controller.abort(); input.value = file.value = preview.textContent = ""; state = null; d.close(); d.remove(); localDictionaryDialog = null; };
  cancel.onclick = close;
  d.addEventListener("cancel", (e) => { e.preventDefault(); close(); });
  d.addEventListener("keydown", (e) => { e.stopPropagation(); if (e.key === "Escape") { e.preventDefault(); close(); } });
  d.addEventListener("click", (e) => { if (e.target === d) { const r=d.getBoundingClientRect(); if(e.clientX<r.left||e.clientX>r.right||e.clientY<r.top||e.clientY>r.bottom) close(); } });
  file.onchange = async () => {
    const f = file.files?.[0]; if (!f) return;
    error.textContent = "";
    if (f.size > 16 * 1024 * 1024) { error.textContent = t("Import exceeds 16 MiB"); file.value = ""; return; }
    lock(true);
    try { const text = new TextDecoder("utf-8", { fatal: true }).decode(await f.arrayBuffer()); if(active()){ input.value=text; const kind=f.name.toLowerCase().endsWith(".json")?"json":"text";format.choose(kind); } }
    catch { if(active()) error.textContent=t("Could not read UTF-8 file; no changes saved"); }
    finally { if(active()) lock(false); }
  };
  save.onclick = async () => {
    if (!state || busy) return;
    if (new TextEncoder().encode(input.value).length > 16*1024*1024) { error.textContent=t("Import exceeds 16 MiB"); return; }
    lock(true); error.textContent="";
    try { const result=await api("settings/local-dictionary", {revision:state.revision,mode:mode.value,format:format.value,content:input.value},undefined,controller.signal); if(active()){state=result;input.value=file.value=preview.textContent="";shown=false;preview.hidden=true;offset=0;reveal.textContent=t("Reveal entries");describe();} }
    catch(e){if(active()&&e.name!=="AbortError")error.textContent=e.message;}
    finally{if(active())lock(false);}
  };
  reveal.onclick = () => { shown=!shown;offset=0;reveal.textContent=t(shown?"Hide entries":"Reveal entries");if(!shown){++request;preview.textContent="";preview.hidden=true;if(state)delete state.words;lock(false);return;}load(); };
  prev.onclick = () => {offset=Math.max(0,offset-100);load();}; next.onclick = () => {offset+=100;load();}; reload.onclick=()=>{offset=0;load();};
  buttons.append(save,cancel,reload,reveal,prev,next);
  d.append(heading,note,boundary,limits,summary,mode,format,file,input,error,buttons,preview);document.body.append(d);d.showModal();load();
}
