import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { DeployConfig } from '../lambda/shared/deploy-config';
import type { SeedManifest } from '../lambda/shared/seed/contract';

/**
 * Presence is judged by the manifest the seeder writes last, so the fake bucket
 * holds manifests rather than a set of keys. The behaviours asserted here are
 * the same ones the key-probe implementation guaranteed — in particular that a
 * companion named after an earlier seed makes the weights count as absent.
 */
let manifests = new Map<string, SeedManifest>();
const fetched: string[] = [];

vi.mock('@aws-sdk/client-s3', () => {
  class NoSuchKey extends Error {
    constructor() {
      super('not found');
      this.name = 'NoSuchKey';
    }
  }
  return {
    S3Client: class {
      async send(cmd: { Key: string }) {
        fetched.push(cmd.Key);
        const manifest = manifests.get(cmd.Key);
        if (!manifest) {
          throw new NoSuchKey();
        }
        return { Body: { transformToString: async () => JSON.stringify(manifest) } };
      }
    },
    GetObjectCommand: class {
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

/** A manifest listing the given stored names. */
function manifest(prefix: string, ...paths: string[]): [string, SeedManifest] {
  return [
    `${prefix}_seed.json`,
    {
      modelId: BASE.modelId,
      revision: 'abc123',
      runner: BASE.runner,
      quant: BASE.quant,
      seededAt: '2026-08-17T00:00:00.000Z',
      seedId: 'llamacpp--m',
      seederVersion: '1.0.0',
      seederNodeVersion: 'v24.11.0',
      files: paths.map((path) => ({ path, size: 1, sha256: 'aa' })),
      totalBytes: paths.length,
    },
  ];
}

beforeEach(() => {
  fetched.length = 0;
  manifests = new Map();
});

describe('weightsPresent', () => {
  it('is true when the weights are seeded and no companion is named', async () => {
    manifests = new Map([manifest(PREFIX, 'model.gguf')]);
    expect(await weightsPresent('bucket', BASE)).toBe(true);
  });

  it('is false when nothing has been seeded', async () => {
    expect(await weightsPresent('bucket', BASE)).toBe(false);
  });

  it('is false when a named companion is missing, even though the model is there', async () => {
    // The regression this guards. The weights prefix is derived from
    // (runner, modelId, quant), so adding a drafter does not change it. Judging
    // presence by the main weights alone would report "present", skip the
    // re-seed, and start an instance whose --spec-draft-model points at a file
    // that was never synced — failing minutes later, with nothing in the deploy
    // output to explain it.
    manifests = new Map([manifest(PREFIX, 'model.gguf')]);
    const withDrafter = { ...BASE, companions: { draft: 'dflash-kquant.gguf' } };
    expect(await weightsPresent('bucket', withDrafter)).toBe(false);
  });

  it('is true once the companion has been seeded too', async () => {
    manifests = new Map([manifest(PREFIX, 'model.gguf', 'draft.gguf')]);
    const withDrafter = { ...BASE, companions: { draft: 'dflash-kquant.gguf' } };
    expect(await weightsPresent('bucket', withDrafter)).toBe(true);
  });

  it('checks every named companion, not just the first', async () => {
    manifests = new Map([manifest(PREFIX, 'model.gguf', 'draft.gguf')]);
    const both = { ...BASE, companions: { draft: 'd.gguf', mmproj: 'p.gguf' } };
    expect(await weightsPresent('bucket', both)).toBe(false);

    manifests = new Map([manifest(PREFIX, 'model.gguf', 'draft.gguf', 'mmproj.gguf')]);
    expect(await weightsPresent('bucket', both)).toBe(true);
  });

  it('reads one object rather than probing a key per expected file', async () => {
    manifests = new Map([manifest(PREFIX, 'model.gguf', 'draft.gguf')]);
    await weightsPresent('bucket', { ...BASE, companions: { draft: 'd.gguf' } });
    expect(fetched).toEqual([`${PREFIX}_seed.json`]);
  });

  it('treats weights with no manifest as absent', async () => {
    // Files may well be there — seeded by the pre-manifest script — but nothing
    // recorded that they are complete or which revision they came from.
    expect(await weightsPresent('bucket', BASE)).toBe(false);
  });

  it('treats an unparseable manifest as absent rather than wedging the deploy', async () => {
    manifests = new Map([
      [`${PREFIX}_seed.json`, { files: [] } as unknown as SeedManifest],
    ]);
    expect(await weightsPresent('bucket', BASE)).toBe(false);
  });

  it('works for a vllm checkpoint, which names no companions', async () => {
    const vllmPrefix = 'models/vllm/org/model/';
    manifests = new Map([manifest(vllmPrefix, 'config.json', 'model.safetensors')]);
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
