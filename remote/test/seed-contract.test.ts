import { describe, expect, it } from 'vitest';
import type { DeployConfig } from '../lambda/shared/deploy-config';
import { runnerSpec } from '../lambda/runners';
import {
  applySelection,
  globMatches,
  isTerminalPhase,
  manifestKey,
  matchesSelection,
  parseSeedRecord,
  seedLogStream,
} from '../lambda/shared/seed/contract';

const LLAMACPP: DeployConfig = {
  runner: 'llamacpp',
  modelId: 'unsloth/Qwen3.6-27B-MTP-GGUF',
  quant: 'UD-Q6_K_XL',
  weightsPrefix: 'models/llamacpp/unsloth/Qwen3.6-27B-MTP-GGUF/UD-Q6_K_XL/',
  contextSize: 131072,
  servedModelName: 'qwen3.6-27b',
  serveArgs: [],
  companions: {},
};

const VLLM: DeployConfig = { ...LLAMACPP, runner: 'vllm', quant: '' };

describe('glob matching', () => {
  it('treats * as any run of characters, path separators included', () => {
    expect(globMatches('a/b/c.gguf', '*.gguf')).toBe(true);
    expect(globMatches('a/b/c.gguf', 'a/*/c.gguf')).toBe(true);
  });

  it('is case-insensitive, because repository paths mix cases', () => {
    expect(globMatches('Model-MMPROJ.gguf', '*mmproj*')).toBe(true);
  });

  it('does not let regex metacharacters in a pattern match loosely', () => {
    // A naive implementation turns `.` into "any character" and matches.
    expect(globMatches('configXjson', 'config.json')).toBe(false);
    expect(globMatches('config.json', 'config.json')).toBe(true);
  });

  it('anchors the whole path', () => {
    expect(globMatches('not-config.json.bak', 'config.json')).toBe(false);
  });
});

