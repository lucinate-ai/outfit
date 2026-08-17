import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { DeployConfig } from '../lambda/shared/deploy-config';

// The set of keys the fake bucket contains. Each test sets it.
let present = new Set<string>();
const headed: string[] = [];

vi.mock('@aws-sdk/client-s3', () => {
  class NotFound extends Error {
    constructor() {
      super('not found');
      this.name = 'NotFound';
    }
  }
  return {
    S3Client: class {
      async send(cmd: { Key: string }) {
        headed.push(cmd.Key);
        if (!present.has(cmd.Key)) {
          throw new NotFound();
        }
        return {};
      }
    },
    HeadObjectCommand: class {
      Key: string;
      constructor(input: { Bucket: string; Key: string }) {
        this.Key = input.Key;
      }
    },
  };
});

// Imported after the mock is registered, so the module picks up the fake S3.
let weightsPresent: (bucket: string, cfg: DeployConfig) => Promise<boolean>;

beforeAll(async () => {
  ({ weightsPresent } = await import('../lambda/shared/seed'));
});

const PREFIX = 'models/llamacpp/meta-models/Muse-Glimmer-30B-GGUF/kquant-dynamic/';

const BASE: DeployConfig = {
  runner: 'llamacpp',
  modelId: 'meta-models/Muse-Glimmer-30B-GGUF',
  quant: 'kquant-dynamic',
  weightsPrefix: PREFIX,
  contextSize: 524288,
  servedModelName: 'muse-glimmer-30b',
  serveArgs: [],
  companions: {},
};

beforeEach(() => {
  headed.length = 0;
});

describe('weightsPresent', () => {
  it('is true when the main weights are there and no companion is named', async () => {
    present = new Set([`${PREFIX}model.gguf`]);
    expect(await weightsPresent('bucket', BASE)).toBe(true);
  });

  it('is false when the main weights are absent', async () => {
    present = new Set();
    expect(await weightsPresent('bucket', BASE)).toBe(false);
  });

  it('is false when a named companion is missing, even though the model is there', async () => {
    // The regression this guards. The weights prefix is derived from
    // (runner, modelId, quant), so adding a drafter does not change it. If
    // only the sentinel were checked, this would report "present", skip the
    // re-seed, and start an instance whose --spec-draft-model points at a
    // file that was never synced — failing minutes later, with nothing in the
    // deploy output to explain it.
    present = new Set([`${PREFIX}model.gguf`]);
    const withDrafter = { ...BASE, companions: { draft: 'dflash-kquant.gguf' } };
    expect(await weightsPresent('bucket', withDrafter)).toBe(false);
    expect(headed).toContain(`${PREFIX}draft.gguf`);
  });

  it('is true once the companion has been seeded too', async () => {
    present = new Set([`${PREFIX}model.gguf`, `${PREFIX}draft.gguf`]);
    const withDrafter = { ...BASE, companions: { draft: 'dflash-kquant.gguf' } };
    expect(await weightsPresent('bucket', withDrafter)).toBe(true);
  });

  it('checks the checkpoint sentinel for vllm', async () => {
    const vllmPrefix = 'models/vllm/org/model/';
    present = new Set([`${vllmPrefix}config.json`]);
    expect(
      await weightsPresent('bucket', {
        ...BASE,
        runner: 'vllm',
        quant: '',
        weightsPrefix: vllmPrefix,
      }),
    ).toBe(true);
  });
});
