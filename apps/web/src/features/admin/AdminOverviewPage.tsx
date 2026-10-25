import { useEffect, useState } from "react";
import { api, ApiRequestError } from "../../lib/apiClient";
import { LoadingState } from "../../components/Feedback";
import { formatBytes } from "../../lib/format";
import type { AdminStats } from "../../lib/types";

export default function AdminOverviewPage() {
  const [stats, setStats] = useState<AdminStats | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    (async () => {
      try {
        setStats(await api.adminStats());
      } catch (err) {
        setError(err instanceof ApiRequestError ? err.message : "Failed to load stats.");
      }
    })();
  }, []);

  if (error) return <p className="form-error">{error}</p>;
  if (!stats) return <LoadingState label="Loading stats…" />;

  return (
    <div className="admin-stats">
      <div className="admin-stat-card">
        <span className="admin-stat-value">{stats.total_users}</span>
        <span className="admin-stat-label">Users</span>
      </div>
      <div className="admin-stat-card">
        <span className="admin-stat-value">{stats.total_organizations}</span>
        <span className="admin-stat-label">Organizations</span>
      </div>
      <div className="admin-stat-card">
        <span className="admin-stat-value">{formatBytes(stats.total_storage_bytes)}</span>
        <span className="admin-stat-label">Storage used (platform-wide)</span>
      </div>
    </div>
  );
}
