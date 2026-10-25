import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { useAuth } from "../features/auth/AuthContext";
import ThemeToggle from "./ThemeToggle";
import BrandMark from "./BrandMark";
import LegalFooter from "./LegalFooter";

export default function Layout() {
  const { me, logout } = useAuth();
  const navigate = useNavigate();

  async function onLogout() {
    await logout();
    navigate("/login", { replace: true });
  }

  return (
    <div className="app-shell">
      <a className="skip-link" href="#main-content">
        Skip to content
      </a>
      <header className="app-header">
        <span className="app-title">
          <BrandMark />
          DeepArchive
        </span>
        <nav aria-label="Main">
          <NavLink to="/" end>
            Devices
          </NavLink>
          <NavLink to="/backups">Backups</NavLink>
          <NavLink to="/organizations">Organizations</NavLink>
          <NavLink to="/storage">Storage</NavLink>
          <NavLink to="/account">Account</NavLink>
          {me?.platform_role === "admin" && <NavLink to="/admin">Admin</NavLink>}
        </nav>
        <div className="app-header-user">
          <ThemeToggle />
          <span>{me?.display_name}</span>
          <button type="button" className="btn-secondary" onClick={onLogout}>
            Log out
          </button>
        </div>
      </header>
      <main className="app-main" id="main-content">
        <Outlet />
      </main>
      <LegalFooter />
    </div>
  );
}
