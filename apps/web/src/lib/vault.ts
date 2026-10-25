// Browser-side half of the backup format, so a person can browse and
// download their own files from the dashboard. The server only ever holds
// ciphertext; the vault key is derived here, in memory, from a passphrase
// and the wrapped key out of the user's own device.json. Mirrors
// backend/internal/backupclient (Go) and crates/backup-core (Rust):
// Argon2id (19 MiB, t=2, p=1) -> AES-256-GCM, chunk AAD = its content hash,
// manifest AAD = "deeparchive-manifest-v1".
import { argon2id } from "hash-wasm";

export interface WrappedVaultKey {
  format_version: number;
  salt: string;
  nonce: string;
  ciphertext: string;
}

export interface ChunkRef {
  hash: string;
  offset: number;
  length: number;
}

export interface FileEntry {
  path: string;
  size: number;
  mode: number;
  modified: string;
  chunks: ChunkRef[];
}

export interface Manifest {
  format_version: number;
  created_at: string;
  files: FileEntry[];
  chunks: Record<string, { nonce: string; ciphertext_size: number }>;
  total_size: number;
}

const enc = new TextEncoder();
const hex = (s: string) => Uint8Array.from(s.match(/../g) ?? [], (h) => parseInt(h, 16));
const b64 = (s: string) => Uint8Array.from(atob(s), (c) => c.charCodeAt(0));

export async function unwrapVaultKey(w: WrappedVaultKey, passphrase: string): Promise<CryptoKey> {
  if (w.format_version !== 1) throw new Error(`Unsupported vault format version ${w.format_version}.`);
  const kek = await argon2id({
    password: passphrase,
    salt: hex(w.salt),
    parallelism: 1,
    iterations: 2,
    memorySize: 19456,
    hashLength: 32,
    outputType: "binary",
  });
  const kekKey = await crypto.subtle.importKey("raw", new Uint8Array(kek), "AES-GCM", false, ["decrypt"]);
  let raw: ArrayBuffer;
  try {
    raw = await crypto.subtle.decrypt({ name: "AES-GCM", iv: hex(w.nonce) }, kekKey, hex(w.ciphertext));
  } catch {
    throw new Error("That passphrase doesn't match. Please try again.");
  }
  return crypto.subtle.importKey("raw", raw, "AES-GCM", false, ["decrypt"]);
}

export async function decryptManifest(key: CryptoKey, nonceB64: string, ciphertextB64: string): Promise<Manifest> {
  let plain: ArrayBuffer;
  try {
    plain = await crypto.subtle.decrypt(
      { name: "AES-GCM", iv: b64(nonceB64), additionalData: enc.encode("deeparchive-manifest-v1") },
      key,
      b64(ciphertextB64),
    );
  } catch {
    throw new Error("This vault can't open this snapshot (it was made by a different vault).");
  }
  const m = JSON.parse(new TextDecoder().decode(plain)) as Manifest;
  if (m.format_version !== 1) throw new Error(`Unsupported manifest version ${m.format_version}.`);
  return m;
}

// downloadFile fetches each chunk the file references (via presigned URLs
// the API already issued), decrypts it with its own hash as AAD — so a
// swapped or corrupted chunk fails loudly — and returns the reassembled file.
export async function fetchFile(
  file: FileEntry,
  key: CryptoKey,
  manifest: Manifest,
  urls: Map<string, string>,
): Promise<Blob> {
  const cache = new Map<string, Uint8Array>();
  const parts: Uint8Array[] = [];
  for (const ref of file.chunks) {
    let plain = cache.get(ref.hash);
    if (!plain) {
      const url = urls.get(ref.hash);
      const meta = manifest.chunks[ref.hash];
      if (!url || !meta) throw new Error(`Missing chunk ${ref.hash.slice(0, 8)}.`);
      const resp = await fetch(url);
      if (!resp.ok) throw new Error(`Downloading chunk failed (${resp.status}).`);
      const buf = await crypto.subtle.decrypt(
        { name: "AES-GCM", iv: hex(meta.nonce), additionalData: enc.encode(ref.hash) },
        key,
        await resp.arrayBuffer(),
      );
      plain = new Uint8Array(buf);
      cache.set(ref.hash, plain);
    }
    if (plain.length !== ref.length) throw new Error("A chunk has the wrong length; the backup may be corrupt.");
    parts.push(plain);
  }
  return new Blob(parts as BlobPart[]);
}

export interface TreeNode {
  name: string;
  path: string;
  size: number;
  files: number;
  file?: FileEntry; // set for files
  children: TreeNode[];
}

export function buildTree(files: FileEntry[]): TreeNode {
  const root: TreeNode = { name: "", path: "", size: 0, files: 0, children: [] };
  for (const f of files) {
    const parts = f.path.split("/").filter(Boolean);
    let node = root;
    node.size += f.size;
    node.files += 1;
    parts.forEach((part, i) => {
      const path = parts.slice(0, i + 1).join("/");
      let child = node.children.find((c) => c.path === path);
      if (!child) {
        child = { name: part, path, size: 0, files: 0, children: [] };
        node.children.push(child);
      }
      if (i === parts.length - 1) {
        child.file = f;
        child.size = f.size;
        child.files = 1;
      } else {
        child.size += f.size;
        child.files += 1;
      }
      node = child;
    });
  }
  const sort = (n: TreeNode) => {
    n.children.sort((a, b) => Number(!!a.file) - Number(!!b.file) || a.name.localeCompare(b.name));
    n.children.forEach(sort);
  };
  sort(root);
  return root;
}
