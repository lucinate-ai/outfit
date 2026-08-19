import { describe, expect, it } from 'vitest';
import type { SeedSelection } from '../../lambda/shared/seed/contract';
import { authHeaders, listSelectedFiles, planTransfer, resolveRevision, resolveUrl } from '../src/hf';

/** A stand-in for the SDK's listFiles async generator. */
function fakeListFiles(
  entries: { path: string; size: number; type?: string; lfs?: { oid: string } }[],
  capture?: (params: Record<string, unknown>) => void,
) {
  return async function* (params: Record<string, unknown>) {
    capture?.(params);
    for (const e of entries) {
      yield { type: e.type ?? 'file', path: e.path, size: e.size, lfs: e.lfs } as never;
    }
  } as never;
}

/** A fetch that answers a HEAD on the resolve endpoint. */
function fakeHead(headers: Record<string, string>, ok = true, status = 200) {
  const seen: { url: string; method?: string; headers?: unknown }[] = [];
  const impl = (async (url: string, init?: RequestInit) => {
    seen.push({ url: String(url), method: init?.method, headers: init?.headers });
    return { ok, status, statusText: ok ? 'OK' : 'Not Found', headers: new Headers(headers) };
  }) as unknown as typeof fetch;
  return { impl, seen };
}

const ALL: SeedSelection = { include: ['*'] };

describe('the resolve URL', () => {
  it('addresses the resolve endpoint, not a signed CDN link', () => {
    // The signed link expires within about an hour — the same order as the
    // seed's own cap — so parts are read through resolve and redirected each
    // time instead.
    expect(resolveUrl('org/m', 'abc', 'model.safetensors')).toBe(
      'https://huggingface.co/org/m/resolve/abc/model.safetensors',
    );
  });

  it('defaults to main when no revision is given', () => {
    expect(resolveUrl('org/m', '', 'a.bin')).toContain('/resolve/main/');
  });

  it('escapes each path segment without escaping the separators', () => {
    expect(resolveUrl('org/m', 'main', 'sub dir/a+b.bin')).toBe(
      'https://huggingface.co/org/m/resolve/main/sub%20dir/a%2Bb.bin',
    );
  });
});

describe('auth headers', () => {
  it('sends a bearer token when there is one', () => {
    expect(authHeaders('hf_x')).toEqual({ authorization: 'Bearer hf_x' });
  });

  it('sends nothing for a public repository', () => {
    expect(authHeaders('')).toEqual({});
  });
});

describe('resolving the revision', () => {
  it('records the commit the default branch pointed at', async () => {
    // x-repo-commit is what makes an unpinned seed identifiable afterwards.
    const { impl, seen } = fakeHead({ 'x-repo-commit': 'a3f9c21' });
    expect(await resolveRevision('org/m', '', 'a.bin', '', impl)).toBe('a3f9c21');
    expect(seen[0].method).toBe('HEAD');
  });

  it('keeps a pin the caller gave even when the header is missing', async () => {
    const { impl } = fakeHead({});
    expect(await resolveRevision('org/m', 'pinned-sha', 'a.bin', '', impl)).toBe('pinned-sha');
  });

  it('prefers the resolved commit over a branch name', async () => {
    const { impl } = fakeHead({ 'x-repo-commit': 'deadbeef' });
    expect(await resolveRevision('org/m', 'main', 'a.bin', '', impl)).toBe('deadbeef');
  });

  it('fails loudly on a repository it cannot reach', async () => {
    const { impl } = fakeHead({}, false, 404);
    await expect(resolveRevision('org/missing', '', 'a.bin', '', impl)).rejects.toThrow(
      /cannot resolve org\/missing@main: HTTP 404/,
    );
  });

  it('passes the token through for a gated repository', async () => {
    const { impl, seen } = fakeHead({ 'x-repo-commit': 'c' });
    await resolveRevision('org/m', '', 'a.bin', 'hf_secret', impl);
    expect(seen[0].headers).toMatchObject({ authorization: 'Bearer hf_secret' });
  });
});

