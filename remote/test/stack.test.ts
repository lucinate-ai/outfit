import * as cdk from 'aws-cdk-lib';
import { Match, Template } from 'aws-cdk-lib/assertions';
import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';
import { beforeAll, describe, expect, it } from 'vitest';
import { loadConfig } from '../lib/config';
import { ImageStack } from '../lib/image-stack';
import { LlmStack } from '../lib/llm-stack';
import {
  apiKeySecretName,
  baseUrlFor,
  deployConfigParam,
  environmentFrom,
  idleStateParam,
  isValidEnvironmentName,
} from '../lambda/shared/environments';

// Keep tests hermetic: never read the developer's real .env at the repo root.
const NO_DOTENV = path.join(os.tmpdir(), 'cloud-vm-llm-no-such-env');

function sharedTemplate(context: Record<string, unknown> = {}): Template {
  const app = new cdk.App({ context });
  const config = loadConfig(app, NO_DOTENV);
  return Template.fromStack(new LlmStack(app, 'test-runtime', { config, env: { region: config.region } }));
}

function imageTemplate(context: Record<string, unknown> = {}): Template {
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
  it('needs no per-environment settings (shared layer only)', () => {
    // allowedCidr, runner, model settings all moved to `outfit remote deploy`.
    expect(() => loadConfig(new cdk.App(), NO_DOTENV)).not.toThrow();
  });

  it('reads HF_TOKEN from a .env file', () => {
    const dotEnv = tempDotEnv('# comment\nHF_TOKEN=hf_test\n');
    const config = loadConfig(new cdk.App(), dotEnv);
    expect(config.hfToken).toBe('hf_test');
  });

  it('applies defaults', () => {
    const config = loadConfig(new cdk.App(), NO_DOTENV);
    expect(config.region).toBe('us-east-1');
    expect(config.availabilityZones).toEqual(['us-east-1b', 'us-east-1c', 'us-east-1d', 'us-east-1e']);
    expect(config.instanceType).toBe('g6e.xlarge');
    expect(config.builderInstanceType).toBe('m5.xlarge');
    expect(config.imageVolumeGb).toBe(80);
    expect(config.vllmVersion).toBe('0.26.0');
    expect(config.nvidiaDriverPackage).toContain('nvidia-driver');
    expect(config.maxRuntimeMinutes).toBe(240);
  });

  it('parses a comma-separated availabilityZones override', () => {
    const app = new cdk.App({ context: { availabilityZones: 'us-east-1b, us-east-1c' } });
    expect(loadConfig(app, NO_DOTENV).availabilityZones).toEqual(['us-east-1b', 'us-east-1c']);
  });
});

describe('environments (pure helpers)', () => {
  it('validates environment names', () => {
    expect(isValidEnvironmentName('default')).toBe(true);
    expect(isValidEnvironmentName('qwen3.6-27b-prod')).toBe(true);
    expect(isValidEnvironmentName('')).toBe(false);
    expect(isValidEnvironmentName('has space')).toBe(false);
    expect(isValidEnvironmentName('a/b')).toBe(false);
    expect(isValidEnvironmentName('-leading')).toBe(false);
  });

  it('resolves the environment from query, body, then default', () => {
    expect(environmentFrom({ env: 'prod' })).toBe('prod');
    expect(environmentFrom(undefined, 'staging')).toBe('staging');
    expect(environmentFrom({ env: 'prod' }, 'staging')).toBe('prod'); // query wins
    expect(environmentFrom(undefined)).toBe('default');
    expect(() => environmentFrom({ env: 'a/b' })).toThrow(/invalid environment/);
  });

  it('derives per-environment resource names', () => {
    expect(deployConfigParam('prod')).toBe('/cloud-vm-llm/prod/deploy-config');
    expect(idleStateParam('prod')).toBe('/cloud-vm-llm/prod/idle-state');
    expect(apiKeySecretName('prod')).toBe('cloud-vm-llm/prod/api-key');
    expect(baseUrlFor('203.0.113.10', 8000)).toBe('http://203.0.113.10:8000/v1');
  });
});