describe('selection', () => {
  it('lets an exclusion beat an inclusion', () => {
    const selection = { include: ['*.gguf'], exclude: ['*mmproj*'] };
    expect(matchesSelection('model-Q6.gguf', selection)).toBe(true);
    expect(matchesSelection('mmproj-f16.gguf', selection)).toBe(false);
  });

  it('takes every file for vllm, storing each at its own path', () => {
    const files = ['config.json', 'model-00001-of-00002.safetensors', 'tokenizer.json'];
    expect(applySelection(files, runnerSpec('vllm').seedSelection(VLLM))).toEqual([
      { path: 'config.json', storeAs: 'config.json' },
      { path: 'model-00001-of-00002.safetensors', storeAs: 'model-00001-of-00002.safetensors' },
      { path: 'tokenizer.json', storeAs: 'tokenizer.json' },
    ]);
  });

  it('renames the single GGUF llama.cpp serves', () => {
    const files = [
      'README.md',
      'Qwen3.6-27B-UD-Q6_K_XL.gguf',
      'mmproj-Qwen3.6-27B-UD-Q6_K_XL.gguf',
      'Qwen3.6-27B-UD-Q4_K_M.gguf',
    ];
    expect(applySelection(files, runnerSpec('llamacpp').seedSelection(LLAMACPP))).toEqual([
      { path: 'Qwen3.6-27B-UD-Q6_K_XL.gguf', storeAs: 'model.gguf' },
    ]);
  });

  it('fails a split quant rather than shipping one shard', () => {
    // The regression this replaces: the old script warned and took the first.
    const files = [
      'Qwen3.6-27B-UD-Q6_K_XL-00001-of-00002.gguf',
      'Qwen3.6-27B-UD-Q6_K_XL-00002-of-00002.gguf',
    ];
    expect(() => applySelection(files, runnerSpec('llamacpp').seedSelection(LLAMACPP))).toThrow(
      /expected exactly one file .* 2 match/,
    );
  });

  it('names both candidates so the failure is actionable', () => {
    const files = ['a-UD-Q6_K_XL.gguf', 'b-UD-Q6_K_XL.gguf'];
    expect(() => applySelection(files, runnerSpec('llamacpp').seedSelection(LLAMACPP))).toThrow(
      /a-UD-Q6_K_XL\.gguf, b-UD-Q6_K_XL\.gguf/,
    );
  });

  it('fails when nothing matches, naming the patterns tried', () => {
    expect(() => applySelection(['README.md'], runnerSpec('llamacpp').seedSelection(LLAMACPP))).toThrow(
      /no files in the repository match/,
    );
  });

  it('takes a named companion by exact filename, stored under its role', () => {
    // The quant glob cannot reach a drafter, so it is selected by name.
    const files = ['Qwen3.6-27B-UD-Q6_K_XL.gguf', 'dflash-kquant.gguf'];
    expect(
      applySelection(files, {
        include: ['*UD-Q6_K_XL*.gguf'],
        exclude: ['*mmproj*'],
        expectSingle: 'model.gguf',
        companions: [{ storeAs: 'draft.gguf', file: 'dflash-kquant.gguf' }],
      }),
    ).toEqual([
      { path: 'Qwen3.6-27B-UD-Q6_K_XL.gguf', storeAs: 'model.gguf' },
      { path: 'dflash-kquant.gguf', storeAs: 'draft.gguf' },
    ]);
  });

  it('does not let a named companion compete for the main weights', () => {
    // Without claiming it first, expectSingle would see two candidates and
    // fail — the companion is excluded from the pick, not from the seed.
    const files = ['m-UD-Q6_K_XL.gguf', 'draft-UD-Q6_K_XL.gguf'];
    expect(
      applySelection(files, {
        include: ['*UD-Q6_K_XL*.gguf'],
        expectSingle: 'model.gguf',
        companions: [{ storeAs: 'draft.gguf', file: 'draft-UD-Q6_K_XL.gguf' }],
      }),
    ).toEqual([
      { path: 'm-UD-Q6_K_XL.gguf', storeAs: 'model.gguf' },
      { path: 'draft-UD-Q6_K_XL.gguf', storeAs: 'draft.gguf' },
    ]);
  });

  it('keeps a named projector even though projectors are excluded by default', () => {
    // The blanket exclusion stops an unnamed projector being served as the
    // model; naming it as a companion is how to keep it.
    const files = ['m-Q6.gguf', 'mmproj-Q6.gguf'];
    expect(
      applySelection(files, {
        include: ['*Q6*.gguf'],
        exclude: ['*mmproj*'],
        expectSingle: 'model.gguf',
        companions: [{ storeAs: 'mmproj.gguf', file: 'mmproj-Q6.gguf' }],
      }),
    ).toEqual([
      { path: 'm-Q6.gguf', storeAs: 'model.gguf' },
      { path: 'mmproj-Q6.gguf', storeAs: 'mmproj.gguf' },
    ]);
  });

  it('fails when a named companion is not in the repository', () => {
    // Better than an instance starting minutes later with a flag pointing at a
    // file that was never fetched.
    expect(() =>
      applySelection(['m-Q6.gguf'], {
        include: ['*Q6*.gguf'],
        expectSingle: 'model.gguf',
        companions: [{ storeAs: 'draft.gguf', file: 'missing.gguf' }],
      }),
    ).toThrow(/companion "missing\.gguf" \(stored as draft\.gguf\) is not in the repository/);
  });

  it('finds a companion that lives in a subdirectory', () => {
    expect(
      applySelection(['m-Q6.gguf', 'extra/draft.gguf'], {
        include: ['*Q6*.gguf'],
        expectSingle: 'model.gguf',
        companions: [{ storeAs: 'draft.gguf', file: 'draft.gguf' }],
      })[1],
    ).toEqual({ path: 'extra/draft.gguf', storeAs: 'draft.gguf' });
  });

  it('excludes the projector companion as well as mmproj', () => {
    const files = ['model-UD-Q6_K_XL.gguf', 'projector-UD-Q6_K_XL.gguf'];
    expect(applySelection(files, runnerSpec('llamacpp').seedSelection(LLAMACPP))).toEqual([
      { path: 'model-UD-Q6_K_XL.gguf', storeAs: 'model.gguf' },
    ]);
  });

  it('wires a deploy-config companion through the runner spec, ahead of the quant glob', () => {
    // The regression this guards. The quant glob cannot reach a drafter —
    // `*UD-Q6_K_XL*` does not match `dflash-kquant.gguf` — so it must be
    // claimed by its exact name rather than by pattern.
    const withDrafter = { ...LLAMACPP, companions: { draft: 'dflash-kquant.gguf' } };
    const files = ['Qwen3.6-27B-UD-Q6_K_XL.gguf', 'dflash-kquant.gguf'];
    expect(applySelection(files, runnerSpec('llamacpp').seedSelection(withDrafter))).toEqual([
      { path: 'Qwen3.6-27B-UD-Q6_K_XL.gguf', storeAs: 'model.gguf' },
      { path: 'dflash-kquant.gguf', storeAs: 'draft.gguf' },
    ]);
  });

  it('orders companions deterministically regardless of the deploy-config map key order', () => {
    const both = { draft: 'dflash-kquant.gguf', mmproj: 'mmproj-kquant.gguf' };
    const flipped = { mmproj: both.mmproj, draft: both.draft };
    const files = ['Qwen3.6-27B-UD-Q6_K_XL.gguf', 'dflash-kquant.gguf', 'mmproj-kquant.gguf'];
    expect(
      applySelection(files, runnerSpec('llamacpp').seedSelection({ ...LLAMACPP, companions: both })),
    ).toEqual(
      applySelection(files, runnerSpec('llamacpp').seedSelection({ ...LLAMACPP, companions: flipped })),
    );
  });
});

