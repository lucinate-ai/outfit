#!/usr/bin/env node
// Generates the concrete, gitignored `Outfit` and `remote.json` from the CDK
// stack outputs that `pnpm deploy` writes (cdk deploy --outputs-file).
//
// The endpoint's base URL is a stable Elastic IP known at deploy time, so it
// lives in the Outfit's BASEURL as static config rather than being fetched
// from a runtime Lambda response. remote.json still carries the start/stop
// Lambda URLs that `outfit remote` drives.

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

const { BaseUrl, ModelId, OutfitRemoteConfig } = stack;
for (const [name, value] of Object.entries({ BaseUrl, ModelId, OutfitRemoteConfig })) {
  if (!value) {
    fail(`stack output ${name} is missing from ${outputsPath}`);
  }
}

const template = readFileSync(join(repoRoot, 'Outfit.example'), 'utf8');
const outfit = template.replaceAll('__BASEURL__', BaseUrl).replaceAll('__MODEL__', ModelId);
writeFileSync(join(repoRoot, 'Outfit'), outfit);

// OutfitRemoteConfig is already a JSON string; re-emit it pretty-printed.
const remote = `${JSON.stringify(JSON.parse(OutfitRemoteConfig), null, 2)}\n`;
writeFileSync(join(repoRoot, 'remote.json'), remote);

console.log(`Wrote Outfit (BASEURL ${BaseUrl}) and remote.json`);
