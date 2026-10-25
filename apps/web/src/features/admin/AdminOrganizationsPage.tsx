import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { api, ApiRequestError } from "../../lib/apiClient";
import { EmptyState, LoadingState } from "../../components/Feedback";
import { formatDateTime } from "../../lib/format";
import type { AdminOrganization } from "../../lib/types";

const PAGE_SIZE = 25;

export default function AdminOrganizationsPage() {
  const [orgs, setOrgs] = useState<AdminOrganization[] | null>(null);
  const [total, setTotal] = useState(0);
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(0);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async (q: string, p: number) => {
    try {
      const resp = await api.adminListOrganizations(q, PAGE_SIZE, p * PAGE_SIZE);
      setOrgs(resp.organizations);
      setTotal(resp.total);
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to load organizations.");
    }
  }, []);

  useEffect(() => {
    load(query, page);
  }, [load, query, page]);

  function onSearch(e: FormEvent) {
    e.preventDefault();
    setPage(0);
  }

  const lastPage = Math.max(0, Math.ceil(total / PAGE_SIZE) - 1);

  return (
    <div>
      <form className="inline-form" onSubmit={onSearch}>
        <input
          type="text"
          placeholder="Search by name or slug…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <button type="submit">Search</button>
      </form>
      {error && <p className="form-error">{error}</p>}
      {!orgs ? (
        <LoadingState label="Loading organizations…" />
      ) : orgs.length === 0 ? (
        <EmptyState title="No organizations match.">
          <p>Try a different search term.</p>
        </EmptyState>
      ) : (
        <div className="card">
          <table className="data-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>Slug</th>
                <th>Created</th>
              </tr>
            </thead>
            <tbody>
              {orgs.map((o) => (
                <tr key={o.id}>
                  <td>
                    <Link to={`/admin/organizations/${o.id}`}>{o.name}</Link>
                  </td>
                  <td>{o.slug}</td>
                  <td>{formatDateTime(o.created_at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <div className="admin-pager">
            <button type="button" className="btn-secondary" disabled={page === 0} onClick={() => setPage((p) => p - 1)}>
              Previous
            </button>
            <span className="hint">
              Page {page + 1} of {lastPage + 1} ({total} total)
            </span>
            <button
              type="button"
              className="btn-secondary"
              disabled={page >= lastPage}
              onClick={() => setPage((p) => p + 1)}
            >
              Next
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
