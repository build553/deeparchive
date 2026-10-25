// The one signature element the product is built around visually:
// three offset, rounded layers standing in for versioned, encrypted
// snapshots stacking up over time — DeepArchive's actual job. Flat
// accent blue at three opacities (not a gradient) so it carries the
// live --color-accent token with no asset request and adapts instantly
// when the theme switches.
export default function BrandMark() {
  return (
    <svg className="brand-mark" width="22" height="22" viewBox="0 0 22 22" aria-hidden="true">
      <rect x="2" y="7" width="14" height="14" rx="3.5" fill="var(--color-accent)" opacity="0.35" />
      <rect x="5" y="4" width="14" height="14" rx="3.5" fill="var(--color-accent)" opacity="0.65" />
      <rect x="8" y="1" width="14" height="14" rx="3.5" fill="var(--color-accent)" />
    </svg>
  );
}
