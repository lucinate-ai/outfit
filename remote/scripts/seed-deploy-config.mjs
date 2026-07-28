#!/usr/bin/env node
// Seeds the deploy-config SSM parameter with CDK's initial config the FIRST
// time — while the parameter still holds the `unconfigured` placeholder CDK
// created it with. It never overwrites a real config: `outfit remote deploy`
// and manual edits own the parameter after the first seed, and a plain
// `cdk deploy` leaves it untouched (the parameter's CloudFormation value is a
// constant, so a deploy cannot clobber what is actually being served).
//
// Runs as the last step of `pnpm deploy`, reading the stack outputs that
// `cdk deploy --outputs-file` wrote.

import { existsSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { GetParameterCommand, PutParameterCommand, SSMClient } from '@aws-sdk/client-ssm';

// Keep in step with UNCONFIGURED_DEPLOY_CONFIG in lambda/shared/deploy-config.ts.
const UNCONFIGURED = 'unconfigured';

const repoRoot = dirname(dirname(fileURLToPath(import.meta.url)));
const outputsPath = process.argv[2] ?? join(repoRoot, 'cdk-outputs.json');

function fail(message) {
  console.error(`Error: ${message}`);
  process.exit(1);
}

if (!existsSync(outputsPath)) {
  fail(`${outputsPath} not found — run \`pnpm deploy\` first (it writes the stack outputs).`);
}
const allOutputs = JSON.parse(readFileSync(outputsPath, 'utf8'));
const stack = allOutputs['cloud-vm-llm'] ?? Object.values(allOutputs)[0];
if (!stack) {
  fail(`no stack outputs found in ${outputsPath}`);
}

const { DeployConfigParam, InitialDeployConfig, Region } = stack;
for (const [name, value] of Object.entries({ DeployConfigParam, InitialDeployConfig, Region })) {
  if (!value) {
    fail(`stack output ${name} is missing from ${outputsPath}`);
  }
}

function describe(raw) {
  try {
    const cfg = JSON.parse(raw);
    return `runner=${cfg.runner} model=${cfg.modelId} ctx=${cfg.contextSize}`;
  } catch {
    return raw;
  }
}

// Local checks first, so the common "nothing to do" paths need no AWS call.
//
// CDK only knows a runner's full serve config when it has the serve args.
// llama.cpp's args come from an Outfit (via `outfit remote deploy`), so CDK's
// initial config for it is incomplete — seeding it would produce a config that
// fails obscurely at wake. Leave the parameter unconfigured (it fails loudly
// with a clear message) for outfit/manual to set.
const initial = JSON.parse(InitialDeployConfig);
if (!Array.isArray(initial.serveArgs) || initial.serveArgs.length === 0) {
  console.log(
    `deploy-config left unconfigured: CDK has no complete serve config for runner=${initial.runner} ` +
      `(its serve args come from an Outfit). Set it with \`outfit remote deploy\` or a manual put.`,
  );
  process.exit(0);
}

const ssm = new SSMClient({ region: Region });

const current = await ssm
  .send(new GetParameterCommand({ Name: DeployConfigParam }))
  .then((r) => r.Parameter?.Value)
  .catch((err) => {
    if (err?.name === 'ParameterNotFound') {
      return undefined;
    }
    throw err;
  });

if (current && current.trim() !== UNCONFIGURED) {
  console.log(`deploy-config already set (${describe(current)}) — leaving it untouched.`);
  process.exit(0);
}

await ssm.send(
  new PutParameterCommand({
    Name: DeployConfigParam,
    Type: 'String',
    Overwrite: true,
    Value: InitialDeployConfig,
  }),
);
console.log(`Seeded deploy-config (${describe(InitialDeployConfig)}).`);
