// The provider's optional priority window is staged with Routing and made
// with Save. Loaded only when that editor opens; it uses the app's existing
// controls so a choice keeps the editor, its focus and its scroll position.
window.resetPriorityField = function (p, box) {
  const saved = p.resetPriority || { hours: 0 }, initial = draft.resetPriority ?? saved;
  const [toggle, enabled] = tick(t("Prefer accounts near reset"), initial.hours > 0);
  const details = el("div", "reset-priority-options");
  let cycle = initial.window || "week", order = initial.order || "";
  const hours = input(initial.hours || 24, "24");
  hours.type = "number";
  hours.min = "0"; hours.max = "8760"; hours.step = "any";
  hours.style.width = "7em";
  hours.classList.add("reset-priority-hours");
  hours.setAttribute("aria-label", t("Hours before reset"));
  const unsaved = el("span", "hint munsaved", t("unsaved"));
  unsaved.title = t("Made when the provider is saved; Cancel drops it");
  const current = () => ({ hours: enabled.checked ? Number(hours.value) : 0, window: cycle, order });
  const draw = () => {
    details.hidden = !enabled.checked;
    const r = current();
    unsaved.hidden = r.hours === (saved.hours || 0) && (!r.hours || cycle === (saved.window || "week") && order === (saved.order || ""));
  };
  const changed = () => { draft.resetPriority = current(); draw(); };
  enabled.onchange = changed;
  hours.oninput = changed;
  const cycles = [["week", "Weekly quota"], ["month", "Monthly quota"], ["day", "Daily quota"], ["short", "Short quota window"], ["any", "Any quota window"]];
  details.append(...field(t("Quota cycle"), segs(cycles.map(([v, name]) => [v, t(name)]), cycle, (v) => { cycle = v; changed(); })),
    ...field(t("Hours before reset"), hours),
    ...field(t("Preferred accounts"), segs([["", t("Current routing")], ["soonest", t("Soonest reset first")]], order, (v) => { order = v; changed(); })));
  box.replaceChildren(toggle, details, unsaved,
    el("div", "hint expl", t("Accounts whose selected quota resets within this many hours are preferred. Others take over if they cannot answer. Current routing applies when none qualify.")),
    el("div", "hint expl", t("Priority applies to new turns with automatic affinity. Tool calls in a turn and explicit Session or Strict affinity keep their account.")));
  draw();
};

window.resetPriorityValid = function (ed) {
  const box = ed.querySelector(".reset-priority-hours");
  if (!box || box.closest(".reset-priority-options").hidden) return true;
  const value = Number(box.value);
  if (box.value.trim() && Number.isFinite(value) && value > 0 && value <= 8760) return true;
  box.focus(NO_SCROLL);
  editorError(t("Reset priority hours: enter a number above 0 up to 8760"), "warn");
  return false;
};
