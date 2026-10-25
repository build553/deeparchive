export type Theme = "light" | "dark" | "system";
type Resolved = "light" | "dark";

const STORAGE_KEY = "deeparchive.theme";

function systemPrefersLight(): boolean {
  return window.matchMedia("(prefers-color-scheme: light)").matches;
}

function resolve(theme: Theme): Resolved {
  return theme === "system" ? (systemPrefersLight() ? "light" : "dark") : theme;
}

export function getStoredTheme(): Theme {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === "light" || stored === "dark") return stored;
  } catch {
    // localStorage unavailable (private browsing, blocked) — fall back to system.
  }
  return "system";
}

// Always sets an explicit resolved value (never removes the attribute) so
// CSS only needs one override block (:root[data-theme="light"]) instead
// of duplicating it under a prefers-color-scheme media query too.
export function applyTheme(theme: Theme) {
  document.documentElement.dataset.theme = resolve(theme);
}

export function setTheme(theme: Theme) {
  applyTheme(theme);
  try {
    if (theme === "system") {
      localStorage.removeItem(STORAGE_KEY);
    } else {
      localStorage.setItem(STORAGE_KEY, theme);
    }
  } catch {
    // Best effort — the theme still applies for this page view.
  }
}

let listening = false;

// Keeps the resolved theme in sync with the OS while the user's choice is
// "system" — e.g. macOS switching to Dark Mode at sunset mid-session.
// Safe to call more than once; only one listener is ever attached.
export function watchSystemTheme(getCurrentChoice: () => Theme) {
  if (listening) return;
  listening = true;
  window.matchMedia("(prefers-color-scheme: light)").addEventListener("change", () => {
    if (getCurrentChoice() === "system") applyTheme("system");
  });
}
