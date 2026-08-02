import * as cdk from 'aws-cdk-lib';
import { loadConfig } from '../lib/config';
import { ImageStack } from '../lib/image-stack';
import { LlmStack } from '../lib/llm-stack';

const app = new cdk.App();
const config = loadConfig(app);
const env = { region: config.region };

// Per-runner AMI bake pipelines. Deploy rarely — only on an image change —
// and expect a ~20-40 min build per bake. Decoupled from the shared stack:
// the start Lambda finds AMIs by tag, so there is no CloudFormation
// dependency.
new ImageStack(app, 'cloud-vm-llm-image', {
  config,
  env,
  description: 'Bakes the per-runner runtime AMIs for cloud-vm-llm',
});

// The shared, account-level layer `outfit remote bootstrap` deploys once:
// weights bucket, VPC, roles, and the environment-aware lifecycle Lambdas.
// Environments (EIP + instance each) are created on it by `outfit remote
// deploy`; the scheduled idle sweep covers them all.
new LlmStack(app, 'cloud-vm-llm', {
  config,
  env,
  description: 'Shared scale-to-zero LLM layer (lifecycle Lambdas, weights bucket, VPC)',
});
