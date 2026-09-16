// Tooltips for chart marks (hover and keyboard focus) and Ctrl/Cmd+Enter to
// run the SQL console. No dependencies.
(() => {
  const tip = document.createElement("div");
  tip.className = "tooltip";
  tip.hidden = true;
  document.addEventListener("DOMContentLoaded", () => document.body.append(tip));

  const show = (mark, x, y) => {
    const value = document.createElement("strong");
    value.textContent = mark.dataset.tipValue || "";
    const label = document.createElement("span");
    label.textContent = mark.dataset.tipLabel || "";
    tip.replaceChildren(value, label);
    tip.hidden = false;
    const pad = 14;
    const w = tip.offsetWidth, h = tip.offsetHeight;
    tip.style.left = Math.min(x + pad, window.innerWidth - w - 8) + "px";
    tip.style.top = Math.max(8, y - h - pad) + "px";
  };
  const hide = () => { tip.hidden = true; };
  const markOf = (el) => el instanceof Element ? el.closest("[data-tip-value]") : null;

  document.addEventListener("pointermove", (e) => {
    const mark = markOf(e.target);
    mark ? show(mark, e.clientX, e.clientY) : hide();
  });
  document.addEventListener("pointerleave", hide);
  document.addEventListener("focusin", (e) => {
    const mark = markOf(e.target);
    if (!mark) return;
    const r = mark.getBoundingClientRect();
    show(mark, r.left + r.width / 2, r.top);
  });
  document.addEventListener("focusout", hide);

  document.addEventListener("keydown", (e) => {
    if (e.key === "Enter" && (e.ctrlKey || e.metaKey) && e.target.matches("textarea[name=sql]")) {
      e.preventDefault();
      e.target.form.requestSubmit();
    }
  });
})();
