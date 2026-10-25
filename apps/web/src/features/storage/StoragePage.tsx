import { useEffect, useState } from "react";
import { api, ApiRequestError } from "../../lib/apiClient";
import { LoadingState } from "../../components/Feedback";
import { formatBytes } from "../../lib/format";
import type { ScopeUsage, UsageReport } from "../../lib/types";

export default function StoragePage() {
  const [report, setReport] = useState<UsageReport | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .usage()
      .then(setReport)
      .catch((err) => setError(err instanceof ApiRequestError ? err.message : "Failed to load storage usage."));
  }, []);

  if (error) {
    return <p className="form-error">{error}</p>;
  }
  if (!report) {
    return <LoadingState label="Loading storage usage…" />;
  }

  return (
    <div>
      <header className="page-header">
        <h1>Storage</h1>
      </header>
      <div className="card">
        <UsageMeter label="Personal" usage={report.personal} />
        {report.organizations.map((o) => (
          <UsageMeter key={o.organization_id} label={o.name} usage={o} />
        ))}
      </div>
    </div>
  );
}

function UsageMeter({ label, usage }: { label: string; usage: ScopeUsage }) {
  const pct = usage.quota_bytes > 0 ? Math.min(100, (usage.used_bytes / usage.quota_bytes) * 100) : 0;
  const over = usage.used_bytes > usage.quota_bytes;
  return (
    <section className="usage-meter">
      <h3>{label}</h3>
      <div
        className="usage-bar-track"
        role="progressbar"
        aria-valuenow={Math.round(pct)}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-label={`${label} storage used`}
      >
        <div className={`usage-bar-fill${over ? " usage-bar-over" : ""}`} style={{ width: `${pct}%` }} />
      </div>
      <p className="hint">
        {formatBytes(usage.used_bytes)} of {formatBytes(usage.quota_bytes)} used ({pct.toFixed(1)}%)
      </p>
    </section>
  );
}
