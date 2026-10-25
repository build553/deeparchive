import { Navigate, Outlet, useLocation } from "react-router-dom";
import { useAuth } from "../features/auth/AuthContext";
import { LoadingState } from "../components/Feedback";

export default function ProtectedRoute() {
  const { me, loading } = useAuth();
  const location = useLocation();

  if (loading) {
    return (
      <div className="app-loading">
        <LoadingState />
      </div>
    );
  }
  if (!me) {
    // Remember where they were headed (e.g. /device?code=... from a CLI
    // login link) so sign-in returns there instead of dropping them on /.
    return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />;
  }
  return <Outlet />;
}
