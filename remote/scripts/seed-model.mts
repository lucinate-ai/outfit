#!/usr/bin/env node
// Manual model seed: launches a disposable EC2 instance that downloads the
// model from Hugging Face and syncs it to the runtime's S3 bucket, then
// terminates itself. WHAT to seed (runner, model id, quant, S3 prefix) comes
// from the deploy-config SSM parameter — set it with `outfit remote deploy`
// (or the deploy Lambda) first.
//
// The deploy Lambda now seeds automatically when a posted config names weights
// that are not in S3 (see lambda/shared/seed.ts), so this script is only needed
// to force a re-seed of weights that are already there. Infra (bucket, seed profile/subnet/sg) comes
// from cdk-outputs.json (`pnpm run deploy`). The seed always runs on the vLLM AMI
// because that one carries a Python venv with huggingface_hub; it can fetch
// either safetensors (vLLM) or a single GGUF file (llama.cpp).

import { existsSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { DescribeImagesCommand, EC2Client, RunInstancesCommand } from '@aws-sdk/client-ec2';
import { GetParameterCommand, SSMClient } from '@aws-sdk/client-ssm';
import { runnerSpec } from '../lambda/runners/index.js';
import { parseDeployConfig } from '../lambda/shared/deploy-config.js';

const AMI_ROLE_TAG_KEY = 'cloud-vm-llm:role';
const AMI_ROLE_TAG_VALUE = 'runtime-ami';
const AMI_RUNNER_TAG_KEY = 'cloud-vm-llm:runner';
// Deploy-config is per environment; name one as the first argument.
const ENVIRONMENT = process.argv[2] ?? 'default';
const DEPLOY_CONFIG_PARAM = `/cloud-vm-llm/${ENVIRONMENT}/deploy-config`;

const repoRoot = dirname(dirname(fileURLToPath(import.meta.url)));

function fail(message) {
  console.error(`Error: ${message}`);
  process.exit(1);
}

const outputsPath = join(repoRoot, 'cdk-outputs.json');
if (!existsSync(outputsPath)) {
  fail('cdk-outputs.json not found — run `pnpm run deploy` first.');
}
const stack = JSON.parse(readFileSync(outputsPath, 'utf8'))['cloud-vm-llm'];
if (!stack) {
  fail('no cloud-vm-llm outputs in cdk-outputs.json');
}
const {
  Region: region,
  WeightsBucket: bucket,
  SeedInstanceProfileArn: profileArn,
  SeedInstanceType: instanceType,
  SeedSubnetId: subnetId,
  SeedSecurityGroupId: securityGroupId,
  HfTokenSecretArn: hfSecretArn,
} = stack;

// What to seed comes from the deploy-config parameter, not the stack outputs.
const ssm = new SSMClient({ region });
const param = await ssm.send(new GetParameterCommand({ Name: DEPLOY_CONFIG_PARAM }));
// Validated by the same parser the deploy Lambda uses, so this path cannot
// accept a config the automatic one would reject (or vice versa).
let deploy;
try {
  deploy = parseDeployConfig(param.Parameter?.Value);
} catch (err) {
  fail(`${(err as Error).message} — set it via \`outfit remote deploy\` first.`);
}
const { runner, modelId, quant, weightsPrefix, companions } = deploy!;

const ec2 = new EC2Client({ region });

// The seed runs on the vLLM AMI (it has the Python venv + huggingface_hub),
// regardless of which runner the weights are for.
async function newestAmi(filters) {
  const r = await ec2.send(new DescribeImagesCommand({ Owners: ['self'], Filters: filters }));
  return (r.Images ?? []).sort((a, b) =>
    (b.CreationDate ?? '').localeCompare(a.CreationDate ?? ''),
  )[0];
}
const roleFilter = { Name: `tag:${AMI_ROLE_TAG_KEY}`, Values: [AMI_ROLE_TAG_VALUE] };
const stateFilter = { Name: 'state', Values: ['available'] };
let image = await newestAmi([
  roleFilter,
  { Name: `tag:${AMI_RUNNER_TAG_KEY}`, Values: ['vllm'] },
  stateFilter,
]);
if (!image) {
  // Transition fallback: older vLLM AMIs predate the runner tag. The seed only
  // needs huggingface_hub + the AWS CLI, which any runtime AMI carries.
  image = await newestAmi([roleFilter, stateFilter]);
}
if (!image) {
  fail('no baked runtime AMI found — run `pnpm bake vllm` and wait for it to finish.');
}

const header = `#!/bin/bash
set -euxo pipefail
HF_TOKEN=""
${hfSecretArn ? `HF_TOKEN=$(aws secretsmanager get-secret-value --secret-id '${hfSecretArn}' --region '${region}' --query SecretString --output text)` : ''}
export HF_TOKEN
export MODEL_ID='${modelId}'
mkdir -p /opt/llm/model
`;

// The download fragment comes from the runner's spec — the same one the deploy
// Lambda's seed uses. Restating it here is what let the two drift.
const download = runnerSpec(runner).seedDownload(deploy!);

const userData = `${header}${download}aws s3 sync /opt/llm/model/ 's3://${bucket}/${weightsPrefix}' --region '${region}'
shutdown -h now
`;

const run = await ec2.send(
  new RunInstancesCommand({
    ImageId: image.ImageId,
    InstanceType: instanceType,
    MinCount: 1,
    MaxCount: 1,
    SubnetId: subnetId,
    SecurityGroupIds: [securityGroupId],
    IamInstanceProfile: { Arn: profileArn },
    UserData: Buffer.from(userData).toString('base64'),
    MetadataOptions: { HttpTokens: 'required' },
    // Self-terminate when the userdata runs `shutdown -h now`.
    InstanceInitiatedShutdownBehavior: 'terminate',
    TagSpecifications: [
      { ResourceType: 'instance', Tags: [{ Key: 'Name', Value: 'cloud-vm-llm-seed' }] },
    ],
  }),
);
const instanceId = run.Instances?.[0]?.InstanceId;

console.log(`Seeding ${runner} model ${modelId}${quant ? `:${quant}` : ''} -> s3://${bucket}/${weightsPrefix}`);
for (const [role, file] of Object.entries(companions)) {
  console.log(`  + ${role} companion: ${file}`);
}
console.log(`Seed instance: ${instanceId} (${instanceType}, from ${image.ImageId})`);
console.log('It downloads from Hugging Face, syncs to S3, then terminates itself (~15-20 min).');
console.log('\nWatch it:');
console.log(`  aws ssm start-session --target ${instanceId}   # then: sudo tail -f /var/log/cloud-init-output.log`);
console.log('Done when it appears in S3:');
console.log(`  aws s3 ls s3://${bucket}/${weightsPrefix} --region ${region}`);