describe('manifest and stream naming', () => {
  it('puts the manifest under the weights prefix', () => {
    expect(manifestKey('models/vllm/Qwen/Qwen3-32B/')).toBe('models/vllm/Qwen/Qwen3-32B/_seed.json');
  });

  it('gives each attempt its own stream so a re-seed does not interleave', () => {
    expect(seedLogStream('vllm--Qwen-Qwen3-32B', 'i-abc')).toBe('vllm--Qwen-Qwen3-32B/i-abc');
    expect(seedLogStream('vllm--Qwen-Qwen3-32B', 'i-def')).not.toBe(
      seedLogStream('vllm--Qwen-Qwen3-32B', 'i-abc'),
    );
  });
});

describe('record parsing', () => {
  it('parses a well-formed record', () => {
    const record = parseSeedRecord(
      JSON.stringify({ SeedId: 'vllm--m', Runner: 'vllm', Phase: 'transferring', ProgressPercent: 12 }),
    );
    expect(record?.Phase).toBe('transferring');
    expect(record?.ProgressPercent).toBe(12);
  });

  it('returns null for a truncated line rather than throwing', () => {
    // The agent can ship a partial write; the status read must fall back to the
    // previous record instead of failing.
    expect(parseSeedRecord('{"SeedId":"vllm--m","Pha')).toBeNull();
  });

  it('rejects a record with no recognisable phase', () => {
    expect(parseSeedRecord(JSON.stringify({ SeedId: 'x', Phase: 'inventing' }))).toBeNull();
  });

  it('rejects a line that is valid JSON but not a record', () => {
    expect(parseSeedRecord('"just a string"')).toBeNull();
    expect(parseSeedRecord('[]')).toBeNull();
  });

  it('knows which phases are terminal', () => {
    expect(['succeeded', 'failed', 'stopped'].every(isTerminalPhase)).toBe(true);
    expect(['starting', 'resolving', 'transferring', 'finalising'].some(isTerminalPhase)).toBe(false);
  });
});
