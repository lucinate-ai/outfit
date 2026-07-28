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
};

const VLLM: DeployConfig = {
  runner: 'vllm',
  modelId: 'Qwen/Qwen3.6-27B-FP8',
  quant: '',
  weightsPrefix: 'models/vllm/Qwen/Qwen3.6-27B-FP8/',
  contextSize: 32768,
  servedModelName: 'Qwen/Qwen3.6-27B-FP8',
  serveArgs: [],
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
