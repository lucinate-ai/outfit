import { beforeAll, describe, expect, it } from 'vitest';
import type { DeployConfig } from '../lambda/shared/deploy-config';

// The start Lambda reads its wiring from the environment at import time, so
// stub every required variable before the module loads.
const LAMBDA_ENV = {
  TAG_KEY: 'cloud-vm-llm:managed',
  TAG_VALUE: 'true',
  ENGINE_PORT: '8000',
  AMI_ROLE_TAG_KEY: 'cloud-vm-llm:role',
  AMI_ROLE_TAG_VALUE: 'runtime-ami',
  AMI_RUNNER_TAG_KEY: 'cloud-vm-llm:runner',
  INSTANCE_TYPE: 'g6e.xlarge',
  SUBNET_IDS: 'subnet-test',
  // A single-digit account id: the sanctioned fake that the cloud-identifier
  // guard's 12-digit patterns can never mistake for a real ARN.
  INSTANCE_PROFILE_ARN: 'arn:aws:iam::0:instance-profile/test',
  WEIGHTS_BUCKET: 'test-bucket',
  AWS_REGION: 'us-east-1',
  BOOT_LOG_GROUP: '/test/boot',
  LLAMACPP_LOG_GROUP: '/test/llamacpp',
  VLLM_LOG_GROUP: '/test/vllm',
};

let buildInferenceUserData: (env: string, cfg: DeployConfig) => string;

beforeAll(async () => {
  Object.assign(process.env, LAMBDA_ENV);
  ({ buildInferenceUserData } = await import('../lambda/start/index'));
});

const LLAMACPP: DeployConfig = {
  runner: 'llamacpp',
  modelId: 'org/model',
  quant: 'Q4_K_M',
  weightsPrefix: 'llamacpp/org/model/Q4_K_M',
  contextSize: 32768,
  servedModelName: 'friendly',
  serveArgs: ['--flash-attn'],
  companions: {},
};

const VLLM: DeployConfig = {
  ...LLAMACPP,
  runner: 'vllm',
  serveArgs: ['--kv-cache-dtype', 'fp8'],
};

describe('buildInferenceUserData', () => {
  it('boots the engine through the outfit daemon, not a per-runner unit', () => {
    for (const cfg of [LLAMACPP, VLLM]) {
      const data = buildInferenceUserData('prod', cfg);
      expect(data).toContain('outfit-daemon.service');
      expect(data).toContain('outfit daemon --api-addr 127.0.0.1:4242');
      expect(data).toContain('systemctl enable --now outfit-daemon.service');
      expect(data).toContain('outfit-nudge.timer');
      expect(data).not.toContain('llama-server.service');
      expect(data).not.toContain('vllm.service');
      // The first start is an explicit API call, retried until the daemon
      // answers; 409 (already running) also counts.
      expect(data).toContain('-X POST http://127.0.0.1:4242/v1/start');
      expect(data).toContain('"$code" = "409"');
    }
  });

  it('pins the daemon config dir so it does not depend on $HOME', () => {
    for (const cfg of [LLAMACPP, VLLM]) {
      const data = buildInferenceUserData('prod', cfg);
      // The unit sets OUTFIT_CONFIG_DIR and the boot writes the deploy config
      // under it — the same location the daemon then reads.
      expect(data).toContain('Environment=OUTFIT_CONFIG_DIR=/var/lib/outfit');
      expect(data).toContain('/var/lib/outfit/daemon/deploy-config.json');
      expect(data).not.toContain('/root/.config/outfit');
    }
  });

  it('renders the daemon deploy config with cloud-owned settings resolved', () => {
    const data = buildInferenceUserData('prod', LLAMACPP);
    expect(data).toContain('deploy-config.json');
    expect(data).toContain('"runner": "llamacpp"');
    // The synced local weights file is the model — path-shaped, so the daemon
    // builds --model rather than an HF download.
    expect(data).toContain('"modelId": "/opt/llm/model/model.gguf"');
    expect(data).toContain('"servedModelName": "friendly"');
    expect(data).toContain('"contextSize": 32768');
    for (const arg of ['"--host"', '"0.0.0.0"', '"--port"', '"8000"', '"--api-key-file"', '"/etc/llm/api-key"', '"--flash-attn"']) {
      expect(data).toContain(arg);
    }
    // The daemon switches the metrics endpoint on itself.
    expect(data).not.toContain('--metrics');
  });

  it('names a companion drafter at its synced path', () => {
    const data = buildInferenceUserData('prod', {
      ...LLAMACPP,
      companions: { draft: 'dflash-kquant.gguf' },
    });
    // The deployment owns *where* the drafter is; the source filename never
    // reaches the instance, only the normalised name.
    expect(data).toContain('"--spec-draft-model"');
    expect(data).toContain('"/opt/llm/model/draft.gguf"');
    expect(data).not.toContain('dflash-kquant.gguf');
  });

  it('leaves the command unchanged when no companions are named', () => {
    // The compatibility guarantee: a pre-companion deployment must produce
    // byte-identical boot data.
    const { companions, ...preCompanions } = LLAMACPP;
    expect(buildInferenceUserData('prod', preCompanions as DeployConfig)).toBe(
      buildInferenceUserData('prod', LLAMACPP),
    );
    expect(buildInferenceUserData('prod', LLAMACPP)).not.toContain('--spec-draft-model');
  });

  it('ignores a companion for vllm rather than failing', () => {
    const data = buildInferenceUserData('prod', {
      ...VLLM,
      companions: { draft: 'dflash-kquant.gguf' },
    });
    expect(data).not.toContain('--spec-draft-model');
    expect(data).toContain('"modelId": "/opt/llm/model"');
  });

  it('omits parallel from the rendered deploy config when unset', () => {
    const data = buildInferenceUserData('prod', LLAMACPP);
    expect(data).not.toContain('"parallel"');
  });

  it('carries parallel through to the rendered deploy config when set', () => {
    const data = buildInferenceUserData('prod', { ...LLAMACPP, parallel: 2 });
    expect(data).toContain('"parallel": 2');
  });

  it('renders the vllm deploy config with the model dir and env-file key delivery', () => {
    const data = buildInferenceUserData('prod', VLLM);
    expect(data).toContain('"modelId": "/opt/llm/model"');
    expect(data).toContain('EnvironmentFile=/etc/vllm.env');
    expect(data).toContain('VLLM_API_KEY=$API_KEY');
    for (const arg of ['"--gpu-memory-utilization"', '"0.92"', '"--kv-cache-dtype"', '"fp8"']) {
      expect(data).toContain(arg);
    }
  });

  it('tails the daemon engine log into the runner log group', () => {
    const data = buildInferenceUserData('prod', LLAMACPP);
    expect(data).toContain('/var/lib/outfit/daemon/engine.log');
    expect(data).toContain('/test/llamacpp');
    expect(data).not.toContain('/var/log/llm/llama-server.log');
  });
});
