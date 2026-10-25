import { useEffect, useRef, useState } from "react";
import { getStoredTheme, setTheme, watchSystemTheme, type Theme } from "../lib/theme";

const OPTIONS: { value: Theme; label: string; icon: JSX.Element }[] = [
  {
    value: "light",
    label: "Light",
    icon: (
      <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
        <circle cx="8" cy="8" r="3.5" fill="currentColor" />
        <g stroke="currentColor" strokeWidth="1.4" strokeLinecap="round">
          <path d="M8 1v1.6M8 13.4V15M1 8h1.6M13.4 8H15M3.05 3.05l1.13 1.13M11.82 11.82l1.13 1.13M12.95 3.05l-1.13 1.13M4.18 11.82L3.05 12.95" />
        </g>
      </svg>
    ),
  },
  {
    value: "system",
    label: "Auto",
    icon: (
      <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
        <rect x="1.5" y="2.5" width="13" height="8.5" rx="1.5" fill="none" stroke="currentColor" strokeWidth="1.4" />
        <path d="M5.5 14h5M8 11v3" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      </svg>
    ),
  },
  {
    value: "dark",
    label: "Dark",
    icon: (
      <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true">
        <path
          d="M13.5 9.5A6 6 0 0 1 6.5 2.5a6 6 0 1 0 7 7Z"
          fill="currentColor"
        />
      </svg>
    ),
  },
];

export default function ThemeToggle() {
  const [theme, setThemeState] = useState<Theme>(() => getStoredTheme());
  const themeRef = useRef(theme);
  themeRef.current = theme;

  useEffect(() => {
    setTheme(theme);
  }, [theme]);

  useEffect(() => {
    watchSystemTheme(() => themeRef.current);
  }, []);

  return (
    <div className="theme-toggle" role="radiogroup" aria-label="Appearance">
      {OPTIONS.map((opt) => (
        <button
          key={opt.value}
          type="button"
          role="radio"
          aria-checked={theme === opt.value}
          aria-label={opt.label}
          title={opt.label}
          className={`theme-toggle-option${theme === opt.value ? " active" : ""}`}
          onClick={() => setThemeState(opt.value)}
        >
          {opt.icon}
        </button>
      ))}
    </div>
  );
}
