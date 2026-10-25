import { NavLink, Outlet } from "react-router-dom";

export default function AdminLayout() {
  return (
    <div>
      <header className="page-header">
        <h1>Admin panel</h1>
      </header>
      <nav className="admin-tabs" aria-label="Admin sections">
        <NavLink to="/admin" end>
          Overview
        </NavLink>
        <NavLink to="/admin/users">Users</NavLink>
        <NavLink to="/admin/organizations">Organizations</NavLink>
        <NavLink to="/admin/activity">Activity</NavLink>
      </nav>
      <div className="admin-content">
        <Outlet />
      </div>
    </div>
  );
}
