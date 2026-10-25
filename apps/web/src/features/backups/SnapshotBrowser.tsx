import { useEffect, useMemo, useState, type FormEvent } from "react";
import { decryptManifest, fetchFile, unwrapVaultKey, buildTree, type Manifest, type TreeNode, type WrappedVaultKey } from "../../lib/vault";
import { api, ApiRequestError } from "../../lib/apiClient";
import { formatBytes, formatDateTime } from "../../lib/format";
import type { SnapshotDetail } from "../../lib/types";

// Unlocked vault keys for this tab, so opening a second snapshot from the
// same device doesn't ask again. Memory only: gone on reload or "Lock".
const unlockedKeys = new Map<string, CryptoKey>();

// Browse a snapshot's folders and download single files, entirely in this
// tab. The server keeps only the passphrase-encrypted vault key; the
// passphrase and the unlocked key never leave the browser.
export default function SnapshotBrowser({ snapshot }: { snapshot: SnapshotDetail }) {
  const [wrapped, setWrapped] = useState<WrappedVaultKey | null | undefined>(undefined); // undefined = loading, null = none stored
  const [deviceFile, setDeviceFile] = useState<File | null>(null);
  const [remember, setRemember] = useState(true);
  const [passphrase, setPassphrase] = useState("");
  const [unlocking, setUnlocking] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [session, setSession] = useState<{ key: CryptoKey; manifest: Manifest } | null>(null);
  const [busyPath, setBusyPath] = useState<string | null>(null);

  const urls = useMemo(() => new Map(snapshot.chunks.map((c) => [c.hash, c.download_url])), [snapshot.chunks]);
  const tree = useMemo(() => (session ? buildTree(session.manifest.files) : null), [session]);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const cached = unlockedKeys.get(snapshot.device_id);
      if (cached) {
        try {
          const manifest = await decryptManifest(cached, snapshot.manifest_nonce, snapshot.manifest_ciphertext);
          if (!cancelled) setSession({ key: cached, manifest });
          return;
        } catch {
          unlockedKeys.delete(snapshot.device_id);
        }
      }
      try {
        const r = await api.getVaultKey(snapshot.device_id);
        if (!cancelled) setWrapped(r.wrapped_vault_key);
      } catch (err) {
        if (cancelled) return;
        if (err instanceof ApiRequestError && err.code === "no_vault_key") setWrapped(null);
        else {
          setWrapped(null);
          setError("Couldn't check your saved key. You can still unlock with your device file.");
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [snapshot.device_id, snapshot.manifest_nonce, snapshot.manifest_ciphertext]);

  async function onUnlock(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setUnlocking(true);
    try {
      let source = wrapped ?? undefined;
      let fromFile = false;
      if (!source) {
        if (!deviceFile) throw new Error("Choose your device file first.");
        try {
          source = JSON.parse(await deviceFile.text()).wrapped_vault_key as WrappedVaultKey;
        } catch {
          /* handled below */
        }
        if (!source?.salt) throw new Error("That doesn't look like a DeepArchive device file (device.json).");
        fromFile = true;
      }
      const key = await unwrapVaultKey(source, passphrase);
      const manifest = await decryptManifest(key, snapshot.manifest_nonce, snapshot.manifest_ciphertext);
      unlockedKeys.set(snapshot.device_id, key);
      setSession({ key, manifest });
      setPassphrase("");
      if (fromFile && remember) {
        // Best effort: the passphrase-wrapped key is already encrypted, so
        // saving it just means next time only the passphrase is needed.
        api.putVaultKey(snapshot.device_id, source!).catch(() => undefined);
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : "Couldn't unlock.");
    } finally {
      setUnlocking(false);
    }
  }

  async function onDownload(node: TreeNode) {
    if (!session || !node.file) return;
    setBusyPath(node.path);
    setError(null);
    try {
      const blob = await fetchFile(node.file, session.key, session.manifest, urls);
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = node.name;
      a.click();
      setTimeout(() => URL.revokeObjectURL(a.href), 10_000);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Download failed.");
    } finally {
      setBusyPath(null);
    }
  }

  if (!session || !tree) {
    if (wrapped === undefined) {
      return (
        <div className="card">
          <h3>Browse files</h3>
          <p className="hint">Getting ready…</p>
        </div>
      );
    }
    return (
      <div className="card">
        <h3>Browse files</h3>
        <p className="hint">
          {wrapped
            ? "Your files are encrypted. Enter your vault passphrase to open them. It's checked right here in your browser and never sent to us."
            : "This device didn't save its key to your account, so choose the device.json file created by deepctl init, then enter your vault passphrase."}
        </p>
        <form className="unlock-form" onSubmit={onUnlock}>
          {!wrapped && (
            <label>
              Device file
              <input type="file" accept=".json,application/json" onChange={(e) => setDeviceFile(e.target.files?.[0] ?? null)} required />
            </label>
          )}
          <label>
            Vault passphrase
            <input
              type="password"
              autoComplete="off"
              value={passphrase}
              onChange={(e) => setPassphrase(e.target.value)}
              required
              autoFocus={!!wrapped}
            />
          </label>
          {!wrapped && (
            <label className="check-row">
              <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
              <span>Remember this device, so next time I only need my passphrase</span>
            </label>
          )}
          <button type="submit" disabled={unlocking || (!wrapped && !deviceFile)}>
            {unlocking ? "Unlocking…" : "Unlock"}
          </button>
        </form>
        {error && <p className="form-error">{error}</p>}
      </div>
    );
  }

  return (
    <div className="card">
      <div className="tree-head">
        <div>
          <h3>Files</h3>
          <p className="hint">
            {tree.files} {tree.files === 1 ? "file" : "files"} · {formatBytes(tree.size)} · backed up{" "}
            {formatDateTime(session.manifest.created_at)}
          </p>
        </div>
        <button type="button" className="btn-secondary" onClick={() => {
            unlockedKeys.delete(snapshot.device_id);
            setSession(null);
          }}>
          Lock
        </button>
      </div>
      {error && <p className="form-error">{error}</p>}
      {tree.children.length === 0 ? (
        <p className="hint">This snapshot has no files.</p>
      ) : (
        <div className="tree" role="tree">
          {tree.children.map((n) => (
            <TreeRow key={n.path} node={n} depth={0} busyPath={busyPath} onDownload={onDownload} />
          ))}
        </div>
      )}
    </div>
  );
}

function TreeRow({ node, depth, busyPath, onDownload }: { node: TreeNode; depth: number; busyPath: string | null; onDownload: (n: TreeNode) => void }) {
  const indent = { paddingLeft: `calc(var(--space-3) + ${depth} * var(--space-5))` };
  if (node.file) {
    return (
      <div className="tree-row tree-file" role="treeitem" style={indent}>
        <span className="tree-name">
          <FileIcon />
          <span title={node.path}>{node.name}</span>
        </span>
        <span className="tree-meta tree-modified">{formatDateTime(node.file.modified)}</span>
        <span className="tree-meta tree-size">{formatBytes(node.size)}</span>
        <button type="button" className="btn-secondary tree-action" disabled={busyPath === node.path} onClick={() => onDownload(node)}>
          {busyPath === node.path ? "Preparing…" : "Download"}
        </button>
      </div>
    );
  }
  return (
    <details className="tree-dir" open={depth === 0}>
      <summary className="tree-row" role="treeitem" style={indent}>
        <span className="tree-name">
          <span className="tree-chevron" aria-hidden="true" />
          <FolderIcon />
          <span>{node.name}</span>
        </span>
        <span className="tree-meta tree-modified">
          {node.files} {node.files === 1 ? "file" : "files"}
        </span>
        <span className="tree-meta tree-size">{formatBytes(node.size)}</span>
        <span className="tree-action" />
      </summary>
      {node.children.map((c) => (
        <TreeRow key={c.path} node={c} depth={depth + 1} busyPath={busyPath} onDownload={onDownload} />
      ))}
    </details>
  );
}

function FolderIcon() {
  return (
    <svg className="tree-icon tree-icon-folder" viewBox="0 0 20 20" width="18" height="18" aria-hidden="true">
      <path d="M2 5.5A1.5 1.5 0 0 1 3.5 4h4l1.6 1.8h7.4A1.5 1.5 0 0 1 18 7.3v7.2a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 2 14.5v-9Z" fill="currentColor" />
    </svg>
  );
}

function FileIcon() {
  return (
    <svg className="tree-icon" viewBox="0 0 20 20" width="18" height="18" aria-hidden="true">
      <path d="M5.5 2.5h6l3.5 3.5v10a1.5 1.5 0 0 1-1.5 1.5h-8A1.5 1.5 0 0 1 4 16V4a1.5 1.5 0 0 1 1.5-1.5Z" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" />
      <path d="M11.5 2.5V6H15" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" />
    </svg>
  );
}
