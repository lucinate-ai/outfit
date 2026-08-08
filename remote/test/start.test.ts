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
    expect(data).toContain('/root/.config/outfit/daemon/engine.log');
    expect(data).toContain('/test/llamacpp');
    expect(data).not.toContain('/var/log/llm/llama-server.log');
  });
});
