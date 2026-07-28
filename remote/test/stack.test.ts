import * as cdk from 'aws-cdk-lib';
import { Match, Template } from 'aws-cdk-lib/assertions';
import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';
import { beforeAll, describe, expect, it } from 'vitest';
import { loadConfig } from '../lib/config';
import { ImageStack } from '../lib/image-stack';
import { LlmStack } from '../lib/llm-stack';
import { parseDeployConfig, UNCONFIGURED_DEPLOY_CONFIG } from '../lambda/shared/deploy-config';

const CONTEXT = { allowedCidr: '203.0.113.7/32', runner: 'vllm' };
// Keep tests hermetic: never read the developer's real .env at the repo root.
const NO_DOTENV = path.join(os.tmpdir(), 'cloud-vm-llm-no-such-env');

function runtimeTemplate(context: Record<string, unknown> = CONTEXT): Template {
  const app = new cdk.App({ context });
  const config = loadConfig(app, NO_DOTENV);
  return Template.fromStack(new LlmStack(app, 'test-runtime', { config, env: { region: config.region } }));
}

function imageTemplate(context: Record<string, unknown> = CONTEXT): Template {
  const app = new cdk.App({ context });
  const config = loadConfig(app, NO_DOTENV);
  return Template.fromStack(new ImageStack(app, 'test-image', { config, env: { region: config.region } }));
}

function tempDotEnv(content: string): string {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'cloud-vm-llm-test-'));
  const file = path.join(dir, '.env');
  fs.writeFileSync(file, content);
  return file;
}

describe('config', () => {
  it('requires allowedCidr', () => {
    expect(() => loadConfig(new cdk.App(), NO_DOTENV)).toThrow(/allowedCidr/);
  });

  it('rejects a non-CIDR allowedCidr', () => {
    const app = new cdk.App({ context: { allowedCidr: 'not-a-cidr', runner: 'vllm' } });
    expect(() => loadConfig(app, NO_DOTENV)).toThrow(/IPv4 CIDR/);
  });

  it('requires a runner to be chosen (no default)', () => {
    const app = new cdk.App({ context: { allowedCidr: '203.0.113.7/32' } });
    expect(() => loadConfig(app, NO_DOTENV)).toThrow(/runner/);
    const bad = new cdk.App({ context: { allowedCidr: '203.0.113.7/32', runner: 'tgi' } });
    expect(() => loadConfig(bad, NO_DOTENV)).toThrow(/runner/);
  });

  it('reads ALLOWED_CIDR and HF_TOKEN from a .env file', () => {
    const dotEnv = tempDotEnv('# comment\nALLOWED_CIDR=203.0.113.9/32\nHF_TOKEN=hf_test\n');
    const config = loadConfig(new cdk.App({ context: { runner: 'vllm' } }), dotEnv);
    expect(config.allowedCidr).toBe('203.0.113.9/32');
    expect(config.hfToken).toBe('hf_test');
  });

  it('applies defaults', () => {
    const config = loadConfig(new cdk.App({ context: CONTEXT }), NO_DOTENV);
    expect(config.runner).toBe('vllm');
    expect(config.region).toBe('us-east-1');
    expect(config.availabilityZones).toEqual(['us-east-1b', 'us-east-1c', 'us-east-1d', 'us-east-1e']);
    expect(config.modelId).toBe('Qwen/Qwen3.6-27B-FP8');
    expect(config.instanceType).toBe('g6e.xlarge');
    expect(config.builderInstanceType).toBe('m5.xlarge');
    expect(config.imageVolumeGb).toBe(80);
    expect(config.vllmVersion).toBe('0.26.0');
    expect(config.nvidiaDriverPackage).toContain('nvidia-driver');
    expect(config.maxRuntimeMinutes).toBe(240);
  });

  it('parses a comma-separated availabilityZones override', () => {
    const app = new cdk.App({ context: { ...CONTEXT, availabilityZones: 'us-east-1b, us-east-1c' } });
    expect(loadConfig(app, NO_DOTENV).availabilityZones).toEqual(['us-east-1b', 'us-east-1c']);
  });
});

