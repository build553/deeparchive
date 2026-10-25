import { useEffect, useState } from "react";
import { useParams, Link } from "react-router-dom";
import { api, ApiRequestError } from "../../lib/apiClient";
import { LoadingState } from "../../components/Feedback";
import { formatBytes, formatDateTime } from "../../lib/format";
import type { SnapshotDetail } from "../../lib/types";
import SnapshotBrowser from "./SnapshotBrowser";

export default function SnapshotDetailPage() {
  const { snapshotId } = useParams<{ snapshotId: string }>();
  const [snapshot, setSnapshot] = useState<SnapshotDetail | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!snapshotId) return;
    api
      .getSnapshot(snapshotId)
      .then(setSnapshot)
      .catch((err) => setError(err instanceof ApiRequestError ? err.message : "Failed to load snapshot."));
  }, [snapshotId]);

  if (error) {
    return <p className="form-error">{error}</p>;
  }
  if (!snapshot) {
    return <LoadingState label="Loading snapshot…" />;
  }

  const restoreCommand = `deepctl restore --snapshot ${snapshot.id} --to ./restored`;

  return (
    <div>
      <p>
        <Link to="/backups">&larr; Back to backups</Link>
      </p>
      <header className="page-header">
        <h1>Snapshot {formatDateTime(snapshot.created_at)}</h1>
      </header>
      <div className="card">
        <dl className="detail-list">
          <dt>Total size</dt>
          <dd>{formatBytes(snapshot.total_size)}</dd>
          <dt>Chunks</dt>
          <dd>{snapshot.chunk_count}</dd>
          <dt>Device</dt>
          <dd>{snapshot.device_id}</dd>
          <dt>Snapshot ID</dt>
          <dd>
            <code>{snapshot.id}</code>
          </dd>
        </dl>
      </div>

      <SnapshotBrowser snapshot={snapshot} />

      <div className="card">
        <h3>Restore everything</h3>
        <p className="hint">
          File names, directory structure, and content are encrypted end-to-end; the server never sees them. To restore
          the whole snapshot at once, run:
        </p>
        <pre className="code-block">{restoreCommand}</pre>
      </div>
    </div>
  );
}
