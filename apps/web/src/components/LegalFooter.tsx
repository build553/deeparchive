import { Link } from "react-router-dom";

export default function LegalFooter() {
  return (
    <footer className="legal-footer">
      <span>&copy; {new Date().getFullYear()} Build553</span>
      <Link to="/terms">Terms</Link>
      <Link to="/privacy">Privacy</Link>
    </footer>
  );
}
