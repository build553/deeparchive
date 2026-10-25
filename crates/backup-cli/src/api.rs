//! Thin HTTP client for the DeepArchive Go API. Knows the request/response
//! shapes; knows nothing about encryption or chunking — that stays in
//! backup-core and the command implementations.

use std::net::SocketAddr;

use anyhow::{anyhow, bail, Context, Result};
use reqwest::blocking::{Client, Response};
use serde::{Deserialize, Serialize};

pub struct Api {
    http: Client,
    base: String,
}

impl Api {
    pub fn new(base: &str, resolves: &[(String, SocketAddr)]) -> Result<Self> {
        let mut builder = Client::builder();
        for (host, addr) in resolves {
            builder = builder.resolve(host, *addr);
        }
        let http = builder.build().context("building HTTP client")?;
        Ok(Self {
            http,
            base: base.trim_end_matches('/').to_string(),
        })
    }

    fn url(&self, path: &str) -> String {
        format!("{}{}", self.base, path)
    }

    /// Turns a non-2xx response into an error carrying the response body
    /// (the API always returns a JSON `{"error":{...}}` on failure — see
    /// backend/internal/httpx/respond.go), rather than a bare status code.
    fn check(resp: Response) -> Result<Response> {
        if resp.status().is_success() {
            return Ok(resp);
        }
        let status = resp.status();
        let body = resp.text().unwrap_or_default();
        bail!("request failed: {status}: {body}")
    }

