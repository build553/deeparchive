import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, ApiRequestError } from "../../lib/apiClient";
import { EmptyState, LoadingState } from "../../components/Feedback";
import { formatBytes, formatDateTime } from "../../lib/format";
import type { Snapshot } from "../../lib/types";

export default function BackupsPage() {
  const [snapshots, setSnapshots] = useState<Snapshot[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const resp = await api.listSnapshots();
      setSnapshots(resp.snapshots);
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to load backups.");
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  async function onDelete(s: Snapshot) {
    if (!confirm(`Delete this snapshot from ${formatDateTime(s.created_at)}? This cannot be undone.`)) return;
    setBusyId(s.id);
    setError(null);
    try {
      await api.deleteSnapshot(s.id);
      await load();
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to delete snapshot.");
    } finally {
      setBusyId(null);
    }
  }

  if (error && !snapshots) {
    return <p className="form-error">{error}</p>;
  }
  if (!snapshots) {
    return <LoadingState label="Loading backups…" />;
  }

  return (
    <div>
      <header className="page-header">
        <div>
          <h1>Backups</h1>
          <p className="hint">
            Created by <code>deepctl backup</code> runs. Restoring a snapshot requires its vault passphrase and
            happens on the command line — this dashboard shows metadata only. See docs/backup-protocol.md.
          </p>
        </div>
      </header>
      {error && <p className="form-error">{error}</p>}
      {snapshots.length === 0 ? (
        <EmptyState title="No backups yet.">
          <p>Run <code>deepctl backup</code> from an enrolled device to create your first snapshot.</p>
        </EmptyState>
      ) : (
        <div className="card">
          <table className="data-table">
            <thead>
              <tr>
                <th>Created</th>
                <th>Size</th>
                <th>Chunks</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {snapshots.map((s) => (
                <tr key={s.id}>
                  <td>
                    <Link to={`/backups/${s.id}`}>{formatDateTime(s.created_at)}</Link>
                  </td>
                  <td>{formatBytes(s.total_size)}</td>
                  <td>{s.chunk_count}</td>
                  <td className="row-actions">
                    <button type="button" className="danger" disabled={busyId === s.id} onClick={() => onDelete(s)}>
                      Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
