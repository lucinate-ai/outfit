import { describe, expect, it } from 'vitest';
import {
  parseDeployConfig,
  UNCONFIGURED_DEPLOY_CONFIG,
  weightsPrefixFor,
  type DeployConfig,
} from '../lambda/shared/deploy-config';

const VLLM: DeployConfig = {
  runner: 'vllm',
  modelId: 'Qwen/Qwen3.6-27B-FP8',
  quant: '',
  weightsPrefix: 'models/vllm/Qwen/Qwen3.6-27B-FP8/',
  contextSize: 32768,
  servedModelName: 'Qwen/Qwen3.6-27B-FP8',
  serveArgs: ['--enforce-eager', '--tool-call-parser', 'qwen3_coder'],
};

const LLAMACPP: DeployConfig = {
  runner: 'llamacpp',
  modelId: 'unsloth/Qwen3.6-27B-MTP-GGUF',
  quant: 'UD-Q6_K_XL',
  weightsPrefix: 'models/llamacpp/unsloth/Qwen3.6-27B-MTP-GGUF/UD-Q6_K_XL/',
  contextSize: 131072,
  servedModelName: 'qwen3.6-27b',
  serveArgs: ['-ngl', '99', '-fa', 'on', '--spec-type', 'mtp', '--jinja'],
};

describe('weightsPrefixFor', () => {
  it('includes the runner, and the quant only when there is one', () => {
    expect(weightsPrefixFor('llamacpp', 'unsloth/Qwen3.6-27B-MTP-GGUF', 'UD-Q6_K_XL')).toBe(
      'models/llamacpp/unsloth/Qwen3.6-27B-MTP-GGUF/UD-Q6_K_XL/',
    );
    expect(weightsPrefixFor('vllm', 'Qwen/Qwen3.6-27B-FP8', '')).toBe(
      'models/vllm/Qwen/Qwen3.6-27B-FP8/',
    );
  });

  it('keeps the two runners apart for the same model id', () => {
    expect(weightsPrefixFor('vllm', 'org/m', '')).not.toBe(weightsPrefixFor('llamacpp', 'org/m', ''));
  });
});

describe('parseDeployConfig', () => {
  it('derives weightsPrefix and ignores any sent on the wire', () => {
    const cfg = parseDeployConfig(
      JSON.stringify({ ...LLAMACPP, weightsPrefix: 'models/attacker-controlled/' }),
    );
    expect(cfg.weightsPrefix).toBe('models/llamacpp/unsloth/Qwen3.6-27B-MTP-GGUF/UD-Q6_K_XL/');
  });

  it('derives weightsPrefix when none is sent at all', () => {
    const { weightsPrefix, ...withoutPrefix } = LLAMACPP;
    expect(parseDeployConfig(JSON.stringify(withoutPrefix)).weightsPrefix).toBe(weightsPrefix);
  });

  it('round-trips a vllm config', () => {
    expect(parseDeployConfig(JSON.stringify(VLLM))).toEqual(VLLM);
  });

  it('round-trips a llamacpp config', () => {
    expect(parseDeployConfig(JSON.stringify(LLAMACPP))).toEqual(LLAMACPP);
  });

  it('defaults quant and serveArgs when omitted', () => {
    const cfg = parseDeployConfig(
      JSON.stringify({ ...VLLM, quant: undefined, serveArgs: undefined }),
    );
    expect(cfg.quant).toBe('');
    expect(cfg.serveArgs).toEqual([]);
  });

  it('rejects an empty or unconfigured config', () => {
    expect(() => parseDeployConfig('')).toThrow(/not set/);
    expect(() => parseDeployConfig(undefined)).toThrow(/not set/);
    // The placeholder CDK creates the parameter with must fail loudly, so a
    // wake before the config is seeded is a clear error, not a silent default.
    expect(() => parseDeployConfig(UNCONFIGURED_DEPLOY_CONFIG)).toThrow(/not set/);
  });

  it('rejects malformed JSON', () => {
    expect(() => parseDeployConfig('{not json')).toThrow(/not valid JSON/);
  });

  it('rejects a missing or unknown runner (no default)', () => {
    expect(() => parseDeployConfig(JSON.stringify({ ...VLLM, runner: undefined }))).toThrow(/runner/);
    expect(() => parseDeployConfig(JSON.stringify({ ...VLLM, runner: 'tgi' }))).toThrow(/runner/);
  });

  it('rejects a non-positive context size', () => {
    expect(() => parseDeployConfig(JSON.stringify({ ...VLLM, contextSize: 0 }))).toThrow(/contextSize/);
  });

  it('rejects a missing modelId', () => {
    expect(() => parseDeployConfig(JSON.stringify({ ...VLLM, modelId: '' }))).toThrow(/modelId/);
  });

  it('rejects non-string serveArgs', () => {
    expect(() => parseDeployConfig(JSON.stringify({ ...VLLM, serveArgs: [1, 2] }))).toThrow(/serveArgs/);
  });
});