    pub fn login(&self, email: &str, password: &str) -> Result<LoginSession> {
        #[derive(Serialize)]
        struct Req<'a> {
            email: &'a str,
            password: &'a str,
        }
        let resp: LoginSession = Self::check(
            self.http
                .post(self.url("/api/v1/auth/login"))
                .json(&Req { email, password })
                .send()?,
        )?
        .json()
        .context("decoding login response")?;
        Ok(resp)
    }

    /// Exchanges a refresh token for a fresh access+refresh pair. Called
    /// on every CLI invocation that uses a stored login (not
    /// --email/--password) rather than parsing the access token's expiry
    /// client-side — simpler, and self-correcting if the two clocks
    /// disagree. The API rotates the refresh token on every use, so the
    /// caller must persist the new one or the next refresh will fail.
    pub fn refresh(&self, refresh_token: &str) -> Result<LoginSession> {
        #[derive(Serialize)]
        struct Req<'a> {
            refresh_token: &'a str,
        }
        let resp: LoginSession = Self::check(
            self.http
                .post(self.url("/api/v1/auth/refresh"))
                .json(&Req { refresh_token })
                .send()?,
        )?
        .json()
        .context("decoding refresh response")?;
        Ok(resp)
    }

    /// Starts a device-code sign-in (the `backup-cli login` flow, the
    /// same shape `gh auth login` uses): no password ever touches this
    /// process, only a short code a person confirms in their own
    /// browser.
    pub fn device_code(&self, client_name: &str) -> Result<DeviceCodeStart> {
        #[derive(Serialize)]
        struct Req<'a> {
            client_name: &'a str,
        }
        let resp: DeviceCodeStart = Self::check(
            self.http
                .post(self.url("/api/v1/auth/device/code"))
                .json(&Req { client_name })
                .send()?,
        )?
        .json()
        .context("decoding device code response")?;
        Ok(resp)
    }

    /// Polls once. Returns Ok(DevicePoll::Pending) rather than an error
    /// for the normal "not approved yet" case — only a genuine request
    /// failure (network, decode) is an Err.
    pub fn device_token(&self, device_code: &str) -> Result<DevicePoll> {
        #[derive(Serialize)]
        struct Req<'a> {
            device_code: &'a str,
        }
        let resp = self
            .http
            .post(self.url("/api/v1/auth/device/token"))
            .json(&Req { device_code })
            .send()?;

        if resp.status().is_success() {
            let session: LoginSession = resp.json().context("decoding device token response")?;
            return Ok(DevicePoll::Approved(session));
        }

        let status = resp.status();
        #[derive(Deserialize)]
        struct ErrBody {
            error: ErrInner,
        }
        #[derive(Deserialize)]
        struct ErrInner {
            code: String,
        }
        let code = resp
            .json::<ErrBody>()
            .map(|b| b.error.code)
            .unwrap_or_else(|_| "unknown".to_string());
        match code.as_str() {
            "authorization_pending" => Ok(DevicePoll::Pending),
            "slow_down" => Ok(DevicePoll::SlowDown),
            "access_denied" => Ok(DevicePoll::Denied),
            "expired_token" => Ok(DevicePoll::Expired),
            other => bail!("device token poll failed ({status}): {other}"),
        }
    }

    pub fn enroll_device(
        &self,
        user_token: &str,
        name: &str,
        os: &str,
        client_version: &str,
        public_key_b64: &str,
    ) -> Result<String> {
        #[derive(Serialize)]
        struct Req<'a> {
            name: &'a str,
            os: &'a str,
            client_version: &'a str,
            public_key: &'a str,
        }
        #[derive(Deserialize)]
        struct Resp {
            id: String,
        }
        let resp: Resp = Self::check(
            self.http
                .post(self.url("/api/v1/devices/enroll"))
                .bearer_auth(user_token)
                .json(&Req {
                    name,
                    os,
                    client_version,
                    public_key: public_key_b64,
                })
                .send()?,
        )?
        .json()
        .context("decoding enroll response")?;
        Ok(resp.id)
    }

    pub fn approve_device(&self, user_token: &str, device_id: &str) -> Result<()> {
        Self::check(
            self.http
                .post(self.url(&format!("/api/v1/devices/{device_id}/approve")))
                .bearer_auth(user_token)
                .send()?,
        )?;
        Ok(())
    }

    pub fn issue_challenge(&self, device_id: &str) -> Result<(String, String)> {
        #[derive(Deserialize)]
        struct Resp {
            challenge_id: String,
            nonce: String,
        }
        let resp: Resp = Self::check(
            self.http
                .post(self.url(&format!("/api/v1/devices/{device_id}/challenge")))
                .send()?,
        )?
        .json()
        .context("decoding challenge response")?;
        Ok((resp.challenge_id, resp.nonce))
    }

    pub fn authenticate_device(
        &self,
        device_id: &str,
        challenge_id: &str,
        signature_b64: &str,
    ) -> Result<String> {
        #[derive(Serialize)]
        struct Req<'a> {
            challenge_id: &'a str,
            signature: &'a str,
        }
        #[derive(Deserialize)]
        struct Resp {
            access_token: String,
        }
        let resp: Resp = Self::check(
            self.http
                .post(self.url(&format!("/api/v1/devices/{device_id}/authenticate")))
                .json(&Req {
                    challenge_id,
                    signature: signature_b64,
                })
                .send()?,
        )?
        .json()
        .context("decoding device authenticate response")?;
        Ok(resp.access_token)
    }

    pub fn negotiate(
        &self,
        device_token: &str,
        device_id: &str,
        hashes: &[String],
    ) -> Result<Vec<ChunkStatus>> {
        #[derive(Serialize)]
        struct Req<'a> {
            chunk_hashes: &'a [String],
        }
        #[derive(Deserialize)]
        struct Resp {
            chunks: Vec<ChunkStatus>,
        }
        let resp: Resp = Self::check(
            self.http
                .post(self.url(&format!("/api/v1/devices/{device_id}/backups/negotiate")))
                .bearer_auth(device_token)
                .json(&Req {
                    chunk_hashes: hashes,
                })
                .send()?,
        )?
        .json()
        .context("decoding negotiate response")?;
        Ok(resp.chunks)
    }

    pub fn commit_snapshot(
        &self,
        device_token: &str,
        device_id: &str,
        req: &CommitRequest,
    ) -> Result<String> {
        #[derive(Deserialize)]
        struct Resp {
            id: String,
        }
        let resp: Resp = Self::check(
            self.http
                .post(self.url(&format!("/api/v1/devices/{device_id}/backups")))
                .bearer_auth(device_token)
                .json(req)
                .send()?,
        )?
        .json()
        .context("decoding commit response")?;
        Ok(resp.id)
    }

    pub fn list_backups(&self, user_token: &str) -> Result<Vec<SnapshotSummary>> {
        #[derive(Deserialize)]
        struct Resp {
            snapshots: Vec<SnapshotSummary>,
        }
        let resp: Resp = Self::check(
            self.http
                .get(self.url("/api/v1/backups"))
                .bearer_auth(user_token)
                .send()?,
        )?
        .json()
        .context("decoding backups list response")?;
        Ok(resp.snapshots)
    }

    pub fn get_snapshot(&self, user_token: &str, snapshot_id: &str) -> Result<SnapshotDetail> {
        Self::check(
            self.http
                .get(self.url(&format!("/api/v1/backups/{snapshot_id}")))
                .bearer_auth(user_token)
                .send()?,
        )?
        .json()
        .context("decoding snapshot detail response")
    }

    /// PUTs directly to the presigned URL — no Authorization header; the
    /// signature embedded in the URL itself is the authorization.
    pub fn put_bytes(&self, presigned_url: &str, body: Vec<u8>) -> Result<()> {
        Self::check(self.http.put(presigned_url).body(body).send()?)?;
        Ok(())
    }

    pub fn get_bytes(&self, presigned_url: &str) -> Result<Vec<u8>> {
        let resp = Self::check(self.http.get(presigned_url).send()?)?;
        Ok(resp.bytes()?.to_vec())
    }
}

