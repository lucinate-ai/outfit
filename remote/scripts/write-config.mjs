#!/usr/bin/env node
// Generates the gitignored `remote.json` from the CDK stack outputs that
// `pnpm deploy` writes (cdk deploy --outputs-file).
//
// remote.json holds everything about the deployment that is not a choice the
// user made: the start/stop/deploy Lambda URLs `outfit remote` drives, the
// region, and the endpoint's base URL (a stable Elastic IP, known at deploy
// time). The `Outfit` beside it is hand-written and never touched here.

import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

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

const { BaseUrl, OutfitRemoteConfig } = stack;
if (!OutfitRemoteConfig) {
  fail(`stack output OutfitRemoteConfig is missing from ${outputsPath}`);
}

const remote = JSON.parse(OutfitRemoteConfig);
// Outputs from a stack deployed before base_url joined OutfitRemoteConfig
// still carry the address as its own output.
if (!remote.base_url && BaseUrl) {
  remote.base_url = BaseUrl;
}
if (!remote.base_url) {
  fail(`neither OutfitRemoteConfig.base_url nor the BaseUrl output is present in ${outputsPath}`);
}

writeFileSync(join(repoRoot, 'remote.json'), `${JSON.stringify(remote, null, 2)}\n`);

console.log(`Wrote remote.json (base_url ${remote.base_url})`);
