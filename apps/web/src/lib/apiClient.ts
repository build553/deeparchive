import type { WrappedVaultKey } from "./vault";
import type {
  AdminOrganization,
  AdminStats,
  AdminUser,
  ApiError,
  AuditEvent,
  CapturedEmail,
  Device,
  Me,
  Member,
  Organization,
  Session,
  SessionSummary,
  Snapshot,
  SnapshotDetail,
  UsageReport,
} from "./types";

export const API_BASE_URL = import.meta.env.VITE_API_BASE_URL ?? "http://localhost:8080";
const BASE_URL = API_BASE_URL;

export class ApiRequestError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public details?: Record<string, unknown>,
  ) {
    super(message);
  }
}

// Set by AuthProvider, the single place that owns the current access
// token and knows how to refresh it. Keeping this at module scope (rather
// than threading it through every call site) is what lets request() retry
// a 401 with a freshly refreshed token transparently.
let accessToken: string | null = null;
let refreshFn: (() => Promise<void>) | null = null;
let refreshInFlight: Promise<void> | null = null;

export function setAccessToken(token: string | null) {
  accessToken = token;
}

export function setRefreshHandler(fn: (() => Promise<void>) | null) {
  refreshFn = fn;
}

interface RequestOptions {
  // false for routes that must not send (or wait to send) a bearer token:
  // register, login, refresh itself.
  auth?: boolean;
  retryOn401?: boolean;
}

async function request<T>(path: string, init: RequestInit = {}, opts: RequestOptions = {}): Promise<T> {
  const { auth = true, retryOn401 = true } = opts;

  const headers = new Headers(init.headers);
  headers.set("Content-Type", "application/json");
  if (auth && accessToken) {
    headers.set("Authorization", `Bearer ${accessToken}`);
  }

  const resp = await fetch(`${BASE_URL}${path}`, { ...init, headers });

  if (resp.status === 401 && auth && retryOn401 && refreshFn) {
    if (!refreshInFlight) {
      refreshInFlight = refreshFn().finally(() => {
        refreshInFlight = null;
      });
    }
    try {
      await refreshInFlight;
    } catch {
      throw new ApiRequestError(401, "session_expired", "your session has expired; please log in again");
    }
    return request<T>(path, init, { auth, retryOn401: false });
  }

  if (!resp.ok) {
    let body: ApiError | undefined;
    try {
      body = (await resp.json()) as ApiError;
    } catch {
      // no JSON body (e.g. a network-level error page) — fall through to the generic message below
    }
    throw new ApiRequestError(resp.status, body?.error.code ?? "unknown_error", body?.error.message ?? resp.statusText, body);
  }

  if (resp.status === 204) {
    return undefined as T;
  }
  return (await resp.json()) as T;
}

