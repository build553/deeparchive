import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useNavigate, useParams, Link } from "react-router-dom";
import { api, ApiRequestError } from "../../lib/apiClient";
import { LoadingState } from "../../components/Feedback";
import { formatDateTime } from "../../lib/format";
import type { Member, Organization } from "../../lib/types";

export default function OrganizationDetailPage() {
  const { organizationId } = useParams<{ organizationId: string }>();
  const navigate = useNavigate();
  const [org, setOrg] = useState<Organization | null>(null);
  const [role, setRole] = useState<string | null>(null);
  const [members, setMembers] = useState<Member[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const [name, setName] = useState("");
  const [inviteEmail, setInviteEmail] = useState("");
  const [inviteRole, setInviteRole] = useState<"admin" | "member">("member");
  const [inviteMessage, setInviteMessage] = useState<string | null>(null);

  const load = useCallback(async () => {
    if (!organizationId) return;
    try {
      const [orgResp, membersResp] = await Promise.all([
        api.getOrganization(organizationId),
        api.listMembers(organizationId),
      ]);
      setOrg(orgResp.organization);
      setRole(orgResp.your_role);
      setName(orgResp.organization.name);
      setMembers(membersResp.members);
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to load organization.");
    }
  }, [organizationId]);

  useEffect(() => {
    load();
  }, [load]);

  const canManage = role === "owner" || role === "admin";

  async function onRename(e: FormEvent) {
    e.preventDefault();
    if (!organizationId) return;
    setError(null);
    setBusy(true);
    try {
      await api.renameOrganization(organizationId, name);
      await load();
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to rename organization.");
    } finally {
      setBusy(false);
    }
  }

  async function onInvite(e: FormEvent) {
    e.preventDefault();
    if (!organizationId) return;
    setInviteMessage(null);
    setBusy(true);
    try {
      await api.inviteMember(organizationId, inviteEmail, inviteRole);
      setInviteMessage(`Invitation sent to ${inviteEmail}.`);
      setInviteEmail("");
    } catch (err) {
      setInviteMessage(err instanceof ApiRequestError ? err.message : "Failed to send invitation.");
    } finally {
      setBusy(false);
    }
  }

  async function onRoleChange(userId: string, newRole: "admin" | "member") {
    if (!organizationId) return;
    setError(null);
    setBusy(true);
    try {
      await api.updateMemberRole(organizationId, userId, newRole);
      await load();
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to update role.");
    } finally {
      setBusy(false);
    }
  }

  async function onRemove(userId: string) {
    if (!organizationId) return;
    if (!confirm("Remove this member?")) return;
    setError(null);
    setBusy(true);
    try {
      await api.removeMember(organizationId, userId);
      await load();
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to remove member.");
    } finally {
      setBusy(false);
    }
  }

  async function onLeave() {
    if (!organizationId) return;
    if (!confirm("Leave this organization?")) return;
    setError(null);
    setBusy(true);
    try {
      await api.leaveOrganization(organizationId);
      navigate("/organizations", { replace: true });
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to leave organization.");
      setBusy(false);
    }
  }

  if (error && !org) {
    return <p className="form-error">{error}</p>;
  }
  if (!org || !members) {
    return <LoadingState label="Loading organization…" />;
  }

  return (
    <div>
      <p>
        <Link to="/organizations">&larr; Back to organizations</Link>
      </p>
      <header className="page-header">
        <div>
          <h1>{org.name}</h1>
          <p className="hint">
            Your role: <strong>{role}</strong>
          </p>
        </div>
      </header>
      {error && <p className="form-error">{error}</p>}

      {canManage && (
        <div className="card">
          <h3>Rename</h3>
          <form className="inline-form" onSubmit={onRename}>
            <input type="text" value={name} onChange={(e) => setName(e.target.value)} required />
            <button type="submit" disabled={busy}>
              Rename
            </button>
          </form>
        </div>
      )}

      <div className="card">
        <h3>Members</h3>
        <table className="data-table">
          <thead>
            <tr>
              <th>Name</th>
              <th>Email</th>
              <th>Role</th>
              <th>Joined</th>
              {canManage && <th></th>}
            </tr>
          </thead>
          <tbody>
            {members.map((m) => (
              <tr key={m.user_id}>
                <td>{m.display_name}</td>
                <td>{m.email}</td>
                <td>
                  {canManage && m.role !== "owner" ? (
                    <select
                      value={m.role}
                      disabled={busy}
                      onChange={(e) => onRoleChange(m.user_id, e.target.value as "admin" | "member")}
                    >
                      <option value="admin">admin</option>
                      <option value="member">member</option>
                    </select>
                  ) : (
                    m.role
                  )}
                </td>
                <td>{formatDateTime(m.created_at)}</td>
                {canManage && (
                  <td className="row-actions">
                    {m.role !== "owner" && (
                      <button type="button" className="danger" disabled={busy} onClick={() => onRemove(m.user_id)}>
                        Remove
                      </button>
                    )}
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {canManage && (
        <div className="card">
          <h3>Invite a member</h3>
          <form className="inline-form" onSubmit={onInvite}>
            <input
              type="email"
              placeholder="Email"
              value={inviteEmail}
              onChange={(e) => setInviteEmail(e.target.value)}
              required
            />
            <select value={inviteRole} onChange={(e) => setInviteRole(e.target.value as "admin" | "member")}>
              <option value="member">member</option>
              <option value="admin">admin</option>
            </select>
            <button type="submit" disabled={busy}>
              Invite
            </button>
          </form>
          {inviteMessage && <p className="form-hint">{inviteMessage}</p>}
        </div>
      )}

      <div className="card">
        <h3 className="danger-heading">Leave organization</h3>
        <button type="button" className="danger" disabled={busy} onClick={onLeave}>
          Leave
        </button>
      </div>
    </div>
  );
}
