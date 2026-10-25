import { useCallback, useEffect, useState } from "react";
import { api, ApiRequestError } from "../../lib/apiClient";
import { EmptyState, LoadingState } from "../../components/Feedback";
import { formatDateTime } from "../../lib/format";
import type { AuditEvent } from "../../lib/types";

const PAGE_SIZE = 50;

export default function AdminActivityPage() {
  const [events, setEvents] = useState<AuditEvent[] | null>(null);
  const [page, setPage] = useState(0);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async (p: number) => {
    try {
      const resp = await api.adminListAudit(PAGE_SIZE, p * PAGE_SIZE);
      setEvents(resp.events);
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to load activity.");
    }
  }, []);

  useEffect(() => {
    load(page);
  }, [load, page]);

  if (error) return <p className="form-error">{error}</p>;
  if (!events) return <LoadingState label="Loading activity…" />;

  return (
    <div>
      <p className="hint">The most recent platform-wide audit events — every admin, auth, and org mutation writes one.</p>
      {events.length === 0 ? (
        <EmptyState title="No activity on this page." />
      ) : (
        <div className="card">
          <table className="data-table">
            <thead>
              <tr>
                <th>When</th>
                <th>Event</th>
                <th>Target</th>
                <th>Metadata</th>
              </tr>
            </thead>
            <tbody>
              {events.map((e) => (
                <tr key={e.id}>
                  <td>{formatDateTime(e.created_at)}</td>
                  <td>{e.event_type}</td>
                  <td>
                    {e.target_type ? `${e.target_type} ${e.target_id?.slice(0, 8)}` : <span className="hint">—</span>}
                  </td>
                  <td>
                    <code className="code-block">{JSON.stringify(e.metadata)}</code>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      <div className="admin-pager">
        <button type="button" className="btn-secondary" disabled={page === 0} onClick={() => setPage((p) => p - 1)}>
          Previous
        </button>
        <span className="hint">Page {page + 1}</span>
        <button
          type="button"
          className="btn-secondary"
          disabled={events.length < PAGE_SIZE}
          onClick={() => setPage((p) => p + 1)}
        >
          Next
        </button>
      </div>
    </div>
  );
}