describe('LlmStack (runtime)', () => {
  let template: Template;
  beforeAll(() => {
    template = runtimeTemplate();
  });

  it('holds no EC2 instance and no persistent EBS volume', () => {
    template.resourceCountIs('AWS::EC2::Instance', 0);
    template.resourceCountIs('AWS::EC2::Volume', 0);
  });

  it('creates one public subnet per configured AZ', () => {
    const subnets = template.findResources('AWS::EC2::Subnet');
    const azs = Object.values(subnets).map((s) => s.Properties.AvailabilityZone);
    expect(azs.sort()).toEqual(['us-east-1b', 'us-east-1c', 'us-east-1d', 'us-east-1e']);
  });

  it('opens only the vLLM port, only to the allowed CIDR', () => {
    template.hasResourceProperties('AWS::EC2::SecurityGroup', {
      SecurityGroupIngress: [
        Match.objectLike({ CidrIp: '203.0.113.7/32', FromPort: 8000, ToPort: 8000, IpProtocol: 'tcp' }),
      ],
    });
  });

  it('allocates an Elastic IP but does not attach it at deploy', () => {
    template.resourceCountIs('AWS::EC2::EIP', 1);
    for (const eip of Object.values(template.findResources('AWS::EC2::EIP'))) {
      expect(eip.Properties?.InstanceId).toBeUndefined();
    }
  });

  it('creates the start, stop and deploy Lambdas with IAM-authenticated function URLs', () => {
    template.resourceCountIs('AWS::Lambda::Function', 3);
    const urls = template.findResources('AWS::Lambda::Url');
    expect(Object.keys(urls)).toHaveLength(3);
    for (const url of Object.values(urls)) {
      expect(url.Properties.AuthType).toBe('AWS_IAM');
    }
  });

  it('has a placeholder deploy-config parameter and a deploy Lambda that writes it', () => {
    const params = template.findResources('AWS::SSM::Parameter');
    const deployParam = Object.values(params).find(
      (p) => p.Properties.Name === '/cloud-vm-llm/deploy-config',
    );
    expect(deployParam).toBeDefined();
    // The parameter's CloudFormation value is a constant placeholder so a later
    // `cdk deploy` never clobbers a real (outfit/manual) config. The cfg-derived
    // initial config is emitted as an output instead, which `pnpm deploy` seeds
    // over the placeholder the first time.
    expect(deployParam!.Properties.Value).toBe(UNCONFIGURED_DEPLOY_CONFIG);
    const initialOutput = Object.values(template.findOutputs('InitialDeployConfig'))[0];
    const initial = parseDeployConfig(initialOutput.Value);
    expect(initial.runner).toBe('vllm');
    expect(initial.contextSize).toBe(32768);
    expect(initial.serveArgs).toContain('--enforce-eager');
    expect(initial.serveArgs).toContain('qwen3_coder');

    const fns = template.findResources('AWS::Lambda::Function');
    const deploy = Object.values(fns).find((f) =>
      String(f.Properties.Description).includes('deploy-config'),
    );
    expect(deploy).toBeDefined();
    expect(Object.keys(template.findOutputs('DeployUrl'))).toHaveLength(1);
  });

  it('lets the deploy Lambda seed the weights, with PassRole scoped to the seed role', () => {
    const fns = template.findResources('AWS::Lambda::Function');
    const deploy = Object.values(fns).find((f) =>
      String(f.Properties.Description).includes('deploy-config'),
    );
    const env = deploy!.Properties.Environment.Variables;
    for (const key of [
      'WEIGHTS_BUCKET',
      'SEED_INSTANCE_TYPE',
      'SEED_SUBNET_ID',
      'SEED_SECURITY_GROUP_ID',
      'SEED_INSTANCE_PROFILE_ARN',
      'AMI_ROLE_TAG_KEY',
      'AMI_RUNNER_TAG_KEY',
    ]) {
      expect(env).toHaveProperty(key);
    }

    const passRole = allPolicyStatements(template).filter((s) =>
      [s.Action].flat().includes('iam:PassRole'),
    );
    expect(passRole.length).toBeGreaterThan(0);
    // A wildcard PassRole would let a caller hand EC2 any role in the account.
    for (const statement of passRole) {
      expect(JSON.stringify(statement.Resource)).not.toBe('"*"');
      expect(JSON.stringify(statement.Condition)).toContain('ec2.amazonaws.com');
    }
  });

  it('schedules the idle check every 5 minutes', () => {
    template.hasResourceProperties('AWS::Events::Rule', { ScheduleExpression: 'rate(5 minutes)' });
  });

  it('grants the start Lambda RunInstances, PassRole, AssociateAddress and DescribeImages', () => {
    const actions = allPolicyActions(template);
    expect(actions).toContain('ec2:RunInstances');
    expect(actions).toContain('ec2:AssociateAddress');
    expect(actions).toContain('iam:PassRole');
    expect(actions).toContain('ec2:DescribeImages');
  });

  it('tag-scopes the terminate permission', () => {
    const statements = allPolicyStatements(template);
    const terminate = statements.find((s) => [s.Action].flat().includes('ec2:TerminateInstances'));
    expect(terminate).toBeDefined();
    expect(JSON.stringify(terminate!.Condition)).toContain('cloud-vm-llm');
  });

  it('passes the AMI role tag, weights bucket and subnet list to the start Lambda', () => {
    const fns = template.findResources('AWS::Lambda::Function');
    const start = Object.values(fns).find((f) =>
      String(f.Properties.Description).includes('Launches the baked AMI'),
    );
    const env = start!.Properties.Environment.Variables;
    expect(env.AMI_ROLE_TAG_KEY).toBe('cloud-vm-llm:role');
    expect(env.WEIGHTS_BUCKET).toBeDefined();
    expect(env.DEPLOY_CONFIG_PARAM).toBeDefined();
    expect(env.SUBNET_IDS).toBeDefined();
  });

  it('creates an S3 weights bucket retained on destroy', () => {
    template.resourceCountIs('AWS::S3::Bucket', 1);
    const bucket = Object.values(template.findResources('AWS::S3::Bucket'))[0];
    expect(bucket.DeletionPolicy).toBe('Retain');
  });

  it('gives the runtime instance role read on the weights, and a separate seed profile', () => {
    // Two instance profiles: the runtime instance and the disposable seed one.
    template.resourceCountIs('AWS::IAM::InstanceProfile', 2);
    const actions = allPolicyActions(template);
    expect(actions.some((a) => a.startsWith('s3:GetObject'))).toBe(true);
    expect(actions.some((a) => a.startsWith('s3:PutObject'))).toBe(true);
  });

  it('creates the HF-token secret only when a token is configured', () => {
    // The runtime always has the API-key secret; the HF secret is conditional.
    template.resourceCountIs('AWS::SecretsManager::Secret', 1);
    runtimeTemplate({ ...CONTEXT, hfToken: 'hf_abc' }).resourceCountIs(
      'AWS::SecretsManager::Secret',
      2,
    );
  });

  it('outputs the outfit remote config, EIP and seed inputs', () => {
    expect(Object.keys(template.findOutputs('OutfitRemoteConfig'))).toHaveLength(1);
    expect(Object.keys(template.findOutputs('EipAddress'))).toHaveLength(1);
    expect(Object.keys(template.findOutputs('WeightsBucket'))).toHaveLength(1);
    expect(Object.keys(template.findOutputs('SeedInstanceProfileArn'))).toHaveLength(1);
  });
});

