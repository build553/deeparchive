// Hand-written to mirror the Go API's actual JSON response shapes (see
// backend/internal/{identity,devices,backups,organizations,usage}/*.go).
// backend/api/openapi.yaml is not yet kept in sync with every route (a
// pre-existing gap — see docs/implementation-status.md), so it isn't
// generated from that spec. If a field here and the Go struct it mirrors
// ever disagree, the Go struct is right and this file is stale.

export interface Me {
  id: string;
  email: string;
  display_name: string;
  email_verified: boolean;
  mfa_enabled: boolean;
  status: "active" | "suspended" | "deactivated";
  platform_role: "none" | "operator" | "admin";
}

export interface Session {
  access_token: string;
  access_expires_at: string;
  refresh_token: string;
  refresh_expires_at: string;
}

export interface Device {
  id: string;
  user_id: string;
  organization_id?: string;
  name: string;
  os: string;
  client_version: string;
  status: "pending" | "active" | "revoked";
  registered_at: string;
  last_seen_at?: string;
}

export interface Snapshot {
  id: string;
  device_id: string;
  user_id: string;
  organization_id?: string;
  // Present but not used client-side in this app: restoring requires the
  // vault passphrase and client-side AES-256-GCM/Argon2id decryption,
  // which this dashboard does not implement — see docs/backup-protocol.md
  // and docs/encryption.md. Restore stays a deepctl operation; this UI
  // only shows metadata and the command to run.
  manifest_nonce: string;
  manifest_ciphertext: string;
  total_size: number;
  chunk_count: number;
  created_at: string;
}

export interface ChunkLocation {
  hash: string;
  download_url: string;
}

export interface SnapshotDetail extends Snapshot {
  chunks: ChunkLocation[];
}

export interface SessionSummary {
  id: string;
  user_agent: string;
  ip_address: string;
  created_at: string;
  last_used_at: string;
  expires_at: string;
}

export interface Organization {
  id: string;
  name: string;
  slug: string;
  owner_id: string;
  created_at: string;
}

export interface Member {
  user_id: string;
  email: string;
  display_name: string;
  role: "owner" | "admin" | "member";
  created_at: string;
}

export interface ScopeUsage {
  used_bytes: number;
  quota_bytes: number;
}

export interface OrganizationUsage extends ScopeUsage {
  organization_id: string;
  name: string;
}

export interface UsageReport {
  personal: ScopeUsage;
  organizations: OrganizationUsage[];
}

export interface CapturedEmail {
  to: string;
  subject: string;
  body: string;
  html?: string;
  sent_at: string;
}

// --- Admin panel (super-admin only — see backend/internal/admin) -------

export interface AdminUser {
  id: string;
  email: string;
  display_name: string;
  status: "active" | "suspended" | "deactivated";
  platform_role: "none" | "operator" | "admin";
  storage_quota_bytes: number;
  retention_keep_snapshots: number;
  created_at: string;
}

export interface AdminOrganization {
  id: string;
  name: string;
  slug: string;
  owner_id: string;
  storage_quota_bytes: number;
  retention_keep_snapshots: number;
  created_at: string;
}

export interface AdminStats {
  total_users: number;
  total_organizations: number;
  total_storage_bytes: number;
}

export interface AuditEvent {
  id: string;
  organization_id?: string;
  actor_user_id?: string;
  event_type: string;
  target_type?: string;
  target_id?: string;
  metadata: Record<string, unknown>;
  ip_address?: string;
  created_at: string;
}

export interface ApiError {
  error: {
    code: string;
    message: string;
  };
  [key: string]: unknown;
}

export interface AdminOTPChallenge {
  challenge_id: string;
  expires_at: string;
}
