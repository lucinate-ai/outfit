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

  it('excludes the projector companion as well as mmproj', () => {
    const files = ['model-UD-Q6_K_XL.gguf', 'projector-UD-Q6_K_XL.gguf'];
    expect(applySelection(files, runnerSpec('llamacpp').seedSelection(LLAMACPP))).toEqual([
      { path: 'model-UD-Q6_K_XL.gguf', storeAs: 'model.gguf' },
    ]);
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
