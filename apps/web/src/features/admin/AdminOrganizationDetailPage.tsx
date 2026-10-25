import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Link, useParams } from "react-router-dom";
import { api, ApiRequestError } from "../../lib/apiClient";
import { LoadingState } from "../../components/Feedback";
import { formatBytes, formatDateTime } from "../../lib/format";
import type { AdminOrganization, Member } from "../../lib/types";

export default function AdminOrganizationDetailPage() {
  const { orgId } = useParams<{ orgId: string }>();
  const [org, setOrg] = useState<AdminOrganization | null>(null);
  const [members, setMembers] = useState<Member[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  const [quota, setQuota] = useState("");
  const [retention, setRetention] = useState("");
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    if (!orgId) return;
    try {
      const resp = await api.adminGetOrganization(orgId);
      setOrg(resp.organization);
      setMembers(resp.members);
      setQuota(String(resp.organization.storage_quota_bytes));
      setRetention(String(resp.organization.retention_keep_snapshots));
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to load organization.");
    }
  }, [orgId]);

  useEffect(() => {
    load();
  }, [load]);

  async function onSaveQuota(e: FormEvent) {
    e.preventDefault();
    if (!orgId) return;
    setSaving(true);
    setMessage(null);
    setError(null);
    try {
      await api.adminSetOrgQuota(orgId, Number(quota));
      setMessage("Storage quota updated.");
      await load();
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to update quota.");
    } finally {
      setSaving(false);
    }
  }

  async function onSaveRetention(e: FormEvent) {
    e.preventDefault();
    if (!orgId) return;
    setSaving(true);
    setMessage(null);
    setError(null);
    try {
      await api.adminSetOrgRetention(orgId, Number(retention));
      setMessage("Retention limit updated.");
      await load();
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to update retention.");
    } finally {
      setSaving(false);
    }
  }

  if (error && !org) return <p className="form-error">{error}</p>;
  if (!org) return <LoadingState label="Loading organization…" />;

  return (
    <div>
      <p>
        <Link to="/admin/organizations">&larr; Back to organizations</Link>
      </p>
      <header className="page-header">
        <h2>{org.name}</h2>
      </header>

      <div className="card">
        <dl className="detail-list">
          <dt>Slug</dt>
          <dd>{org.slug}</dd>
          <dt>Created</dt>
          <dd>{formatDateTime(org.created_at)}</dd>
        </dl>
      </div>

      {message && <p className="form-success">{message}</p>}
      {error && <p className="form-error">{error}</p>}

      <div className="card">
        <h3>Members</h3>
        <table className="data-table">
          <thead>
            <tr>
              <th>Email</th>
              <th>Name</th>
              <th>Role</th>
              <th>Joined</th>
            </tr>
          </thead>
          <tbody>
            {members.map((m) => (
              <tr key={m.user_id}>
                <td>
                  <Link to={`/admin/users/${m.user_id}`}>{m.email}</Link>
                </td>
                <td>{m.display_name}</td>
                <td>{m.role}</td>
                <td>{formatDateTime(m.created_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
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
    </div>
  );
}
