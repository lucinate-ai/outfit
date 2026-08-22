import { describe, expect, it } from 'vitest';
import {
  AUTO_GENERATION,
  freshGeneration,
  seedClientToken,
  seedIdFor,
} from '../lambda/shared/seed/identity';

describe('seed id', () => {
  it('is derived from what the seed produces, so the same weights converge', () => {
    expect(seedIdFor('vllm', 'Qwen/Qwen3-32B', '')).toBe(
      seedIdFor('vllm', 'Qwen/Qwen3-32B', ''),
    );
  });

  it('is readable rather than a digest', () => {
    expect(seedIdFor('vllm', 'Qwen/Qwen3-32B', '')).toBe('vllm--Qwen-Qwen3-32B');
  });

  it('separates runner, model and quant', () => {
    expect(seedIdFor('llamacpp', 'unsloth/Qwen3-32B-GGUF', 'UD-Q6_K_XL')).toBe(
      'llamacpp--unsloth-Qwen3-32B-GGUF--UD-Q6_K_XL',
    );
  });

  it('distinguishes two runners for one model', () => {
    expect(seedIdFor('vllm', 'Qwen/Qwen3-32B', '')).not.toBe(
      seedIdFor('llamacpp', 'Qwen/Qwen3-32B', ''),
    );
  });

  it('distinguishes two quants of one model', () => {
    expect(seedIdFor('llamacpp', 'u/m-GGUF', 'Q6')).not.toBe(seedIdFor('llamacpp', 'u/m-GGUF', 'Q4'));
  });

  it('does not confuse a quant with part of a model id', () => {
    // A single-hyphen join would make these two collide.
    expect(seedIdFor('llamacpp', 'u/m', 'Q6')).not.toBe(seedIdFor('llamacpp', 'u/m--Q6', ''));
  });

  it('produces an id legal as a log stream name and an EC2 tag value', () => {
    const id = seedIdFor('vllm', 'Org.Name/Model:v2 (beta)/weird', '');
    expect(id).not.toMatch(/[:*\s]/);
    expect(id.length).toBeLessThanOrEqual(120);
  });

  it('slugifies non-ASCII without emitting an empty segment', () => {
    const id = seedIdFor('vllm', 'orgé/modèle', '');
    expect(id).toBe('vllm--org-mod-le');
  });

  it('collapses runs of separators rather than stacking hyphens', () => {
    expect(seedIdFor('vllm', 'a///b', '')).toBe('vllm--a-b');
  });

  it('bounds an over-long id but keeps it unique', () => {
    const a = seedIdFor('vllm', `org/${'x'.repeat(300)}a`, '');
    const b = seedIdFor('vllm', `org/${'x'.repeat(300)}b`, '');
    expect(a.length).toBeLessThanOrEqual(120);
    expect(b.length).toBeLessThanOrEqual(120);
    expect(a).not.toBe(b);
  });
});

describe('client token', () => {
  it('is identical for concurrent ordinary starts, which is what dedupes them', () => {
    const id = seedIdFor('vllm', 'Qwen/Qwen3-32B', '');
    expect(seedClientToken(id, AUTO_GENERATION)).toBe(seedClientToken(id, AUTO_GENERATION));
  });

  it('differs between two seeds', () => {
    expect(seedClientToken(seedIdFor('vllm', 'a/b', ''), AUTO_GENERATION)).not.toBe(
      seedClientToken(seedIdFor('vllm', 'a/c', ''), AUTO_GENERATION),
    );
  });

  it('differs once a fresh generation is used, so a re-seed is not deduped', () => {
    const id = seedIdFor('vllm', 'Qwen/Qwen3-32B', '');
    expect(seedClientToken(id, freshGeneration())).not.toBe(seedClientToken(id, AUTO_GENERATION));
  });

  it('stays within the 64-character EC2 limit', () => {
    const id = seedIdFor('vllm', `org/${'x'.repeat(300)}`, '');
    expect(seedClientToken(id, freshGeneration()).length).toBeLessThanOrEqual(64);
  });

  it('hashes rather than truncates when over the limit, so two seeds cannot collide', () => {
    // Truncation would make these equal, silently returning the wrong instance.
    const long = 'x'.repeat(300);
    const a = seedClientToken(seedIdFor('vllm', `org/${long}a`, ''), AUTO_GENERATION);
    const b = seedClientToken(seedIdFor('vllm', `org/${long}b`, ''), AUTO_GENERATION);
    expect(a).not.toBe(b);
    expect(a.length).toBeLessThanOrEqual(64);
  });
});

describe('generations', () => {
  it('never collides with the constant used by ordinary starts', () => {
    expect(freshGeneration()).not.toBe(AUTO_GENERATION);
  });

  it('differs between two forced re-seeds in the same millisecond', () => {
    const now = new Date('2026-08-17T12:00:00.000Z');
    const generations = new Set(Array.from({ length: 50 }, () => freshGeneration(now)));
    expect(generations.size).toBeGreaterThan(1);
  });

  it('produces a token-safe string', () => {
    expect(freshGeneration()).toMatch(/^[0-9a-z-]+$/);
  });
});
