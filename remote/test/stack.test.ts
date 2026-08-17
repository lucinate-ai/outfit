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
  // Pin outfitVersion so the image assertions do not depend on the build
  // machine's git tags (its real default is the latest release tag). Callers
  // can still override via context.
  const app = new cdk.App({ context: { outfitVersion: '9.9.9', ...context } });
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
  it('needs no per-environment settings (control plane only)', () => {
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

  it('takes outfitVersion from context, overriding the git-tag default', () => {
    const app = new cdk.App({ context: { outfitVersion: '2.3.4' } });
    expect(loadConfig(app, NO_DOTENV).outfitVersion).toBe('2.3.4');
  });

  it('defaults outfitVersion to the latest git tag (or "" without one)', () => {
    // No hard-coded version: it is the latest release tag, or empty on a
    // checkout without tags — never a stale constant.
    const version = loadConfig(new cdk.App(), NO_DOTENV).outfitVersion;
    expect(version === '' || /^\d+\.\d+\.\d+/.test(version)).toBe(true);
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

  it('requires the environment: query or body, never a silent default', () => {
    expect(environmentFrom({ env: 'prod' })).toBe('prod');
    expect(environmentFrom(undefined, 'staging')).toBe('staging');
    expect(environmentFrom({ env: 'prod' }, 'staging')).toBe('prod'); // query wins
    // Defaults are a CLI affordance; the AWS API is explicit.
    expect(() => environmentFrom(undefined)).toThrow(/missing environment/);
    expect(() => environmentFrom({})).toThrow(/missing environment/);
    expect(() => environmentFrom({ env: 'a/b' })).toThrow(/invalid environment/);
  });

  it('derives per-environment resource names', () => {
    expect(deployConfigParam('prod')).toBe('/cloud-vm-llm/prod/deploy-config');
    expect(apiKeySecretName('prod')).toBe('cloud-vm-llm/prod/api-key');
    expect(baseUrlFor('203.0.113.10', 8000)).toBe('http://203.0.113.10:8000/v1');
  });
});

describe('LlmStack (control plane)', () => {
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

  it('creates the start, stop, deploy, stats, env, seed and update Lambdas with IAM-authenticated function URLs', () => {
    template.resourceCountIs('AWS::Lambda::Function', 7);
    const urls = template.findResources('AWS::Lambda::Url');
    expect(Object.keys(urls)).toHaveLength(7);
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
      // The seeder bundle and the shared lifetime cap replace the AMI tag
      // lookup: the seed runs on a stock image, so there is no AMI to find.
      'SEEDER_BUCKET',
      'SEEDER_KEY',
      'MAX_SEED_MINUTES',
    ]) {
      expect(env).toHaveProperty(key);
    }
    // The regression this guards: `s3assets.Asset` zips a directory path
    // before publishing, but the instance fetches SEEDER_KEY straight into
    // `node /opt/seed.mjs` — handed a zip, node fails immediately on the "PK"
    // magic bytes as a syntax error. A `.mjs` key confirms the asset was
    // published as the built file itself, not as a zipped directory.
    expect(env.SEEDER_KEY).toMatch(/\.mjs$/);

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

    // The regression this guards: AllocateAddress and CreateSecurityGroup both
    // tag inline via TagSpecifications, which needs ec2:CreateTags as its own
    // grant — deploying a fresh environment failed with "not authorized to
    // perform: ec2:CreateTags on resource: elastic-ip/*" because only the
    // resource-creation actions were granted.
    const policies = template.findResources('AWS::IAM::Policy');
    const deployPolicy = Object.values(policies).find((p) =>
      String(p.Properties.PolicyName).startsWith('DeployFnServiceRoleDefaultPolicy'),
    );
    expect(deployPolicy).toBeDefined();
    const createTags = (
      deployPolicy!.Properties.PolicyDocument.Statement as {
        Action: string | string[];
        Condition?: { StringEquals?: Record<string, unknown> };
      }[]
    ).find((s) => [s.Action].flat().includes('ec2:CreateTags'));
    expect(createTags).toBeDefined();
    expect(createTags!.Condition?.StringEquals?.['ec2:CreateAction']).toEqual([
      'AllocateAddress',
      'CreateSecurityGroup',
    ]);
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

  it('pre-creates per-engine, boot and seed log groups with the configured retention', () => {
    for (const name of ['/cloud-vm-llm/llamacpp', '/cloud-vm-llm/vllm', '/cloud-vm-llm/boot']) {
      template.hasResourceProperties('AWS::Logs::LogGroup', {
        LogGroupName: name,
        RetentionInDays: 1,
      });
    }
    // Seed records are kept longer than engine logs: they are the account's
    // record of what is in its weights bucket, and a failure is often noticed
    // the following day.
    template.hasResourceProperties('AWS::Logs::LogGroup', {
      LogGroupName: '/cloud-vm-llm/seed',
      RetentionInDays: 3,
    });
    // Ephemeral logs — the groups are destroyed with the stack, not retained.
    for (const g of Object.values(template.findResources('AWS::Logs::LogGroup'))) {
      expect(g.DeletionPolicy).toBe('Delete');
    }
  });

  it('pre-creates every control Lambda its own log group, so none keeps logs forever', () => {
    // The regression this guards: without an explicit log group, Lambda
    // auto-creates one with no retention policy at all — every invocation of
    // every control Lambda kept forever.
    for (const name of ['start', 'stop', 'deploy', 'seed', 'stats', 'env', 'update']) {
      template.hasResourceProperties('AWS::Logs::LogGroup', {
        LogGroupName: `/cloud-vm-llm/lambda/${name}`,
        RetentionInDays: 3,
      });
    }
    // Every Lambda function is wired to its pre-created group, not left to
    // fall back to an auto-created one.
    const fns = template.findResources('AWS::Lambda::Function');
    for (const fn of Object.values(fns)) {
      expect(fn.Properties.LoggingConfig?.LogGroup).toBeDefined();
    }
    // 4 instance-facing groups (llamacpp, vllm, boot, seed) + 7 Lambda groups.
    template.resourceCountIs('AWS::Logs::LogGroup', 11);
  });

  it('gives the seed Lambda its own function, cap and concurrency bound', () => {
    const fns = template.findResources('AWS::Lambda::Function');
    const seed = Object.values(fns).find((f) =>
      String(f.Properties.Description).includes('model weight seeds'),
    );
    expect(seed).toBeDefined();
    const env = seed!.Properties.Environment.Variables;
    expect(env.MAX_CONCURRENT_SEEDS).toBe('3');
    expect(env.MAX_SEED_MINUTES).toBe('60');
    expect(env.SEED_INSTANCE_TYPE).toBe('c7g.large');
    // Seeds are account-wide, so the seed Lambda takes no environment.
    expect(env).not.toHaveProperty('ENVIRONMENT');
  });

  it('gives the sweep the same lifetime cap the boot script is rendered with', () => {
    // Two layers of the termination guarantee read one config value, so they
    // cannot drift apart.
    const fns = template.findResources('AWS::Lambda::Function');
    const stop = Object.values(fns).find((f) =>
      String(f.Properties.Description).includes('stop retention'),
    );
    expect(stop!.Properties.Environment.Variables.MAX_SEED_MINUTES).toBe('60');
    expect(stop!.Properties.Environment.Variables.SEED_STALL_MINUTES).toBe('10');
  });

  it('publishes the seeder bundle so the instance needs no baked image', () => {
    // The seed runs on a stock AL2023 image; the program it runs is an asset.
    const seedFnRole = Object.values(template.findResources('AWS::IAM::Policy')).filter((p) =>
      JSON.stringify(p.Properties.PolicyDocument).includes('ami-amazon-linux-latest'),
    );
    expect(seedFnRole.length).toBeGreaterThan(0);
  });

  it('tag-scopes seed termination separately from endpoint termination', () => {
    // The two sweeps act on disjoint populations; neither Lambda's credentials
    // can terminate the other's instances.
    const statements = allPolicyStatements(template).filter((s) =>
      [s.Action].flat().includes('ec2:TerminateInstances'),
    );
    const tagValues = statements.map(
      (s) =>
        (s.Condition as { StringEquals?: Record<string, string> } | undefined)?.StringEquals?.[
          'ec2:ResourceTag/cloud-vm-llm'
        ],
    );
    expect(tagValues).toContain('endpoint');
    expect(tagValues).toContain('seed');
    // Never unscoped.
    expect(tagValues.every((v) => v === 'endpoint' || v === 'seed')).toBe(true);
  });

  it('honours a logRetentionDays override', () => {
    sharedTemplate({ logRetentionDays: 7 }).hasResourceProperties('AWS::Logs::LogGroup', {
      LogGroupName: '/cloud-vm-llm/boot',
      RetentionInDays: 7,
    });
  });

  it('honours a lambdaLogRetentionDays override, independent of the instance logs', () => {
    const overridden = sharedTemplate({ lambdaLogRetentionDays: 14 });
    overridden.hasResourceProperties('AWS::Logs::LogGroup', {
      LogGroupName: '/cloud-vm-llm/lambda/seed',
      RetentionInDays: 14,
    });
    // Unaffected by the Lambda-specific override.
    overridden.hasResourceProperties('AWS::Logs::LogGroup', {
      LogGroupName: '/cloud-vm-llm/boot',
      RetentionInDays: 1,
    });
  });

  it('grants the instance role scoped log shipping — stream + put, never CreateLogGroup', () => {
    const actions = allPolicyActions(template);
    expect(actions).toContain('logs:CreateLogStream');
    expect(actions).toContain('logs:PutLogEvents');
    expect(actions).not.toContain('logs:CreateLogGroup');
  });

  it('passes the engine and boot log group names to the start Lambda', () => {
    const fns = template.findResources('AWS::Lambda::Function');
    const start = Object.values(fns).find((f) =>
      String(f.Properties.Description).includes('Launches an environment instance'),
    );
    const env = start!.Properties.Environment.Variables;
    expect(env.LLAMACPP_LOG_GROUP).toBeDefined();
    expect(env.VLLM_LOG_GROUP).toBeDefined();
    expect(env.BOOT_LOG_GROUP).toBeDefined();
  });

  it('outputs the discovery values and no per-environment address', () => {
    for (const name of [
      'OutfitRemoteConfig',
      'StartUrl',
      'StopUrl',
      'DeployUrl',
      'StatsUrl',
      'EnvUrl',
      'WeightsBucket',
      'VpcId',
      'SeedInstanceProfileArn',
    ]) {
      expect(Object.keys(template.findOutputs(name))).toHaveLength(1);
    }
    // An environment's base URL is its own EIP, allocated at deploy — the
    // control-plane stack has no address to output.
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

  it('refuses to synth when outfitVersion is empty (no release tag)', () => {
    // Would otherwise bake a pipeline that curls a bogus /download/v/ URL and
    // fails 20 minutes in; fail at deploy instead.
    expect(() => imageTemplate({ outfitVersion: '' })).toThrow(/outfitVersion/);
  });

  it('defines a pipeline per runner and runs no build at deploy time', () => {
    template.resourceCountIs('AWS::ImageBuilder::Component', 2);
    template.resourceCountIs('AWS::ImageBuilder::ImageRecipe', 2);
    // One control plane config; a distribution + pipeline per runner.
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

  it('bakes the CloudWatch agent and the engine-log rotation into every runner', () => {
    const components = Object.values(template.findResources('AWS::ImageBuilder::Component'));
    expect(components.length).toBe(2);
    for (const c of components) {
      const data = c.Properties.Data as string;
      expect(data).toContain('amazon-cloudwatch-agent.deb');
      // The rotation is size-triggered and copytruncate (the engine holds the
      // append fd); only the daemon's engine log (under its pinned config dir)
      // is rotated, not the boot log.
      expect(data).toContain('/var/lib/outfit/daemon/engine.log');
      expect(data).toContain('copytruncate');
      expect(data).toContain('llm-logrotate.timer');
      expect(data).not.toContain('cloud-init-output.log');
    }
  });

  it('bakes outfit and the crash-nudge timer into every runner', () => {
    const components = Object.values(template.findResources('AWS::ImageBuilder::Component'));
    for (const c of components) {
      const data = c.Properties.Data as string;
      // The pinned release, checksum-verified, installed as /usr/local/bin/outfit.
      expect(data).toContain('OutfitVersion');
      expect(data).toContain('outfit_linux_amd64.tar.gz');
      expect(data).toContain('sha256sum -c');
      expect(data).toContain('install -m 0755 /tmp/outfit-dl/outfit /usr/local/bin/outfit');
      // An unresolved version fails the bake with a clear message rather than
      // curling a bogus /download/v/ URL.
      expect(data).toContain('test -n "$OUTFIT_VERSION"');
      // The nudge acts only on a crashed engine.
      expect(data).toContain('outfit-nudge.timer');
      expect(data).toContain('"state":"crashed"');
    }
  });

  it('puts the vllm venv entrypoint on the PATH for the daemon', () => {
    const components = Object.values(template.findResources('AWS::ImageBuilder::Component'));
    const vllm = components.find((c) => (c.Properties.Data as string).includes('VllmVersion'));
    expect(vllm).toBeDefined();
    expect(vllm!.Properties.Data as string).toContain(
      'ln -sf /opt/llm/venv/bin/vllm /usr/local/bin/vllm',
    );
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
