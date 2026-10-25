import type { ReactNode } from "react";

// Replaces bare "Loading…" text across pages with something that
// appears immediately and reads as motion, not a stalled page
// (loading.md › "Something appears immediately while loading").
export function LoadingState({ label = "Loading…" }: { label?: string }) {
  return (
    <div className="loading-state" role="status">
      <span className="spinner" aria-hidden="true" />
      <span>{label}</span>
    </div>
  );
}

// Replaces bare "No X yet." text with a labeled, bordered block that
// can optionally point at the next action (writing.md › "Empty screens
// invite the next action").
export function EmptyState({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="empty-state">
      <p className="empty-state-title">{title}</p>
      {children}
    </div>
  );
}