export const api = {
  register: (email: string, password: string, displayName: string, consent: { termsAccepted: boolean; ageConfirmed: boolean }) =>
    request<{ message: string }>(
      "/api/v1/auth/register",
      {
        method: "POST",
        body: JSON.stringify({
          email,
          password,
          display_name: displayName,
          terms_accepted: consent.termsAccepted,
          age_confirmed: consent.ageConfirmed,
        }),
      },
      { auth: false },
    ),

  verifyEmail: (token: string) =>
    request<{ message: string }>(
      "/api/v1/auth/verify-email",
      { method: "POST", body: JSON.stringify({ token }) },
      { auth: false },
    ),

  forgotPassword: (email: string) =>
    request<{ message: string }>(
      "/api/v1/auth/password/forgot",
      { method: "POST", body: JSON.stringify({ email }) },
      { auth: false },
    ),

  resendVerification: (email: string) =>
    request<{ message: string }>(
      "/api/v1/auth/resend-verification",
      { method: "POST", body: JSON.stringify({ email }) },
      { auth: false },
    ),

  resetPassword: (token: string, newPassword: string) =>
    request<{ message: string }>(
      "/api/v1/auth/password/reset",
      { method: "POST", body: JSON.stringify({ token, new_password: newPassword }) },
      { auth: false },
    ),

  login: (email: string, password: string, mfaCode?: string) =>
    request<Session>(
      "/api/v1/auth/login",
      { method: "POST", body: JSON.stringify({ email, password, mfa_code: mfaCode }) },
      { auth: false },
    ),

  verifyAdminOtp: (challengeId: string, code: string) =>
    request<Session>(
      "/api/v1/auth/login/otp/verify",
      { method: "POST", body: JSON.stringify({ challenge_id: challengeId, code }) },
      { auth: false },
    ),

  refresh: (refreshToken: string) =>
    request<Session>(
      "/api/v1/auth/refresh",
      { method: "POST", body: JSON.stringify({ refresh_token: refreshToken }) },
      { auth: false },
    ),

  logout: () => request<{ message: string }>("/api/v1/auth/logout", { method: "POST" }),

  me: () => request<Me>("/api/v1/me"),

  updateMe: (displayName: string) =>
    request<{ message: string }>("/api/v1/me", { method: "PATCH", body: JSON.stringify({ display_name: displayName }) }),

  deactivateMe: (password: string) =>
    request<{ message: string }>("/api/v1/me", { method: "DELETE", body: JSON.stringify({ password }) }),

  getVaultKey: (deviceId: string) =>
    request<{ wrapped_vault_key: WrappedVaultKey }>(`/api/v1/devices/${deviceId}/vault-key`),

  putVaultKey: (deviceId: string, wrapped: WrappedVaultKey) =>
    request<{ message: string }>(`/api/v1/devices/${deviceId}/vault-key`, {
      method: "PUT",
      body: JSON.stringify({ wrapped_vault_key: wrapped }),
    }),

  listDevices: () => request<{ devices: Device[] }>("/api/v1/devices"),

  approveDevice: (id: string) => request<{ message: string }>(`/api/v1/devices/${id}/approve`, { method: "POST" }),

  renameDevice: (id: string, name: string) =>
    request<{ message: string }>(`/api/v1/devices/${id}`, { method: "PATCH", body: JSON.stringify({ name }) }),

  revokeDevice: (id: string) => request<{ message: string }>(`/api/v1/devices/${id}`, { method: "DELETE" }),

  listSnapshots: () => request<{ snapshots: Snapshot[] }>("/api/v1/backups"),

  getSnapshot: (id: string) => request<SnapshotDetail>(`/api/v1/backups/${id}`),

  deleteSnapshot: (id: string) => request<{ message: string }>(`/api/v1/backups/${id}`, { method: "DELETE" }),

  changePassword: (currentPassword: string, newPassword: string) =>
    request<{ message: string }>("/api/v1/auth/password/change", {
      method: "POST",
      body: JSON.stringify({ current_password: currentPassword, new_password: newPassword }),
    }),

  enrollMfa: () => request<{ secret: string; uri: string }>("/api/v1/auth/mfa/enroll", { method: "POST" }),

  confirmMfa: (code: string) =>
    request<{ recovery_codes: string[] }>("/api/v1/auth/mfa/confirm", { method: "POST", body: JSON.stringify({ code }) }),

  disableMfa: () => request<{ message: string }>("/api/v1/auth/mfa", { method: "DELETE" }),

  listSessions: () => request<{ sessions: SessionSummary[] }>("/api/v1/auth/sessions"),

  revokeSession: (id: string) => request<{ message: string }>(`/api/v1/auth/sessions/${id}`, { method: "DELETE" }),

  revokeOtherSessions: () => request<{ message: string }>("/api/v1/auth/sessions/revoke-others", { method: "POST" }),

  listOrganizations: () => request<{ organizations: Organization[] }>("/api/v1/organizations"),

  createOrganization: (name: string) =>
    request<Organization>("/api/v1/organizations", { method: "POST", body: JSON.stringify({ name }) }),

  getOrganization: (id: string) =>
    request<{ organization: Organization; your_role: string }>(`/api/v1/organizations/${id}`),

  renameOrganization: (id: string, name: string) =>
    request<Organization>(`/api/v1/organizations/${id}`, { method: "PATCH", body: JSON.stringify({ name }) }),

  listMembers: (orgId: string) => request<{ members: Member[] }>(`/api/v1/organizations/${orgId}/members`),

  inviteMember: (orgId: string, email: string, role: "admin" | "member") =>
    request<{ message: string }>(`/api/v1/organizations/${orgId}/invitations`, {
      method: "POST",
      body: JSON.stringify({ email, role }),
    }),

  updateMemberRole: (orgId: string, userId: string, role: "admin" | "member") =>
    request<{ message: string }>(`/api/v1/organizations/${orgId}/members/${userId}`, {
      method: "PATCH",
      body: JSON.stringify({ role }),
    }),

  removeMember: (orgId: string, userId: string) =>
    request<{ message: string }>(`/api/v1/organizations/${orgId}/members/${userId}`, { method: "DELETE" }),

  leaveOrganization: (orgId: string) =>
    request<{ message: string }>(`/api/v1/organizations/${orgId}/leave`, { method: "POST" }),

  acceptInvitation: (token: string) =>
    request<Organization>("/api/v1/invitations/accept", { method: "POST", body: JSON.stringify({ token }) }),

  usage: () => request<UsageReport>("/api/v1/usage"),

  // Only ever reachable when the API was started with APP_ENV=development
  // — see backend/internal/notifications and docs/implementation-status.md.
  devListEmails: () =>
    request<{ emails: CapturedEmail[] }>("/api/v1/dev/emails", {}, { auth: false }),

  // --- Admin panel (requires platform_role === "admin" — see backend/internal/admin) ---

  adminStats: () => request<AdminStats>("/api/v1/admin/stats"),

  adminListUsers: (q: string, limit: number, offset: number) =>
    request<{ users: AdminUser[]; total: number }>(
      `/api/v1/admin/users?${new URLSearchParams({ q, limit: String(limit), offset: String(offset) })}`,
    ),

  adminGetUser: (userId: string) =>
    request<{ user: AdminUser; organizations: Organization[] }>(`/api/v1/admin/users/${userId}`),

  adminSetUserStatus: (userId: string, status: "active" | "suspended" | "deactivated") =>
    request<{ message: string }>(`/api/v1/admin/users/${userId}/status`, {
      method: "PATCH",
      body: JSON.stringify({ status }),
    }),

  adminSetUserQuota: (userId: string, storageQuotaBytes: number) =>
    request<{ message: string }>(`/api/v1/admin/users/${userId}/quota`, {
      method: "PATCH",
      body: JSON.stringify({ storage_quota_bytes: storageQuotaBytes }),
    }),

  adminSetUserRetention: (userId: string, retentionKeepSnapshots: number) =>
    request<{ message: string }>(`/api/v1/admin/users/${userId}/retention`, {
      method: "PATCH",
      body: JSON.stringify({ retention_keep_snapshots: retentionKeepSnapshots }),
    }),

  adminSetUserPlatformRole: (userId: string, platformRole: "none" | "operator" | "admin") =>
    request<{ message: string }>(`/api/v1/admin/users/${userId}/platform-role`, {
      method: "PATCH",
      body: JSON.stringify({ platform_role: platformRole }),
    }),

  adminListOrganizations: (q: string, limit: number, offset: number) =>
    request<{ organizations: AdminOrganization[]; total: number }>(
      `/api/v1/admin/organizations?${new URLSearchParams({ q, limit: String(limit), offset: String(offset) })}`,
    ),

  adminGetOrganization: (orgId: string) =>
    request<{ organization: AdminOrganization; members: Member[] }>(`/api/v1/admin/organizations/${orgId}`),

  adminSetOrgQuota: (orgId: string, storageQuotaBytes: number) =>
    request<{ message: string }>(`/api/v1/admin/organizations/${orgId}/quota`, {
      method: "PATCH",
      body: JSON.stringify({ storage_quota_bytes: storageQuotaBytes }),
    }),

  adminSetOrgRetention: (orgId: string, retentionKeepSnapshots: number) =>
    request<{ message: string }>(`/api/v1/admin/organizations/${orgId}/retention`, {
      method: "PATCH",
      body: JSON.stringify({ retention_keep_snapshots: retentionKeepSnapshots }),
    }),

  adminListAudit: (limit: number, offset: number) =>
    request<{ events: AuditEvent[] }>(
      `/api/v1/admin/audit?${new URLSearchParams({ limit: String(limit), offset: String(offset) })}`,
    ),

  // Device code auth (gh-style `deepctl login`) — the browser side.
  // The CLI itself calls /auth/device/code and /auth/device/token
  // directly, unauthenticated; these three are what the person approving
  // in their browser uses, and do require a logged-in session.
  deviceAuthVerify: (userCode: string) =>
    request<{ client_name: string; expires_at: string }>(
      `/api/v1/auth/device/verify?${new URLSearchParams({ user_code: userCode })}`,
    ),

  deviceAuthApprove: (userCode: string) =>
    request<{ message: string }>("/api/v1/auth/device/approve", {
      method: "POST",
      body: JSON.stringify({ user_code: userCode }),
    }),

  deviceAuthDeny: (userCode: string) =>
    request<{ message: string }>("/api/v1/auth/device/deny", {
      method: "POST",
      body: JSON.stringify({ user_code: userCode }),
    }),
};