describe('ImageStack', () => {
  let template: Template;
  beforeAll(() => {
    template = imageTemplate();
  });

  it('defines a pipeline per runner and runs no build at deploy time', () => {
    template.resourceCountIs('AWS::ImageBuilder::Component', 2);
    template.resourceCountIs('AWS::ImageBuilder::ImageRecipe', 2);
    // One shared infrastructure config; a distribution + pipeline per runner.
    template.resourceCountIs('AWS::ImageBuilder::InfrastructureConfiguration', 1);
    template.resourceCountIs('AWS::ImageBuilder::DistributionConfiguration', 2);
    template.resourceCountIs('AWS::ImageBuilder::ImagePipeline', 2);
    // No Image resource — a bake never blocks or fails the stack deploy.
    template.resourceCountIs('AWS::ImageBuilder::Image', 0);
  });

  it('resizes the AMI root to the configured size on the right device', () => {
    template.hasResourceProperties('AWS::ImageBuilder::ImageRecipe', {
      BlockDeviceMappings: [
        Match.objectLike({ DeviceName: '/dev/sda1', Ebs: Match.objectLike({ VolumeSize: 80 }) }),
      ],
    });
  });

  it('parameterises each runner recipe (vLLM version, llama.cpp release, driver)', () => {
    const recipes = Object.values(template.findResources('AWS::ImageBuilder::ImageRecipe'));
    const params = recipes.map((r) => r.Properties.Components[0].Parameters);
    const names = (p: { Name: string }[]) => p.map((x) => x.Name);
    const vllm = params.find((p) => names(p).includes('VllmVersion'));
    const llamacpp = params.find((p) => names(p).includes('LlamacppRelease'));
    expect(vllm!.find((p: { Name: string }) => p.Name === 'VllmVersion').Value).toEqual(['0.26.0']);
    expect(llamacpp).toBeDefined();
    // Both need the driver.
    for (const p of params) {
      expect(names(p)).toContain('NvidiaDriverPackage');
    }
  });

  it('tags each AMI with the runtime role and its runner (no model tag)', () => {
    const dists = Object.values(
      template.findResources('AWS::ImageBuilder::DistributionConfiguration'),
    );
    const tagSets = dists.map(
      (d) => d.Properties.Distributions[0].AmiDistributionConfiguration.AmiTags,
    );
    for (const tags of tagSets) {
      expect(tags['cloud-vm-llm:role']).toBe('runtime-ami');
      expect(tags['cloud-vm-llm:model']).toBeUndefined();
    }
    expect(tagSets.map((t) => t['cloud-vm-llm:runner']).sort()).toEqual(['llamacpp', 'vllm']);
  });

  it('pins the builder VPC to the first configured AZ', () => {
    for (const subnet of Object.values(template.findResources('AWS::EC2::Subnet'))) {
      expect(subnet.Properties.AvailabilityZone).toBe('us-east-1b');
    }
  });

  it('bakes no secret and no model into the AMI (model-agnostic)', () => {
    template.resourceCountIs('AWS::SecretsManager::Secret', 0);
  });
});

function allPolicyStatements(
  template: Template,
): { Action: string | string[]; Resource?: unknown; Condition?: unknown }[] {
  return Object.values(template.findResources('AWS::IAM::Policy')).flatMap(
    (p) => p.Properties.PolicyDocument.Statement,
  );
}

function allPolicyActions(template: Template): string[] {
  return allPolicyStatements(template).flatMap((s) => [s.Action].flat());
}
