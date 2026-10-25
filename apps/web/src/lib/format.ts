export function formatBytes(bytes: number): string {
  if (bytes === 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const exponent = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1);
  const value = bytes / 1024 ** exponent;
  return `${exponent === 0 ? value : value.toFixed(1)} ${units[exponent]}`;
}

export function formatDateTime(iso: string): string {
  return new Date(iso).toLocaleString();
}

// A raw User-Agent string ("Mozilla/5.0 (Macintosh; Intel Mac OS X ...)
// AppleWebKit/... Chrome/131.0.0.0 Safari/537.36") is real data but not
// something a person scans a session list for — it also has no natural
// break points, which is what was forcing "Revoke" to wrap character by
// character in a narrow table cell. Reduce it to what someone actually
// checks a sessions list for ("is this me, on this machine"), with the
// raw string as a title-attribute fallback for anything unrecognized.
export function summarizeUserAgent(ua: string): string {
  if (!ua) return "Unknown device";

  let os = "";
  if (/iPhone|iPad|iPod/.test(ua)) os = "iOS";
  else if (/Android/.test(ua)) os = "Android";
  else if (/Mac OS X/.test(ua)) os = "macOS";
  else if (/Windows/.test(ua)) os = "Windows";
  else if (/Linux/.test(ua)) os = "Linux";

  let browser = "";
  if (/Edg\//.test(ua)) browser = "Edge";
  else if (/OPR\/|Opera/.test(ua)) browser = "Opera";
  else if (/Chrome\//.test(ua)) browser = "Chrome";
  else if (/CriOS\//.test(ua)) browser = "Chrome";
  else if (/Firefox\//.test(ua)) browser = "Firefox";
  else if (/Safari\//.test(ua) && /Version\//.test(ua)) browser = "Safari";

  if (browser && os) return `${browser} on ${os}`;
  if (browser) return browser;
  if (os) return os;

  // Not a browser UA (e.g. "curl/8.7.1", "backup-cli/0.3.0") — short
  // enough on its own, no summarizing needed.
  return ua.length <= 40 ? ua : `${ua.slice(0, 37)}…`;
}
