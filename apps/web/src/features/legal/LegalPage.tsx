import { useEffect, type ReactNode } from "react";
import { Link, useLocation } from "react-router-dom";
import BrandMark from "../../components/BrandMark";
import ThemeToggle from "../../components/ThemeToggle";
import LegalFooter from "../../components/LegalFooter";
import { LEGAL_UPDATED } from "../../lib/legal";

// Public, readable-without-an-account shell shared by Terms and Privacy.
export default function LegalPage({ title, children }: { title: string; children: ReactNode }) {
  const { hash } = useLocation();

  useEffect(() => {
    document.title = `${title} · DeepArchive`;
    if (hash) document.getElementById(hash.slice(1))?.scrollIntoView();
    else window.scrollTo(0, 0);
  }, [title, hash]);

  return (
    <div className="legal-page">
      <header className="legal-header">
        <Link to="/" className="app-title">
          <BrandMark />
          DeepArchive
        </Link>
        <ThemeToggle />
      </header>
      <main className="legal-body">
        <h1>{title}</h1>
        <p className="hint">Last updated {LEGAL_UPDATED}</p>
        {children}
      </main>
      <LegalFooter />
    </div>
  );
}
