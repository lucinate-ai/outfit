import { describe, expect, it } from 'vitest';
import type { DeployConfig } from '../lambda/shared/deploy-config';
import { buildSeedUserData, type SeedEnv } from '../lambda/shared/seed';

const ENV: SeedEnv = {
  region: 'us-east-1',
  bucket: 'weights-bucket',
  instanceType: 'm5.xlarge',
  subnetId: 'subnet-1',
  securityGroupId: 'sg-1',
  instanceProfileArn: 'arn:aws:iam::1:instance-profile/seed',
  hfSecretArn: '',
  amiRoleTagKey: 'cloud-vm-llm:role',
  amiRoleTagValue: 'runtime-ami',
  amiRunnerTagKey: 'cloud-vm-llm:runner',
};

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

const VLLM: DeployConfig = {
  runner: 'vllm',
  modelId: 'Qwen/Qwen3.6-27B-FP8',
  quant: '',
  weightsPrefix: 'models/vllm/Qwen/Qwen3.6-27B-FP8/',
  contextSize: 32768,
  servedModelName: 'Qwen/Qwen3.6-27B-FP8',
  serveArgs: [],
  companions: {},
};

describe('buildSeedUserData', () => {
  it('downloads only the requested quant and normalises it to model.gguf for llamacpp', () => {
    const script = buildSeedUserData(LLAMACPP, ENV);
    expect(script).toContain("export QUANT='UD-Q6_K_XL'");
    expect(script).toContain('allow_patterns');
    expect(script).toContain('/opt/llm/model/model.gguf');
    // mmproj/projector files would otherwise be picked up as "the" GGUF.
    expect(script).toContain('mmproj');
  });

  it('fetches a named companion by exact filename and normalises it', () => {
    const script = buildSeedUserData(
      { ...LLAMACPP, companions: { draft: 'dflash-kquant.gguf' } },
      ENV,
    );
    // The quant glob cannot reach a drafter — `*kquant-dynamic*` does not
    // match `dflash-kquant.gguf` — so it must be its own allow_patterns entry.
    expect(script).toContain("'dflash-kquant.gguf'");
    expect(script).toContain('/opt/llm/model/draft.gguf');
    // Excluded from the main-GGUF pick, or it could be served as the model.
    expect(script).toContain("! -name 'dflash-kquant.gguf'");
    // A missing companion fails the seed with a named cause.
    expect(script).toContain('not found in $MODEL_ID');
  });

  it('still excludes projectors from the main GGUF when none is named', () => {
    // A projector is never the main model, and where the quant glob matches
    // one it sorts before the real weights.
    expect(buildSeedUserData(LLAMACPP, ENV)).toContain("! -iname '*mmproj*'");
  });

  it('emits no companion handling when none are named', () => {
    const script = buildSeedUserData(LLAMACPP, ENV);
    expect(script).not.toContain('draft.gguf');
    expect(script).not.toContain('COMPANION=');
  });

  it('orders companions deterministically', () => {
    const both = { draft: 'dflash-kquant.gguf', mmproj: 'mmproj-kquant.gguf' };
    expect(buildSeedUserData({ ...LLAMACPP, companions: both }, ENV)).toBe(
      buildSeedUserData(
        { ...LLAMACPP, companions: { mmproj: both.mmproj, draft: both.draft } },
        ENV,
      ),
    );
  });

  it('downloads the whole checkpoint for vllm', () => {
    const script = buildSeedUserData(VLLM, ENV);
    expect(script).toContain('snapshot_download');
    expect(script).not.toContain('allow_patterns');
    expect(script).not.toContain('model.gguf');
  });

  it('syncs to the config’s derived prefix and self-terminates', () => {
    const script = buildSeedUserData(LLAMACPP, ENV);
    expect(script).toContain(
      "aws s3 sync /opt/llm/model/ 's3://weights-bucket/models/llamacpp/unsloth/Qwen3.6-27B-MTP-GGUF/UD-Q6_K_XL/'",
    );
    expect(script.trimEnd().endsWith('shutdown -h now')).toBe(true);
  });

  it('fetches the HF token only when one is configured', () => {
    expect(buildSeedUserData(LLAMACPP, ENV)).not.toContain('get-secret-value');
    const withToken = buildSeedUserData(LLAMACPP, {
      ...ENV,
      hfSecretArn: 'arn:aws:secretsmanager:us-east-1:1:secret:hf',
    });
    expect(withToken).toContain("get-secret-value --secret-id 'arn:aws:secretsmanager:us-east-1:1:secret:hf'");
  });

  it('aborts on any failing step so a partial download is never synced', () => {
    expect(buildSeedUserData(LLAMACPP, ENV)).toContain('set -euxo pipefail');
  });
});
