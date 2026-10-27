function menuFor(trigger) {
  const parent = trigger.closest(".dropdown");
  return parent ? parent.querySelector(".menu") : null;
}

export function closeAllMenus(except) {
  for (const trigger of document.querySelectorAll("[data-menu-trigger]")) {
    const menu = menuFor(trigger);
    if (!menu || menu === except) continue;
    menu.hidden = true;
    trigger.setAttribute("aria-expanded", "false");
  }
}

function toggleMenu(trigger) {
  const menu = menuFor(trigger);
  if (!menu) return;
  const willOpen = menu.hidden;
  closeAllMenus(menu);
  menu.hidden = !willOpen;
  trigger.setAttribute("aria-expanded", willOpen ? "true" : "false");
}

export function initMenus() {
  document.addEventListener("click", (event) => {
    const trigger = event.target.closest("[data-menu-trigger]");
    if (trigger) {
      event.preventDefault();
      toggleMenu(trigger);
      return;
    }
    if (event.target.closest(".menu-item")) {
      closeAllMenus();
      return;
    }
    if (!event.target.closest(".menu")) {
      closeAllMenus();
    }
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape") closeAllMenus();
  });
}
