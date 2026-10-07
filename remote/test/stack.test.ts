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
  it('needs no per-environment settings (control plane only)', () => {
    // allowedCidr, runner, model settings all moved to `spinloop remote deploy`.
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
  let cliStatements: Statement[];
  beforeAll(() => {
    template = sharedTemplate();
  });
  // The control-plane CLI user's inline policy statements (see the
  // control-plane-user tests below).
  beforeAll(() => {
    cliStatements = statementsForResource(template, 'AWS::IAM::User', (_, u) =>
      (u as { Properties: { UserName: string } }).Properties.UserName === 'cloud-vm-llm-remote-cli',
    );
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

  it('creates the start, stop, deploy, stats, env, seed, update and schedule Lambdas with IAM-authenticated function URLs', () => {
    template.resourceCountIs('AWS::Lambda::Function', 8);
    const urls = template.findResources('AWS::Lambda::Url');
    expect(Object.keys(urls)).toHaveLength(8);
    for (const url of Object.values(urls)) {
      expect(url.Properties.AuthType).toBe('AWS_IAM');
    }
  });

  it('creates the CLI user with one managed policy and no inline one', () => {
    const users = template.findResources('AWS::IAM::User') as Record<string, any>;
    const user = Object.values(users).find((u) => u.Properties.UserName === 'cloud-vm-llm-remote-cli');
    expect(user).toBeDefined();
    expect(user.Properties.ManagedPolicyArns).toHaveLength(1);
    expect(user.Properties.Policies).toBeUndefined();
    expect(cliStatements.length).toBeGreaterThan(0);
  });

  it('grants the CLI user invoke-url permission on all eight control functions, and nothing else in lambda', () => {
    const actionsOf = (s: Statement): string[] => [s.Action].flat();
    // The invoke-url grant takes the form grantInvokeUrl renders —
    // lambda:InvokeFunctionUrl (AWS_IAM auth condition) plus
    // lambda:InvokeFunction (InvokedViaFunctionUrl condition), on the backing
    // functions' ARNs — merged into one statement per action.
    const urls = template.findResources('AWS::Lambda::Url') as Record<string, any>;
    const urlFunctionIds = new Set(
      Object.values(urls).map((u) => u.Properties.TargetFunctionArn['Fn::GetAtt'][0]),
    );
    expect(urlFunctionIds).toHaveLength(8);
    // The grant names the functions' ARNs through each URL resource's
    // FunctionArn attribute; resolve that back to the backing function so the
    // assertion is on the functions, not the reference path.
    const grantedFunctions = (s: Statement): Set<string> =>
      new Set(
        ([s.Resource].flat() as { 'Fn::GetAtt': [string, string] }[]).map((r) => {
          const [id, attr] = r['Fn::GetAtt'];
          return attr === 'FunctionArn' && urls[id] ? urls[id].Properties.TargetFunctionArn['Fn::GetAtt'][0] : id;
        }),
      );
    const invokeUrl = cliStatements.filter((s) => actionsOf(s).includes('lambda:InvokeFunctionUrl'));
    expect(invokeUrl).toHaveLength(1);
    expect(invokeUrl[0].Condition).toEqual({ StringEquals: { 'lambda:FunctionUrlAuthType': 'AWS_IAM' } });
    expect(grantedFunctions(invokeUrl[0])).toEqual(urlFunctionIds);
    const invokeViaUrl = cliStatements.filter((s) => actionsOf(s).includes('lambda:InvokeFunction'));
    expect(invokeViaUrl).toHaveLength(1);
    expect(invokeViaUrl[0].Condition).toEqual({ Bool: { 'lambda:InvokedViaFunctionUrl': true } });
    expect(grantedFunctions(invokeViaUrl[0])).toEqual(urlFunctionIds);
    const lambdaActions = cliStatements.flatMap(actionsOf).filter((a) => a.startsWith('lambda:'));
    expect(lambdaActions.sort()).toEqual(['lambda:InvokeFunction', 'lambda:InvokeFunctionUrl']);
  });

  it('grants the CLI user log reading on the runner and boot groups, and their streams only', () => {
    const actionsOf = (s: Statement): string[] => [s.Action].flat();
    const logsStatements = cliStatements.filter((s) => actionsOf(s).some((a) => a.startsWith('logs:')));
    expect(logsStatements).toHaveLength(1);
    expect(actionsOf(logsStatements[0])).toEqual(
      expect.arrayContaining(['logs:DescribeLogStreams', 'logs:FilterLogEvents', 'logs:GetLogEvents']),
    );
    const controlGroupIds = Object.entries(template.findResources('AWS::Logs::LogGroup')).filter(
      ([, g]) => {
        const name = String((g as { Properties: { LogGroupName: string } }).Properties.LogGroupName);
        return name.startsWith('/cloud-vm-llm/') && !name.includes('/lambda/') && name !== '/cloud-vm-llm/seed';
      },
    );
    expect(
      controlGroupIds.map(([, g]) => (g as { Properties: { LogGroupName: string } }).Properties.LogGroupName).sort(),
    ).toEqual(['/cloud-vm-llm/boot', '/cloud-vm-llm/llamacpp', '/cloud-vm-llm/vllm']);
    // Each grant is a group's ARN (Fn::GetAtt) or a stream under it
    // (Fn::Join of that GetAtt with ":*").
    const granted = ([logsStatements[0].Resource].flat() as Record<string, unknown>[]).map((r) => {
      const parts = r['Fn::Join'] ? (r['Fn::Join'] as [string, unknown[]])[1] : [r];
      return (parts[0] as { 'Fn::GetAtt': string[] })['Fn::GetAtt'][0];
    });
    expect(granted).toHaveLength(controlGroupIds.length * 2);
    for (const [id] of controlGroupIds) {
      expect(granted.filter((g) => g === id)).toHaveLength(2);
    }
  });

  it('grants the CLI user DescribeStacks on this stack and pricing lookups only, in their services', () => {
    const actionsOf = (s: Statement): string[] => [s.Action].flat();
    const cfn = cliStatements.find((s) => actionsOf(s).includes('cloudformation:DescribeStacks'));
    expect(cfn).toBeDefined();
    expect(cfn!.Resource).toEqual({ Ref: 'AWS::StackId' });
    expect(actionsOf(cfn!)).toEqual(['cloudformation:DescribeStacks']);
    const pricing = cliStatements.find((s) => actionsOf(s).includes('pricing:GetProducts'));
    expect(pricing).toBeDefined();
    expect(actionsOf(pricing!)).toEqual(['pricing:GetProducts']);
  });

  it('lets the CLI user manage its own access keys, scoped to its own ARN', () => {
    const actionsOf = (s: Statement): string[] => [s.Action].flat();
    const iam = cliStatements.filter((s) => actionsOf(s).some((a) => a.startsWith('iam:')));
    expect(iam).toHaveLength(1);
    expect(actionsOf(iam[0])).toEqual(
      expect.arrayContaining(['iam:GetUser', 'iam:ListAccessKeys', 'iam:CreateAccessKey', 'iam:DeleteAccessKey']),
    );
    // Built from pseudo parameters (not the user's attribute, which would be
    // a dependency cycle), so assert on its shape: the user's own ARN.
    const resource = [iam[0].Resource].flat()[0] as { 'Fn::Join': [string, unknown[]] };
    expect(resource['Fn::Join'][0]).toBe('');
    expect(resource['Fn::Join'][1]).toEqual([
      'arn:',
      { Ref: 'AWS::Partition' },
      ':iam::',
      { Ref: 'AWS::AccountId' },
      ':user/cloud-vm-llm-remote-cli',
    ]);
  });

  it('grants the CLI user no provisioning permission: no stack creation, no image builder, no instance launch', () => {
    const actions = cliStatements.flatMap((s) => [s.Action].flat());
    expect(actions).not.toContain('cloudformation:CreateStack');
    expect(actions).not.toContain('cloudformation:DeleteStack');
    expect(actions.filter((a) => a.startsWith('imagebuilder:'))).toHaveLength(0);
    expect(actions).not.toContain('ec2:RunInstances');
    const selfService = new Set([
      'iam:GetUser',
      'iam:ListAccessKeys',
      'iam:CreateAccessKey',
      'iam:DeleteAccessKey',
    ]);
    expect(actions.filter((a) => a.startsWith('iam:') && !selfService.has(a))).toHaveLength(0);
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
      'secretsmanager:PutSecretValue',
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
    // A wildcard PassRole would let a caller hand EC2 (or Scheduler) any role in the account.
    for (const statement of passRole) {
      expect(JSON.stringify(statement.Resource)).not.toBe('"*"');
      expect(JSON.stringify(statement.Condition)).toMatch(/ec2\.amazonaws\.com|scheduler\.amazonaws\.com/);
    }
  });

  it('runs schedules from a Scheduler group, as a role that can only invoke the start and stop Lambdas', () => {
    template.resourceCountIs('AWS::Scheduler::ScheduleGroup', 1);
    const roles = template.findResources('AWS::IAM::Role') as Record<string, any>;
    const [roleId, role] = Object.entries(roles).find(([, r]) =>
      JSON.stringify(r.Properties.AssumeRolePolicyDocument).includes('scheduler.amazonaws.com'),
    )!;
    expect(role).toBeDefined();
    const policies = template.findResources('AWS::IAM::Policy') as Record<string, any>;
    const granted = Object.values(policies)
      .filter((p) => (p.Properties.Roles as { Ref: string }[]).some((r) => r.Ref === roleId))
      .flatMap((p) => p.Properties.PolicyDocument.Statement as Statement[]);
    // Only lambda:InvokeFunction (one statement per target function), and never on a wildcard.
    expect(new Set(granted.flatMap((s) => [s.Action].flat()))).toEqual(new Set(['lambda:InvokeFunction']));
    expect(granted.length).toBeGreaterThanOrEqual(2);
    for (const s of granted) {
      expect(JSON.stringify(s.Resource)).not.toContain('"*"');
    }
  });

  it('gives the schedule Lambda its group, role and target functions, and scopes its Scheduler writes to the group', () => {
    const fns = template.findResources('AWS::Lambda::Function') as Record<string, any>;
    const schedule = Object.values(fns).find((f) =>
      f.Properties.Environment?.Variables?.SCHEDULE_GROUP !== undefined,
    );
    expect(schedule).toBeDefined();
    const vars = Object.keys(schedule.Properties.Environment.Variables).sort();
    expect(vars).toEqual(
      ['CONTROL_PLANE_VERSION', 'SCHEDULER_ROLE_ARN', 'SCHEDULE_GROUP', 'START_FN_ARN', 'STOP_FN_ARN'].sort(),
    );

    const statements = allPolicyStatements(template);
    const writes = statements.filter((s) => [s.Action].flat().includes('scheduler:CreateSchedule'));
    expect(writes).toHaveLength(1);
    expect([writes[0].Action].flat().sort()).toEqual([
      'scheduler:CreateSchedule',
      'scheduler:DeleteSchedule',
      'scheduler:UpdateSchedule',
    ]);
    expect(JSON.stringify(writes[0].Resource)).not.toBe('"*"');
    expect(JSON.stringify(writes[0].Resource)).toContain(':schedule/');
  });

  it('keeps the idle sweep as the only classic rule: user schedules live in Scheduler', () => {
    template.resourceCountIs('AWS::Events::Rule', 1);
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

  it('carries the seed environment and launch grants on the start Lambda', () => {
    // The weights gate launches the seed itself, so the start Lambda needs
    // the seed material deploy and seed carry — the actions test above cannot
    // see this, because those grants duplicate the start's own.
    const fns = template.findResources('AWS::Lambda::Function');
    const start = Object.values(fns).find((f) =>
      String(f.Properties.Description).includes('Launches an environment instance'),
    );
    const env = start!.Properties.Environment.Variables;
    expect(env.MAX_CONCURRENT_SEEDS).toBeDefined();
    expect(env.SEED_INSTANCE_TYPE).toBeDefined();
    expect(env.SEED_INSTANCE_PROFILE_ARN).toBeDefined();
    expect(env.SEEDER_BUCKET).toBeDefined();

    const policies = template.findResources('AWS::IAM::Policy');
    const startPolicy = Object.values(policies).find((p) =>
      String(p.Properties.PolicyName).startsWith('StartFnServiceRoleDefaultPolicy'),
    );
    expect(startPolicy).toBeDefined();
    const statements = startPolicy!.Properties.PolicyDocument.Statement as {
      Action: string | string[];
      Resource?: unknown;
      Condition?: unknown;
    }[];
    // The start's own PassRole covers the inference role, so the seed one is
    // found by its resource, not by the action.
    const passSeedRole = statements.find(
      (s) => [s.Action].flat().includes('iam:PassRole') && JSON.stringify(s.Resource).includes('SeedRole'),
    );
    expect(passSeedRole).toBeDefined();
    expect(JSON.stringify(passSeedRole!.Condition)).toContain('ec2.amazonaws.com');
    const amiParameter = statements.find(
      (s) =>
        [s.Action].flat().includes('ssm:GetParameter') &&
        JSON.stringify(s.Resource).includes('ami-amazon-linux-latest'),
    );
    expect(amiParameter).toBeDefined();
    const weightsRead = statements.find(
      (s) =>
        [s.Action].flat().some((a) => a.startsWith('s3:GetObject')) &&
        JSON.stringify(s.Resource).includes('/models/*'),
    );
    expect(weightsRead).toBeDefined();
  });

  it('grants the env Lambda read-only access to the deploy-config', () => {
    // Regression guard: the env Lambda reads the deploy-config to report what
    // is deployed (spinloop harness --env auto-configure), but must never be
    // able to write it — only deploy/start do that.
    const fns = template.findResources('AWS::Lambda::Function');
    const env = Object.values(fns).find((f) =>
      String(f.Properties.Description).includes('Returns base URL, API key and deploy-config'),
    );
    expect(env).toBeDefined();

    const policies = template.findResources('AWS::IAM::Policy');
    const envPolicy = Object.values(policies).find((p) =>
      String(p.Properties.PolicyName).startsWith('EnvFnServiceRoleDefaultPolicy'),
    );
    expect(envPolicy).toBeDefined();
    const statements = envPolicy!.Properties.PolicyDocument.Statement as {
      Action: string | string[];
      Resource?: unknown;
    }[];
    const ssmRead = statements.find(
      (s) => [s.Action].flat().includes('ssm:GetParameter') && JSON.stringify(s.Resource).includes('cloud-vm-llm'),
    );
    expect(ssmRead).toBeDefined();
    expect([ssmRead!.Action].flat()).not.toContain('ssm:PutParameter');
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

  it('lets only the start and schedule Lambdas delete SSM parameters, each only its own', () => {
    const policies = template.findResources('AWS::IAM::Policy') as Record<string, any>;
    const fns = template.findResources('AWS::Lambda::Function') as Record<string, any>;
    const deleters = Object.values(policies).filter((p) =>
      (p.Properties.PolicyDocument.Statement as Statement[]).some((s) =>
        [s.Action].flat().includes('ssm:DeleteParameter'),
      ),
    );
    expect(deleters).toHaveLength(2);

    // What each may delete, keyed by the Lambda the role belongs to. The Lambdas
    // are told apart by settings only they are given: the AMI settings for the
    // start Lambda, the schedule group for the schedule Lambda.
    const deletable: Record<string, string> = {};
    for (const policy of deleters) {
      const statements = (policy.Properties.PolicyDocument.Statement as Statement[]).filter((s) =>
        [s.Action].flat().includes('ssm:DeleteParameter'),
      );
      expect(statements).toHaveLength(1);
      const resources = JSON.stringify(statements[0].Resource);
      expect(resources).not.toContain('"*"');
      const roleId = (policy.Properties.Roles as { Ref: string }[])[0].Ref;
      const owner = Object.values(fns).find((f) => f.Properties.Role['Fn::GetAtt'][0] === roleId);
      const vars = owner?.Properties.Environment.Variables ?? {};
      const who = vars.AMI_ROLE_TAG_KEY ? 'start' : vars.SCHEDULE_GROUP ? 'schedule' : 'other';
      deletable[who] = resources;
    }

    expect(Object.keys(deletable).sort()).toEqual(['schedule', 'start']);
    expect(deletable.start).toContain('parameter/cloud-vm-llm/*/wake-lock');
    expect(deletable.start).not.toContain('schedules');
    expect(deletable.schedule).toContain('parameter/cloud-vm-llm/*/schedules');
    expect(deletable.schedule).not.toContain('wake-lock');
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
    for (const name of ['start', 'stop', 'deploy', 'seed', 'stats', 'env', 'update', 'schedule']) {
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
    // 4 instance-facing groups (llamacpp, vllm, boot, seed) + 8 Lambda groups.
    template.resourceCountIs('AWS::Logs::LogGroup', 12);
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
      'SpinloopRemoteConfig',
      'StartUrl',
      'StopUrl',
      'DeployUrl',
      'StatsUrl',
      'EnvUrl',
      'ScheduleUrl',
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

  it('names each recipe after the base AMI it was built on', () => {
    // Recipe properties are create-only and Image Builder rejects a create at
    // an existing name+version. The parent image moves whenever Canonical
    // publishes a new Ubuntu, so the name has to move with it or the
    // replacement collides with the recipe it is replacing.
    const recipes = Object.values(template.findResources('AWS::ImageBuilder::ImageRecipe'));
    expect(recipes).toHaveLength(2);
    for (const recipe of recipes) {
      const [prefix, parentRef] = recipe.Properties.Name['Fn::Join'][1];
      expect(prefix).toMatch(/-recipe-$/);
      expect(parentRef).toEqual(recipe.Properties.ParentImage);
    }
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
    // Both need the driver; neither takes an spinloop version — the instance's
    // boot installs spinloop, so the AMI carries no release of it to pin.
    for (const p of params) {
      expect(names(p)).toContain('NvidiaDriverPackage');
      expect(names(p)).not.toContain('SpinloopVersion');
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
      expect(data).toContain('/var/lib/spinloop/daemon/engine.log');
      expect(data).toContain('copytruncate');
      expect(data).toContain('llm-logrotate.timer');
      expect(data).not.toContain('cloud-init-output.log');
    }
  });

  it('bakes the crash-nudge timer, but not spinloop, into every runner', () => {
    const components = Object.values(template.findResources('AWS::ImageBuilder::Component'));
    for (const c of components) {
      const data = c.Properties.Data as string;
      // spinloop is installed by each instance's boot (the deploy config's pin
      // or latest), never by the bake: no release download, no install.
      expect(data).not.toContain('SpinloopVersion');
      expect(data).not.toContain('spinloop_linux_amd64.tar.gz');
      expect(data).not.toContain('install -m 0755 /tmp/spinloop-dl/spinloop /usr/local/bin/spinloop');
      // The nudge acts only on a crashed engine.
      expect(data).toContain('spinloop-nudge.timer');
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

type Statement = { Action: string | string[]; Resource?: unknown; Condition?: unknown };

function statementsForResource(
  template: Template,
  type: string,
  match: (logicalId: string, resource: any) => boolean,
): Statement[] {
  const resources = template.findResources(type) as Record<string, any>;
  const [logicalId, resource] = Object.entries(resources).find(([id, r]) => match(id, r)) ?? [];
  if (!logicalId) {
    throw new Error(`no ${type} matched ${match}`);
  }
  const [arn] = (resource.Properties.ManagedPolicyArns ?? []) as { Ref?: string }[];
  const policy = arn?.Ref ? (template.toJSON().Resources as Record<string, any>)[arn.Ref] : undefined;
  if (!policy || policy.Type !== 'AWS::IAM::ManagedPolicy') {
    throw new Error(`no managed policy attached to ${type} ${logicalId}`);
  }
  return policy.Properties.PolicyDocument.Statement;
}

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
