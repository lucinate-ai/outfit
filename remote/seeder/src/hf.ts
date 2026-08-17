/**
 * The Hugging Face side of the seed: what to fetch, how big it is, what it
 * should hash to, and the URL to range-read it from.
 *
 * The official SDK (`@huggingface/hub`) does the listing, the auth and the repo
 * addressing. Byte movement deliberately does NOT go through it — see
 * resolveUrl below and transfer.ts for why.
 */

import { listFiles } from '@huggingface/hub';
import { applySelection, type SeedSelection } from '../../lambda/shared/seed/contract';

export const HUB_URL = 'https://huggingface.co';

export interface RemoteFile {
  /** Path within the repository. */
  path: string;
  /** Key under the weights prefix — differs from path only for expectSingle. */
  storeAs: string;
  size: number;
  /**
   * The sha256 the source publishes, when it publishes one. Present for
   * LFS/Xet-backed files, which is every file large enough to matter; absent
   * for small plain-git files (config.json and friends), whose ETag is a git
   * blob hash rather than a content sha256. Verification falls back to size for
   * those — see transfer.ts.
   */
  sha256?: string;
}

export interface RepoContents {
  /** The resolved commit sha. Never a branch name, so the manifest pins a point. */
  revision: string;
  files: RemoteFile[];
  totalBytes: number;
}

/**
 * The URL a part is read from.
 *
 * This is the `resolve` endpoint, NOT the signed CDN link that
 * `fileDownloadInfo` hands back, and that is a deliberate decision from the
 * spike: the CDN link carries an `Expires` roughly an hour out — the same order
 * as the seed's own lifetime cap — so a cached link can expire mid-transfer.
 * Reading each part through `resolve` and following the redirect costs one extra
 * 302 (under a kilobyte) per part and removes URL expiry from the set of things
 * this code has to model.
 */
export function resolveUrl(modelId: string, revision: string, path: string): string {
  const rev = encodeURIComponent(revision || 'main');
  const encodedPath = path.split('/').map(encodeURIComponent).join('/');
  return `${HUB_URL}/${modelId}/resolve/${rev}/${encodedPath}`;
}

export function authHeaders(token: string): Record<string, string> {
  return token ? { authorization: `Bearer ${token}` } : {};
}

/**
 * Resolve the revision a fetch will use. A pin is returned as given; otherwise
 * the repository's current default-branch commit is read from `x-repo-commit`
 * on a HEAD of any file, so the manifest records a commit even when the request
 * named none.
 */
export async function resolveRevision(
  modelId: string,
  requested: string,
  probePath: string,
  token: string,
  fetchImpl: typeof fetch = fetch,
): Promise<string> {
  const response = await fetchImpl(resolveUrl(modelId, requested || 'main', probePath), {
    method: 'HEAD',
    headers: authHeaders(token),
    redirect: 'follow',
  });
  if (!response.ok) {
    throw new Error(
      `cannot resolve ${modelId}@${requested || 'main'}: HTTP ${response.status} ${response.statusText}`,
    );
  }
  const commit = response.headers.get('x-repo-commit');
  // A pin the caller gave is authoritative even if the header is missing.
  return commit || requested || 'main';
}

/**
 * List the repository and apply the runner's selection.
 *
 * `lfs.oid` is the source's sha256 for LFS-tracked files. `listFiles` gives
 * sizes and that oid in one paginated walk, so no per-file HEAD is needed to
 * plan the transfer — which matters because a large checkpoint is dozens of
 * files and each HEAD would be a round trip before any bytes move.
 */
export async function listSelectedFiles(
  modelId: string,
  revision: string,
  selection: SeedSelection,
  token: string,
  deps: { listFiles?: typeof listFiles } = {},
): Promise<RemoteFile[]> {
  const list = deps.listFiles ?? listFiles;
  const entries: { path: string; size: number; sha256?: string }[] = [];
  for await (const entry of list({
    repo: { type: 'model', name: modelId },
    revision,
    recursive: true,
    ...(token ? { accessToken: token } : {}),
  })) {
    if (entry.type !== 'file') {
      continue;
    }
    entries.push({ path: entry.path, size: entry.size, sha256: entry.lfs?.oid });
  }
  if (entries.length === 0) {
    throw new Error(`${modelId}@${revision} lists no files — is the repository empty or gated?`);
  }

  // applySelection enforces expectSingle, so an ambiguous single-file match
  // fails here rather than silently storing one shard of a split quant.
  const selected = applySelection(
    entries.map((e) => e.path),
    selection,
  );
  const bySize = new Map(entries.map((e) => [e.path, e]));
  return selected.map(({ path, storeAs }) => {
    const entry = bySize.get(path);
    return { path, storeAs, size: entry?.size ?? 0, sha256: entry?.sha256 };
  });
}

/** Everything the transfer needs, resolved in one pass. */
export async function planTransfer(
  modelId: string,
  requestedRevision: string,
  selection: SeedSelection,
  token: string,
  deps: { listFiles?: typeof listFiles; fetch?: typeof fetch } = {},
): Promise<RepoContents> {
  // The listing is done against the requested revision (or the default branch),
  // then the commit is resolved from a file that is known to exist — resolving
  // first would need a path we do not yet have.
  const provisional = requestedRevision || 'main';
  const files = await listSelectedFiles(modelId, provisional, selection, token, deps);
  const revision = await resolveRevision(
    modelId,
    provisional,
    files[0].path,
    token,
    deps.fetch ?? fetch,
  );
  return { revision, files, totalBytes: files.reduce((sum, f) => sum + f.size, 0) };
}
