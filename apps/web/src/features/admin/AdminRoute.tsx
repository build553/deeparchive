import { Navigate, Outlet } from "react-router-dom";
import { useAuth } from "../auth/AuthContext";

// Mounted inside ProtectedRoute, so `me` is already known non-null by the
// time this renders — this only adds the platform_role check. Re-checked
// on every navigation into /admin/*, same spirit as the backend's
// RequireAdmin re-checking on every request rather than trusting a
// cached flag.
export default function AdminRoute() {
  const { me } = useAuth();

  if (me?.platform_role !== "admin") {
    return <Navigate to="/" replace />;
  }
  return <Outlet />;
}
