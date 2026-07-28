import * as cdk from 'aws-cdk-lib';
import { loadConfig } from '../lib/config';
import { ImageStack } from '../lib/image-stack';
import { LlmStack } from '../lib/llm-stack';

const app = new cdk.App();
const config = loadConfig(app);
const env = { region: config.region };

// Baked AMI (vLLM image + weights). Deploy rarely — only on a model/image
// change — and expect a ~20-40 min build. Decoupled from the runtime stack
// via the AMI-id SSM parameter, so there is no CloudFormation dependency.
new ImageStack(app, 'cloud-vm-llm-image', {
  config,
  env,
  description: 'Bakes the vLLM + weights AMI for cloud-vm-llm',
});

// Scale-to-zero runtime. Its start Lambda launches the baked AMI on demand,
// trying each g6e AZ for capacity; the idle Lambda terminates it.
new LlmStack(app, 'cloud-vm-llm', {
  config,
  env,
  description: 'Scale-to-zero self-hosted Qwen3.6-27B endpoint (vLLM on EC2)',
});
