import { useCallback, useEffect, useState } from "react";
import { api, ApiRequestError } from "../../lib/apiClient";
import { EmptyState, LoadingState } from "../../components/Feedback";
import { formatDateTime } from "../../lib/format";
import type { Device } from "../../lib/types";

// Devices are enrolled by deepctl (it generates the Ed25519 keypair
// and signs the challenge/response — see docs/backup-protocol.md), not
// from a browser. This page manages devices that already exist; it has
// no "add device" button.
export default function DevicesPage() {
  const [devices, setDevices] = useState<Device[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      const resp = await api.listDevices();
      setDevices(resp.devices);
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "Failed to load devices.");
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  async function withBusy(id: string, fn: () => Promise<unknown>) {
    setBusyId(id);
    setError(null);
    try {
      await fn();
      await load();
    } catch (err) {
      setError(err instanceof ApiRequestError ? err.message : "That action failed.");
    } finally {
      setBusyId(null);
    }
  }

  if (error && !devices) {
    return <p className="form-error">{error}</p>;
  }
  if (!devices) {
    return <LoadingState label="Loading devices…" />;
  }

  return (
    <div>
      <header className="page-header">
        <div>
          <h1>Devices</h1>
          <p className="hint">
            Enroll a new device from the command line: <code>deepctl init ...</code> (after <code>deepctl login</code>). See
            docs/backup-protocol.md.
          </p>
        </div>
      </header>
      {error && <p className="form-error">{error}</p>}
      {devices.length === 0 ? (
        <EmptyState title="No devices enrolled yet.">
          <p>Enroll one from the command line above, then approve it here.</p>
        </EmptyState>
      ) : (
        <div className="card">
          <table className="data-table">
            <thead>
              <tr>
                <th>Name</th>
                <th>OS</th>
                <th>Status</th>
                <th>Registered</th>
                <th>Last seen</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              {devices.map((d) => (
                <tr key={d.id}>
                  <td>{d.name}</td>
                  <td>{d.os}</td>
                  <td>
                    <span className={`status-badge status-${d.status}`}>{d.status}</span>
                  </td>
                  <td>{formatDateTime(d.registered_at)}</td>
                  <td>{d.last_seen_at ? formatDateTime(d.last_seen_at) : "—"}</td>
                  <td className="row-actions">
                    {d.status === "pending" && (
                      <button type="button" disabled={busyId === d.id} onClick={() => withBusy(d.id, () => api.approveDevice(d.id))}>
                        Approve
                      </button>
                    )}
                    {d.status !== "revoked" && (
                      <button
                        type="button"
                        className="danger"
                        disabled={busyId === d.id}
                        onClick={() => {
                          if (confirm(`Revoke "${d.name}"? It will no longer be able to back up or heartbeat.`)) {
                            withBusy(d.id, () => api.revokeDevice(d.id));
                          }
                        }}
                      >
                        Revoke
                      </button>
                    )}
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
