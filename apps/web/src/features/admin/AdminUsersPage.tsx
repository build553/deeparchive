import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { api, ApiRequestError } from "../../lib/apiClient";
import { EmptyState, LoadingState } from "../../components/Feedback";
import { formatDateTime } from "../../lib/format";
import type { AdminUser } from "../../lib/types";

const PAGE_SIZE = 25;

export default function AdminUsersPage() {
  const [users, setUsers] = useState<AdminUser[] | null>(null);
  const [total, setTotal] = useState(0);
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(0);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async (q: string, p: number) => {
    try {
      const resp = await api.adminListUsers(q, PAGE_SIZE, p * PAGE_SIZE);
      setUsers(resp.users);
      setTotal(resp.total);
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to load users.");
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
          placeholder="Search by email or name…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <button type="submit">Search</button>
      </form>
      {error && <p className="form-error">{error}</p>}
      {!users ? (
        <LoadingState label="Loading users…" />
      ) : users.length === 0 ? (
        <EmptyState title="No users match.">
          <p>Try a different search term.</p>
        </EmptyState>
      ) : (
        <div className="card">
          <table className="data-table">
            <thead>
              <tr>
                <th>Email</th>
                <th>Status</th>
                <th>Platform role</th>
                <th>Created</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => (
                <tr key={u.id}>
                  <td>
                    <Link to={`/admin/users/${u.id}`}>{u.email}</Link>
                  </td>
                  <td>
                    <span className={`status-badge status-${u.status}`}>{u.status}</span>
                  </td>
                  <td>{u.platform_role}</td>
                  <td>{formatDateTime(u.created_at)}</td>
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