describe('LlmStack (shared layer)', () => {
  let template: Template;
  beforeAll(() => {
    template = sharedTemplate();
  });

  it('holds no EC2 instance and no persistent EBS volume', () => {
    template.resourceCountIs('AWS::EC2::Instance', 0);
    template.resourceCountIs('AWS::EC2::Volume', 0);
  });

  it('creates no per-environment resources: no EIP, no SSM state, no API key', () => {
    // These are created per environment by the deploy Lambda, not the stack.
    template.resourceCountIs('AWS::EC2::EIP', 0);
    template.resourceCountIs('AWS::SSM::Parameter', 0);
    template.resourceCountIs('AWS::SecretsManager::Secret', 0);
  });

  it('creates one public subnet per configured AZ', () => {
    const subnets = template.findResources('AWS::EC2::Subnet');
    const azs = Object.values(subnets).map((s) => s.Properties.AvailabilityZone);
    expect(azs.sort()).toEqual(['us-east-1b', 'us-east-1c', 'us-east-1d', 'us-east-1e']);
  });

  it('has only the seed security group, with no ingress', () => {
    // Environment security groups (per-env allowed CIDR) come from the deploy
    // Lambda; the stack ships only the egress-only seed SG.
    const groups = Object.values(template.findResources('AWS::EC2::SecurityGroup'));
    expect(groups).toHaveLength(1);
    expect(groups[0].Properties.SecurityGroupIngress).toBeUndefined();
  });

  it('creates the start, stop and deploy Lambdas with IAM-authenticated function URLs', () => {
    template.resourceCountIs('AWS::Lambda::Function', 3);
    const urls = template.findResources('AWS::Lambda::Url');
    expect(Object.keys(urls)).toHaveLength(3);
    for (const url of Object.values(urls)) {
      expect(url.Properties.AuthType).toBe('AWS_IAM');
    }
  });

  it('lets the deploy Lambda create environments (EIP, SG, key) and seed weights', () => {
    const fns = template.findResources('AWS::Lambda::Function');
    const deploy = Object.values(fns).find((f) =>
      String(f.Properties.Description).includes('Creates an environment'),
    );
    expect(deploy).toBeDefined();
    const env = deploy!.Properties.Environment.Variables;
    for (const key of [
      'VPC_ID',
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

    const actions = allPolicyActions(template);
    for (const action of [
      'ec2:AllocateAddress',
      'ec2:CreateSecurityGroup',
      'ec2:AuthorizeSecurityGroupIngress',
      'ec2:RevokeSecurityGroupIngress',
      'secretsmanager:CreateSecret',
    ]) {
      expect(actions).toContain(action);
    }
  });

  it('scopes PassRole to the stack roles, never a wildcard', () => {
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

  it('schedules the idle sweep every 5 minutes', () => {
    template.hasResourceProperties('AWS::Events::Rule', { ScheduleExpression: 'rate(5 minutes)' });
  });

  it('grants the start Lambda launch, EIP and per-env discovery permissions', () => {
    const actions = allPolicyActions(template);
    expect(actions).toContain('ec2:RunInstances');
    expect(actions).toContain('ec2:AssociateAddress');
    expect(actions).toContain('iam:PassRole');
    expect(actions).toContain('ec2:DescribeImages');
    expect(actions).toContain('ec2:DescribeAddresses');
    expect(actions).toContain('ec2:DescribeSecurityGroups');
  });

  it('scopes per-environment SSM and secret access to the cloud-vm-llm prefix', () => {
    const statements = allPolicyStatements(template);
    const ssmStatement = statements.find((s) => [s.Action].flat().includes('ssm:PutParameter'));
    expect(JSON.stringify(ssmStatement!.Resource)).toContain('parameter/cloud-vm-llm/*');
    const secretRead = statements.find((s) =>
      [s.Action].flat().includes('secretsmanager:GetSecretValue'),
    );
    expect(JSON.stringify(secretRead!.Resource)).toContain('secret:cloud-vm-llm/*');
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
      String(f.Properties.Description).includes('Launches an environment instance'),
    );
    const env = start!.Properties.Environment.Variables;
    expect(env.AMI_ROLE_TAG_KEY).toBe('cloud-vm-llm:role');
    expect(env.WEIGHTS_BUCKET).toBeDefined();
    expect(env.SUBNET_IDS).toBeDefined();
    // Per-environment values are found at wake by name, never baked in.
    expect(env.DEPLOY_CONFIG_PARAM).toBeUndefined();
    expect(env.EIP_ALLOCATION_ID).toBeUndefined();
    expect(env.API_KEY_SECRET_ARN).toBeUndefined();
    expect(env.BASE_URL).toBeUndefined();
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
    template.resourceCountIs('AWS::SecretsManager::Secret', 0);
    sharedTemplate({ hfToken: 'hf_abc' }).resourceCountIs('AWS::SecretsManager::Secret', 1);
  });

  it('outputs the discovery values and no per-environment address', () => {
    for (const name of [
      'OutfitRemoteConfig',
      'StartUrl',
      'StopUrl',
      'DeployUrl',
      'WeightsBucket',
      'VpcId',
      'SeedInstanceProfileArn',
    ]) {
      expect(Object.keys(template.findOutputs(name))).toHaveLength(1);
    }
    // An environment's base URL is its own EIP, allocated at deploy — the
    // shared stack has no address to output.
    expect(Object.keys(template.findOutputs('BaseUrl'))).toHaveLength(0);
    expect(Object.keys(template.findOutputs('EipAddress'))).toHaveLength(0);
    expect(Object.keys(template.findOutputs('InitialDeployConfig'))).toHaveLength(0);
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