describe('listing and selecting', () => {
  it('takes the sha256 from the LFS oid, which is what verification needs', async () => {
    const files = await listSelectedFiles(
      'org/m',
      'main',
      ALL,
      '',
      { listFiles: fakeListFiles([{ path: 'model.safetensors', size: 100, lfs: { oid: 'abc123' } }]) },
    );
    expect(files[0]).toEqual({
      path: 'model.safetensors',
      storeAs: 'model.safetensors',
      size: 100,
      sha256: 'abc123',
    });
  });

  it('leaves the checksum absent for a small plain-git file', async () => {
    // Those publish a git blob hash, not a content sha256 — verifying against
    // it would fail every time, so there is nothing to verify against.
    const files = await listSelectedFiles('org/m', 'main', ALL, '', {
      listFiles: fakeListFiles([{ path: 'config.json', size: 12 }]),
    });
    expect(files[0].sha256).toBeUndefined();
    expect(files[0].size).toBe(12);
  });

  it('ignores directory entries', async () => {
    const files = await listSelectedFiles('org/m', 'main', ALL, '', {
      listFiles: fakeListFiles([
        { path: 'sub', size: 0, type: 'directory' },
        { path: 'sub/a.bin', size: 5 },
      ]),
    });
    expect(files.map((f) => f.path)).toEqual(['sub/a.bin']);
  });

  it('walks the repository recursively', async () => {
    let params: Record<string, unknown> = {};
    await listSelectedFiles('org/m', 'main', ALL, '', {
      listFiles: fakeListFiles([{ path: 'a.bin', size: 1 }], (p) => {
        params = p;
      }),
    });
    expect(params.recursive).toBe(true);
    expect(params.revision).toBe('main');
  });

  it('passes credentials only when there is a token', async () => {
    let params: Record<string, unknown> = {};
    const capture = (p: Record<string, unknown>) => {
      params = p;
    };
    await listSelectedFiles('org/m', 'main', ALL, 'hf_x', {
      listFiles: fakeListFiles([{ path: 'a.bin', size: 1 }], capture),
    });
    expect(params.accessToken).toBe('hf_x');

    await listSelectedFiles('org/m', 'main', ALL, '', {
      listFiles: fakeListFiles([{ path: 'a.bin', size: 1 }], capture),
    });
    expect(params).not.toHaveProperty('accessToken');
  });

  it('fails clearly on an empty or inaccessible repository', async () => {
    await expect(
      listSelectedFiles('org/m', 'main', ALL, '', { listFiles: fakeListFiles([]) }),
    ).rejects.toThrow(/lists no files — is the repository empty or gated/);
  });

  it('applies the runner selection, renaming a single-file match', async () => {
    const files = await listSelectedFiles(
      'org/m',
      'main',
      { include: ['*Q6*.gguf'], exclude: ['*mmproj*'], expectSingle: 'model.gguf' },
      '',
      {
        listFiles: fakeListFiles([
          { path: 'README.md', size: 1 },
          { path: 'm-Q6.gguf', size: 900, lfs: { oid: 'aa' } },
          { path: 'mmproj-Q6.gguf', size: 10 },
        ]),
      },
    );
    expect(files).toEqual([{ path: 'm-Q6.gguf', storeAs: 'model.gguf', size: 900, sha256: 'aa' }]);
  });

  it('carries a named companion through with its size and checksum', async () => {
    const files = await listSelectedFiles(
      'org/m',
      'main',
      {
        include: ['*Q6*.gguf'],
        expectSingle: 'model.gguf',
        companions: [{ storeAs: 'draft.gguf', file: 'dflash-kquant.gguf' }],
      },
      '',
      {
        listFiles: fakeListFiles([
          { path: 'm-Q6.gguf', size: 900, lfs: { oid: 'aa' } },
          { path: 'dflash-kquant.gguf', size: 120, lfs: { oid: 'bb' } },
        ]),
      },
    );
    expect(files).toEqual([
      { path: 'm-Q6.gguf', storeAs: 'model.gguf', size: 900, sha256: 'aa' },
      { path: 'dflash-kquant.gguf', storeAs: 'draft.gguf', size: 120, sha256: 'bb' },
    ]);
  });

  it('refuses a split quant rather than seeding one shard', async () => {
    await expect(
      listSelectedFiles(
        'org/m',
        'main',
        { include: ['*Q6*.gguf'], expectSingle: 'model.gguf' },
        '',
        {
          listFiles: fakeListFiles([
            { path: 'm-Q6-00001-of-00002.gguf', size: 1 },
            { path: 'm-Q6-00002-of-00002.gguf', size: 1 },
          ]),
        },
      ),
    ).rejects.toThrow(/expected exactly one file/);
  });
});

describe('planning a transfer', () => {
  it('resolves the revision and totals the bytes in one pass', async () => {
    const { impl } = fakeHead({ 'x-repo-commit': 'resolved-sha' });
    const plan = await planTransfer('org/m', '', ALL, '', {
      listFiles: fakeListFiles([
        { path: 'a.bin', size: 100, lfs: { oid: 'aa' } },
        { path: 'b.bin', size: 250, lfs: { oid: 'bb' } },
      ]),
      fetch: impl,
    });
    expect(plan.revision).toBe('resolved-sha');
    expect(plan.totalBytes).toBe(350);
    expect(plan.files).toHaveLength(2);
  });

  it('resolves against a file that is known to exist', async () => {
    // Resolving first would need a path the plan does not yet have.
    const { impl, seen } = fakeHead({ 'x-repo-commit': 'sha' });
    await planTransfer('org/m', '', ALL, '', {
      listFiles: fakeListFiles([{ path: 'deep/a.bin', size: 1 }]),
      fetch: impl,
    });
    expect(seen[0].url).toContain('/deep/a.bin');
  });

  it('honours a pinned revision when listing', async () => {
    let params: Record<string, unknown> = {};
    const { impl } = fakeHead({});
    const plan = await planTransfer('org/m', 'v1.2', ALL, '', {
      listFiles: fakeListFiles([{ path: 'a.bin', size: 1 }], (p) => {
        params = p;
      }),
      fetch: impl,
    });
    expect(params.revision).toBe('v1.2');
    expect(plan.revision).toBe('v1.2');
  });
});
