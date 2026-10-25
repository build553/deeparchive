import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { api, setAccessToken, setRefreshHandler } from "../../lib/apiClient";
import type { Me, Session } from "../../lib/types";

// The refresh token is the only credential persisted across page reloads.
// The access token lives in memory only (module-scoped in apiClient) —
// never in localStorage, so an XSS payload reading localStorage can't
// lift a currently-valid short-lived access token, only the longer-lived
// refresh token (which is itself bearer-equivalent either way; this is a
// real, accepted tradeoff of storing any credential in a browser at all,
// not something this app tries to fully solve).
const REFRESH_TOKEN_KEY = "deeparchive.refresh_token";

interface AuthContextValue {
  me: Me | null;
  loading: boolean;
  login: (email: string, password: string, mfaCode?: string) => Promise<void>;
  completeAdminOtp: (challengeId: string, code: string) => Promise<void>;
  register: (email: string, password: string, displayName: string, consent: { termsAccepted: boolean; ageConfirmed: boolean }) => Promise<void>;
  logout: () => Promise<void>;
  refreshMe: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [me, setMe] = useState<Me | null>(null);
  const [loading, setLoading] = useState(true);

  const doRefresh = useCallback(async () => {
    const stored = localStorage.getItem(REFRESH_TOKEN_KEY);
    if (!stored) {
      throw new Error("no refresh token stored");
    }
    const session = await api.refresh(stored);
    setAccessToken(session.access_token);
    localStorage.setItem(REFRESH_TOKEN_KEY, session.refresh_token);
  }, []);

  // Registered once so apiClient's request() can transparently retry a
  // 401 after refreshing, without AuthContext and apiClient depending on
  // each other's internals beyond this one function.
  useEffect(() => {
    setRefreshHandler(doRefresh);
    return () => setRefreshHandler(null);
  }, [doRefresh]);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const stored = localStorage.getItem(REFRESH_TOKEN_KEY);
      if (!stored) {
        setLoading(false);
        return;
      }
      try {
        await doRefresh();
        const meResp = await api.me();
        if (!cancelled) setMe(meResp);
      } catch {
        localStorage.removeItem(REFRESH_TOKEN_KEY);
        setAccessToken(null);
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [doRefresh]);

  const adoptSession = useCallback(async (session: Session) => {
    setAccessToken(session.access_token);
    localStorage.setItem(REFRESH_TOKEN_KEY, session.refresh_token);
    setMe(await api.me());
  }, []);

  const login = useCallback(
    async (email: string, password: string, mfaCode?: string) => {
      await adoptSession(await api.login(email, password, mfaCode));
    },
    [adoptSession],
  );

  // Completes the second step of an admin login: the password (and TOTP,
  // if enrolled) already passed in login() above, which returned a
  // challenge_id instead of a session. This redeems the emailed code.
  const completeAdminOtp = useCallback(
    async (challengeId: string, code: string) => {
      await adoptSession(await api.verifyAdminOtp(challengeId, code));
    },
    [adoptSession],
  );

  const register = useCallback(async (email: string, password: string, displayName: string, consent: { termsAccepted: boolean; ageConfirmed: boolean }) => {
    await api.register(email, password, displayName, consent);
  }, []);

  const logout = useCallback(async () => {
    try {
      await api.logout();
    } catch {
      // Best effort — the local session is cleared regardless, so a
      // failed server-side logout call doesn't trap the user logged in.
    }
    setAccessToken(null);
    localStorage.removeItem(REFRESH_TOKEN_KEY);
    setMe(null);
  }, []);

  const refreshMe = useCallback(async () => {
    setMe(await api.me());
  }, []);

  const value = useMemo(
    () => ({ me, loading, login, completeAdminOtp, register, logout, refreshMe }),
    [me, loading, login, completeAdminOtp, register, logout, refreshMe],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) {
    throw new Error("useAuth must be used within an AuthProvider");
  }
  return ctx;
}
