import { describe, expect, it } from 'vitest';
import type { DeployConfig } from '../lambda/shared/deploy-config';
import {
  buildSeedJob,
  buildSeedUserData,
  NODE_PACKAGE,
  RECORD_PATH,
  type SeedInfraEnv,
} from '../lambda/shared/seed/launch';

const INFRA: SeedInfraEnv = {
  region: 'us-east-1',
  bucket: 'weights-bucket',
  instanceType: 'c7g.large',
  subnetId: 'subnet-1',
  securityGroupId: 'sg-1',
  instanceProfileArn: 'arn:aws:iam::1:instance-profile/seed',
  hfSecretArn: 'arn:aws:secretsmanager:us-east-1:1:secret:hf',
  seederBucket: 'cdk-assets',
  seederKey: 'abc123/seed.mjs',
  maxSeedMinutes: 60,
  partSizeBytes: 64 * 1024 * 1024,
  partConcurrency: 8,
  partAttempts: 4,
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
  ...LLAMACPP,
  runner: 'vllm',
  modelId: 'Qwen/Qwen3.6-27B-FP8',
  quant: '',
  weightsPrefix: 'models/vllm/Qwen/Qwen3.6-27B-FP8/',
};

describe('the seed job spec', () => {
  it('derives the seed id from the weights, not from the request', () => {
    expect(buildSeedJob(VLLM, INFRA, '').seedId).toBe('vllm--Qwen-Qwen3.6-27B-FP8');
  });

  it('carries the runner selection rather than a script', () => {
    expect(buildSeedJob(LLAMACPP, INFRA, '').selection).toEqual({
      include: ['*UD-Q6_K_XL*.gguf'],
      exclude: ['*mmproj*', '*projector*'],
      expectSingle: 'model.gguf',
      companions: [],
    });
  });

  it('carries named companions in the job selection', () => {
    const withDrafter = { ...LLAMACPP, companions: { draft: 'dflash-kquant.gguf' } };
    expect(buildSeedJob(withDrafter, INFRA, '').selection.companions).toEqual([
      { storeAs: 'draft.gguf', file: 'dflash-kquant.gguf' },
    ]);
  });

  it('names the Hugging Face secret by ARN and never carries its value', () => {
    const job = buildSeedJob(VLLM, INFRA, '');
    expect(job.hfSecretArn).toBe(INFRA.hfSecretArn);
    expect(JSON.stringify(job)).not.toMatch(/hf_[A-Za-z0-9]/);
  });

  it('passes a revision pin through, and leaves it empty when unpinned', () => {
    expect(buildSeedJob(VLLM, INFRA, 'abc123').revision).toBe('abc123');
    expect(buildSeedJob(VLLM, INFRA, '').revision).toBe('');
  });
});

describe('the seed boot script', () => {
  const script = buildSeedUserData(buildSeedJob(VLLM, INFRA, ''), INFRA);

  it('arms the wall-clock cap before anything that could hang', () => {
    // Layer 2 must be the first thing that runs: a cap armed after a hang never
    // gets armed at all. Comments and the shebang do not execute, so the test
    // is about the first *command*.
    const commands = script.split('\n').filter((l) => l.trim() && !l.trimStart().startsWith('#'));
    expect(commands[0]).toBe('shutdown -h +60');
    expect(script.indexOf('shutdown -h +60')).toBeLessThan(script.indexOf('dnf install'));
  });

  it('shuts down on every exit path, not only on success', () => {
    // The defect this replaces: `set -e` aborted before the closing shutdown,
    // so a failed seed ran until somebody noticed.
    expect(script).toContain("trap 'sleep 10; shutdown -h now' EXIT");
  });

  it('does not use set -e, which would pre-empt the trap', () => {
    expect(script).not.toMatch(/set -e/);
    expect(script).not.toMatch(/set -euxo/);
  });

  it('does not use set -x, which traced the token into the boot log', () => {
    expect(script).not.toMatch(/set -x/);
  });

  it('never fetches the Hugging Face token in the shell', () => {
    // The regression this guards: the old script ran `HF_TOKEN=$(aws
    // secretsmanager get-secret-value …)` under `set -x`, and bash's xtrace
    // expands assignments from command substitution — so the token's value was
    // traced into the boot log and the console output. The seeder reads the
    // secret in-process instead.
    //
    // The secret's ARN does appear, inside the job spec. An ARN is a name, not
    // a credential; what must never appear is a shell reading its value.
    expect(script).not.toMatch(/get-secret-value/);
    expect(script).not.toMatch(/HF_TOKEN=/);
    expect(script).not.toMatch(/aws\s+secretsmanager/);
  });

  it('pins the Node major rather than taking the distribution default', () => {
    // The unversioned `nodejs` package on AL2023 is 18.
    expect(script).toContain(`dnf install -y ${NODE_PACKAGE} amazon-cloudwatch-agent`);
    expect(script).not.toMatch(/dnf install -y nodejs /);
  });

  it('fails the boot when the log shipper will not start', () => {
    // A seed that transfers correctly but reports nothing would be reaped as
    // stalled and diagnosed as a mystery.
    expect(script).toContain('SEED_BOOT_FAILED: the CloudWatch agent would not start');
  });

  it('fails the boot when the bundle cannot be fetched', () => {
    expect(script).toContain('SEED_BOOT_FAILED: could not fetch the seeder bundle');
  });

  it('ships records to the seed log group on a per-attempt stream', () => {
    expect(script).toContain('"log_group_name": "/cloud-vm-llm/seed"');
    expect(script).toContain('"log_stream_name": "vllm--Qwen-Qwen3.6-27B-FP8/{instance_id}"');
    expect(script).toContain(RECORD_PATH);
  });

  it('embeds a job spec that parses back to what was built', () => {
    const spec = script.match(/<<'SEEDJOB'\n([\s\S]*?)\nSEEDJOB/)?.[1];
    expect(spec).toBeDefined();
    expect(JSON.parse(spec!)).toEqual(buildSeedJob(VLLM, INFRA, ''));
  });

  it('quotes the heredocs so nothing in them is expanded by the shell', () => {
    // An unquoted heredoc would let $VARIABLES inside the JSON be substituted.
    expect(script).toContain("<<'SEEDJOB'");
    expect(script).toContain("<<'CWCONFIG'");
  });

  it('runs the seeder as the last thing before the trap fires', () => {
    const lines = script.trimEnd().split('\n');
    expect(lines[lines.length - 1]).toBe('node /opt/seed.mjs /opt/seed-job.json');
  });

  it('renders the same cap the sweep is configured with', () => {
    const tight = buildSeedUserData(buildSeedJob(VLLM, INFRA, ''), { ...INFRA, maxSeedMinutes: 25 });
    expect(tight).toContain('shutdown -h +25');
  });
});
