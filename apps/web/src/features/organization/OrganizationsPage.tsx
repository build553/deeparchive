import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { api, ApiRequestError } from "../../lib/apiClient";
import { EmptyState, LoadingState } from "../../components/Feedback";
import { formatDateTime } from "../../lib/format";
import type { Organization } from "../../lib/types";

export default function OrganizationsPage() {
  const [orgs, setOrgs] = useState<Organization[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [creating, setCreating] = useState(false);

  // The invitation email links here with ?token=…; pre-fill so accepting is one click.
  const [searchParams] = useSearchParams();
  const [invitationToken, setInvitationToken] = useState(searchParams.get("token") ?? "");
  const [acceptMessage, setAcceptMessage] = useState<string | null>(null);
  const [accepting, setAccepting] = useState(false);

  const load = useCallback(async () => {
    try {
      const resp = await api.listOrganizations();
      setOrgs(resp.organizations);
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to load organizations.");
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  async function onCreate(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setCreating(true);
    try {
      await api.createOrganization(name);
      setName("");
      await load();
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to create organization.");
    } finally {
      setCreating(false);
    }
  }

  async function onAccept(e: FormEvent) {
    e.preventDefault();
    setAcceptMessage(null);
    setAccepting(true);
    try {
      const org = await api.acceptInvitation(invitationToken);
      setAcceptMessage(`Joined ${org.name}.`);
      setInvitationToken("");
      await load();
    } catch (err) {
      setAcceptMessage(err instanceof ApiRequestError ? err.message : "Failed to accept invitation.");
    } finally {
      setAccepting(false);
    }
  }

  if (error && !orgs) {
    return <p className="form-error">{error}</p>;
  }

  return (
    <div>
      <header className="page-header">
        <h1>Organizations</h1>
      </header>
      {error && <p className="form-error">{error}</p>}
      {!orgs ? (
        <LoadingState label="Loading organizations…" />
      ) : orgs.length === 0 ? (
        <EmptyState title="You're not a member of any organization yet.">
          <p>Create one below, or accept an invitation someone sent you.</p>
        </EmptyState>
      ) : (
        <div className="card">
          <table className="data-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Created</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {orgs.map((o) => (
                <tr key={o.id}>
                  <td>
                    <Link to={`/organizations/${o.id}`}>{o.name}</Link>
                  </td>
                  <td>{formatDateTime(o.created_at)}</td>
                  <td></td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="card">
        <h3>Create organization</h3>
        <form className="inline-form" onSubmit={onCreate}>
          <input
            type="text"
            placeholder="Organization name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
          />
          <button type="submit" disabled={creating}>
            {creating ? "Creating…" : "Create"}
          </button>
        </form>
      </div>

      <div className="card">
        <h3>Accept an invitation</h3>
        <p className="hint">Paste the token from an invitation email.</p>
        <form className="inline-form" onSubmit={onAccept}>
          <input
            type="text"
            placeholder="Invitation token"
            value={invitationToken}
            onChange={(e) => setInvitationToken(e.target.value)}
            required
          />
          <button type="submit" disabled={accepting}>
            {accepting ? "Joining…" : "Accept"}
          </button>
        </form>
        {acceptMessage && <p className="form-hint">{acceptMessage}</p>}
      </div>
    </div>
  );
}
