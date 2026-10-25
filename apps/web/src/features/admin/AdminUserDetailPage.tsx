import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Link, useParams } from "react-router-dom";
import { api, ApiRequestError } from "../../lib/apiClient";
import { LoadingState } from "../../components/Feedback";
import { formatBytes, formatDateTime } from "../../lib/format";
import type { AdminUser, Organization } from "../../lib/types";
import { useAuth } from "../auth/AuthContext";

export default function AdminUserDetailPage() {
  const { userId } = useParams<{ userId: string }>();
  const { me } = useAuth();
  const [user, setUser] = useState<AdminUser | null>(null);
  const [orgs, setOrgs] = useState<Organization[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  const [quota, setQuota] = useState("");
  const [retention, setRetention] = useState("");
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    if (!userId) return;
    try {
      const resp = await api.adminGetUser(userId);
      setUser(resp.user);
      setOrgs(resp.organizations);
      setQuota(String(resp.user.storage_quota_bytes));
      setRetention(String(resp.user.retention_keep_snapshots));
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to load user.");
    }
  }, [userId]);

  useEffect(() => {
    load();
  }, [load]);

  async function run(action: () => Promise<unknown>, successMessage: string) {
    if (!userId) return;
    setSaving(true);
    setMessage(null);
    setError(null);
    try {
      await action();
      setMessage(successMessage);
      await load();
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Action failed.");
    } finally {
      setSaving(false);
    }
  }

  async function onSaveQuota(e: FormEvent) {
    e.preventDefault();
    await run(() => api.adminSetUserQuota(userId!, Number(quota)), "Storage quota updated.");
  }

  async function onSaveRetention(e: FormEvent) {
    e.preventDefault();
    await run(() => api.adminSetUserRetention(userId!, Number(retention)), "Retention limit updated.");
  }

  async function onSetStatus(status: "active" | "suspended" | "deactivated") {
    if (!confirm(`Set this user's status to "${status}"?`)) return;
    await run(() => api.adminSetUserStatus(userId!, status), `Status set to ${status}.`);
  }

  async function onSetPlatformRole(role: "none" | "operator" | "admin") {
    if (!confirm(`Set this user's platform role to "${role}"?`)) return;
    await run(() => api.adminSetUserPlatformRole(userId!, role), `Platform role set to ${role}.`);
  }

  if (error && !user) return <p className="form-error">{error}</p>;
  if (!user) return <LoadingState label="Loading user…" />;

  const isSelf = me?.id === user.id;

  return (
    <div>
      <p>
        <Link to="/admin/users">&larr; Back to users</Link>
      </p>
      <header className="page-header">
        <h2>{user.email}</h2>
      </header>

      <div className="card">
        <dl className="detail-list">
          <dt>Display name</dt>
          <dd>{user.display_name}</dd>
          <dt>Status</dt>
          <dd>
            <span className={`status-badge status-${user.status}`}>{user.status}</span>
          </dd>
          <dt>Platform role</dt>
          <dd>{user.platform_role}</dd>
          <dt>Created</dt>
          <dd>{formatDateTime(user.created_at)}</dd>
        </dl>
      </div>

      {message && <p className="form-success">{message}</p>}
      {error && <p className="form-error">{error}</p>}

      <div className="card">
        <h3>Organizations</h3>
        {orgs.length === 0 ? (
          <p className="hint">Not a member of any organization.</p>
        ) : (
          <ul>
            {orgs.map((o) => (
              <li key={o.id}>
                <Link to={`/admin/organizations/${o.id}`}>{o.name}</Link>
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="card">
        <h3>Storage quota</h3>
        <form className="inline-form" onSubmit={onSaveQuota}>
          <input type="number" min={0} value={quota} onChange={(e) => setQuota(e.target.value)} />
          <span className="hint">({formatBytes(Number(quota) || 0)})</span>
          <button type="submit" disabled={saving}>
            Save
          </button>
        </form>

        <h3>Retention (snapshots kept per device)</h3>
        <form className="inline-form" onSubmit={onSaveRetention}>
          <input type="number" min={1} value={retention} onChange={(e) => setRetention(e.target.value)} />
          <button type="submit" disabled={saving}>
            Save
          </button>
        </form>
      </div>

      <div className="card">
        <h3>Account status</h3>
        <div className="row-actions">
          <button type="button" disabled={saving || user.status === "active"} onClick={() => onSetStatus("active")}>
            Activate
          </button>
          <button
            type="button"
            disabled={saving || user.status === "suspended"}
            onClick={() => onSetStatus("suspended")}
          >
            Suspend
          </button>
          <button
            type="button"
            className="danger"
            disabled={saving || user.status === "deactivated"}
            onClick={() => onSetStatus("deactivated")}
          >
            Deactivate
          </button>
        </div>
      </div>

      <div className="card">
        <h3 className="danger-heading">Platform role</h3>
        <p className="hint">
          Admins can manage every account on the platform through this panel. You cannot change your own role — ask
          another admin, or use deepctl directly.
        </p>
        <div className="row-actions">
          <button
            type="button"
            disabled={saving || isSelf || user.platform_role === "none"}
            onClick={() => onSetPlatformRole("none")}
          >
            None
          </button>
          <button
            type="button"
            disabled={saving || isSelf || user.platform_role === "operator"}
            onClick={() => onSetPlatformRole("operator")}
          >
            Operator
          </button>
          <button
            type="button"
            className="danger"
            disabled={saving || isSelf || user.platform_role === "admin"}
            onClick={() => onSetPlatformRole("admin")}
          >
            Admin
          </button>
        </div>
        {isSelf && <p className="hint">This is your own account — self role changes are blocked.</p>}
      </div>
    </div>
  );
}