#[derive(Deserialize)]
pub struct LoginSession {
    pub access_token: String,
    pub refresh_token: String,
}

#[derive(Deserialize)]
pub struct DeviceCodeStart {
    #[allow(dead_code)]
    pub device_code: String,
    pub user_code: String,
    #[allow(dead_code)]
    pub verification_uri: String,
    pub verification_uri_complete: String,
    #[allow(dead_code)]
    pub expires_in: u64,
    pub interval: u64,
}

pub enum DevicePoll {
    Pending,
    SlowDown,
    Approved(LoginSession),
    Denied,
    Expired,
}

#[derive(Deserialize)]
pub struct ChunkStatus {
    pub hash: String,
    pub exists: bool,
    #[allow(dead_code)]
    pub object_key: String,
    pub upload_url: Option<String>,
}

#[derive(Serialize)]
pub struct CommitChunk {
    pub hash: String,
    pub ciphertext_size: u64,
}

#[derive(Serialize)]
pub struct CommitRequest {
    pub manifest_nonce: String,
    pub manifest_ciphertext: String,
    pub total_size: u64,
    pub chunks: Vec<CommitChunk>,
}

#[derive(Deserialize)]
pub struct SnapshotSummary {
    pub id: String,
    pub created_at: String,
    pub total_size: u64,
    pub chunk_count: u64,
}

#[derive(Deserialize)]
pub struct SnapshotDetail {
    pub manifest_nonce: String,
    pub manifest_ciphertext: String,
    pub chunks: Vec<ChunkLocation>,
}

#[derive(Deserialize)]
pub struct ChunkLocation {
    pub hash: String,
    pub download_url: String,
}

/// Parses `HOST:IP:PORT` (the shape `--resolve` takes) into a (host, addr)
/// pair, mirroring curl's `--resolve` option. Needed only for local Docker
/// Compose development, where a presigned object-storage URL embeds the
/// compose-internal hostname "minio", which nothing outside the compose
/// network can resolve by default.
pub fn parse_resolve(spec: &str) -> Result<(String, SocketAddr)> {
    let (host, rest) = spec
        .split_once(':')
        .ok_or_else(|| anyhow!("--resolve must be HOST:IP:PORT, got {spec:?}"))?;
    let addr: SocketAddr = rest
        .parse()
        .map_err(|e| anyhow!("--resolve {spec:?}: invalid IP:PORT {rest:?}: {e}"))?;
    Ok((host.to_string(), addr))
}
