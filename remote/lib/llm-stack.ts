import * as cdk from 'aws-cdk-lib';
import {
  aws_ec2 as ec2,
  aws_events as events,
  aws_events_targets as targets,
  aws_iam as iam,
  aws_lambda as lambda,
  aws_lambda_nodejs as nodejs,
  aws_logs as logs,
  aws_s3 as s3,
  aws_s3_assets as s3assets,
  aws_secretsmanager as secretsmanager,
} from 'aws-cdk-lib';
import { Construct } from 'constructs';
import { buildSync } from 'esbuild';
import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';
import { SEED_LOG_GROUP } from '../lambda/shared/seed/contract';
import { logGroupEnvVar, type Runner, RUNNERS } from '../lambda/shared/deploy-config';
import {
  AMI_ROLE_TAG_KEY,
  AMI_ROLE_TAG_VALUE,
  AMI_RUNNER_TAG_KEY,
  type LlmConfig,
} from './config';

export interface LlmStackProps extends cdk.StackProps {
  config: LlmConfig;
}

// Instances the start Lambda launches carry this tag; the idle/status Lambdas
// find managed instances by it. Which *environment* an instance belongs to is
// a second tag (cloud-vm-llm:env), applied at launch.
const TAG_KEY = 'cloud-vm-llm';
const TAG_VALUE = 'endpoint';
/** Seed instances carry the same key with this value, so the two sweeps never overlap. */
const SEED_TAG_VALUE = 'seed';

/**
 * Bundle the seeder to a single file and hand back the directory to publish.
 *
 * Done at synth time, in the stack, for the same reason `NodejsFunction` does
 * it internally: it removes any ordering hazard between a `pnpm build` step and
 * `cdk deploy`, and ties the published bundle to the stack version that
 * references it. The instance then fetches one file with the AWS CLI it already
 * has, so nothing needs baking into an image.
 */
function bundleSeeder(): string {
  const outdir = fs.mkdtempSync(path.join(os.tmpdir(), 'seeder-bundle-'));
  buildSync({
    entryPoints: [path.join(__dirname, '..', 'seeder', 'src', 'index.ts')],
    outfile: path.join(outdir, 'seed.mjs'),
    bundle: true,
    platform: 'node',
    format: 'esm',
    target: 'node20',
    // The AWS SDK is bundled rather than assumed: AL2023 ships the AWS CLI, not
    // the JavaScript SDK, and the point of this design is that nothing is baked.
    banner: {
      js: "import{createRequire as __cr}from'node:module';const require=__cr(import.meta.url);",
    },
  });
  return outdir;
}

/**
 * The account-level control plane — deployed once by `outfit remote bootstrap`,
 * analogous to `cdk bootstrap`. It holds what every environment reuses: the
 * weights bucket, the VPC, the shared IAM roles, and the environment-aware
 * lifecycle Lambdas (start/stop/deploy). It creates NO Elastic IP and NO
 * instance: an environment — its EIP, security group (per-env allowed CIDR),
 * API-key secret and SSM state — is created on demand by the deploy Lambda
 * when `outfit remote deploy` names it, and the same shared Lambdas then
 * start, stop and idle-monitor every environment's instance in the account.
 */
export class LlmStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props: LlmStackProps) {
    super(scope, id, props);
    const cfg = props.config;

    // Where the weights live in S3, shared by all environments: one model
    // seeded once (under models/<runner>/<modelId>[/<quant>]/) serves every
    // environment that names it.
    const weightsBucket = new s3.Bucket(this, 'Weights', {
      // Retain on destroy — the weights are ~30 GB and re-seeding takes time.
      removalPolicy: cdk.RemovalPolicy.RETAIN,
      encryption: s3.BucketEncryption.S3_MANAGED,
      blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
      enforceSSL: true,
    });

    // One public subnet per g6e AZ, so the start Lambda has a target in each
    // zone to try. No NAT gateways — instances get public IPs directly.
    const vpc = new ec2.Vpc(this, 'Vpc', {
      availabilityZones: cfg.availabilityZones,
      natGateways: 0,
      restrictDefaultSecurityGroup: false,
      subnetConfiguration: [{ name: 'public', subnetType: ec2.SubnetType.PUBLIC }],
    });

    // The disposable seed instance's security group: outbound only (Hugging
    // Face + S3), no ingress. Environment security groups — the ones with a
    // per-env allowed CIDR — are created by the deploy Lambda, not here.
    const seedSg = new ec2.SecurityGroup(this, 'SeedSg', {
      vpc,
      description: 'cloud-vm-llm seed instance - outbound only',
      allowAllOutbound: true,
    });

    // Optional Hugging Face token, used only by the seed job for gated repos.
    // Shared: seeding fills the shared bucket, whichever environment asked.
    let hfSecret: secretsmanager.Secret | undefined;
    if (cfg.hfToken) {
      hfSecret = new secretsmanager.Secret(this, 'HfToken', {
        description: 'Hugging Face token used only when seeding weights to S3',
        secretStringValue: cdk.SecretValue.unsafePlainText(cfg.hfToken),
      });
    }

    // ARN patterns for the per-environment resources the Lambdas create and
    // read at runtime: /cloud-vm-llm/<env>/* SSM parameters and
    // cloud-vm-llm/<env>/api-key secrets.
    const envParamArn = `arn:${cdk.Aws.PARTITION}:ssm:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:parameter/cloud-vm-llm/*`;
    const envSecretArn = `arn:${cdk.Aws.PARTITION}:secretsmanager:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:secret:cloud-vm-llm/*`;

    // Where the runtime instances ship logs: one group per engine (the engine's
    // own stdout/stderr) and one for the boot log (the user-data script's
    // output, which holds the pre-engine steps like the weights S3 pull). Each
    // instance writes a stream named <env>/<instance-id>, so a terminated
    // instance's logs outlive it and are grouped by environment. Pre-created
    // here — not by the agent at first write — so retention and teardown are
    // managed infrastructure. Retention is short by default (see config).
    const logRetention = cfg.logRetentionDays as logs.RetentionDays;
    // One engine log group per runner, driven by RUNNERS so a new runner gets
    // its group (and the wiring below) for free. The logical id capitalises
    // the runner name, matching the ids the groups were first created under.
    const engineLogGroups = Object.fromEntries(
      RUNNERS.map((runner) => [
        runner,
        new logs.LogGroup(this, `${runner.charAt(0).toUpperCase()}${runner.slice(1)}LogGroup`, {
          logGroupName: `/cloud-vm-llm/${runner}`,
          retention: logRetention,
          removalPolicy: cdk.RemovalPolicy.DESTROY,
        }),
      ]),
    ) as Record<Runner, logs.LogGroup>;
    const bootLogGroup = new logs.LogGroup(this, 'BootLogGroup', {
      logGroupName: '/cloud-vm-llm/boot',
      retention: logRetention,
      removalPolicy: cdk.RemovalPolicy.DESTROY,
    });

    // Seed records: progress, outcomes, and the diagnosis of a failed seed
    // after its instance is gone. Kept longer than the engine logs (see
    // config.ts) because they are this account's record of what is in its
    // weights bucket and why.
    const seedLogGroup = new logs.LogGroup(this, 'SeedLogGroup', {
      logGroupName: SEED_LOG_GROUP,
      retention: cfg.seedLogRetentionDays as logs.RetentionDays,
      removalPolicy: cdk.RemovalPolicy.DESTROY,
    });

    // The seeder program, bundled at synth and published for the instance to
    // fetch. Replaces the boot-script-as-TypeScript-string the seed used to be.
    const seederAsset = new s3assets.Asset(this, 'SeederBundle', { path: bundleSeeder() });

    // Role assumed by every environment's runtime instance — SSM (for the
    // Lambdas' health/idle checks), read its environment's API-key secret,
    // and read the weights.
    const instanceRole = new iam.Role(this, 'InstanceRole', {
      assumedBy: new iam.ServicePrincipal('ec2.amazonaws.com'),
      managedPolicies: [
        iam.ManagedPolicy.fromAwsManagedPolicyName('AmazonSSMManagedInstanceCore'),
      ],
    });
    instanceRole.addToPolicy(
      new iam.PolicyStatement({
        actions: ['secretsmanager:GetSecretValue'],
        resources: [envSecretArn],
      }),
    );
    // Just what the CloudWatch agent needs to ship logs — create a stream and
    // put events into the three pre-created groups. No logs:CreateLogGroup:
    // the groups exist already, and scoping to these ARNs keeps the grant
    // narrower than the CloudWatchAgentServerPolicy managed policy would.
    instanceRole.addToPolicy(
      new iam.PolicyStatement({
        actions: ['logs:CreateLogStream', 'logs:PutLogEvents'],
        resources: [
          ...RUNNERS.map((runner) => engineLogGroups[runner].logGroupArn),
          bootLogGroup.logGroupArn,
        ],
      }),
    );
    // Broad over the whole models/ tree: the deploy-config chooses the prefix
    // (per runner + model + quant), so the grant can't be pinned to one.
    weightsBucket.grantRead(instanceRole, 'models/*');
    const instanceProfile = new iam.InstanceProfile(this, 'InstanceProfile', {
      role: instanceRole,
    });

    // Role for the disposable seed instance — write the weights it downloads
    // from Hugging Face, and read the HF token if the repo is gated.
    const seedRole = new iam.Role(this, 'SeedRole', {
      assumedBy: new iam.ServicePrincipal('ec2.amazonaws.com'),
      managedPolicies: [
        iam.ManagedPolicy.fromAwsManagedPolicyName('AmazonSSMManagedInstanceCore'),
      ],
    });
    weightsBucket.grantReadWrite(seedRole, 'models/*');
    hfSecret?.grantRead(seedRole);
    // Fetch the seeder bundle, and ship records into the seed group only.
    seederAsset.grantRead(seedRole);
    seedRole.addToPolicy(
      new iam.PolicyStatement({
        actions: ['logs:CreateLogStream', 'logs:PutLogEvents'],
        resources: [seedLogGroup.logGroupArn, `${seedLogGroup.logGroupArn}:*`],
      }),
    );
    const seedProfile = new iam.InstanceProfile(this, 'SeedProfile', { role: seedRole });

    /** Everything a Lambda needs to launch and supervise seeds. */
    const seedEnv = {
      WEIGHTS_BUCKET: weightsBucket.bucketName,
      SEED_INSTANCE_TYPE: cfg.seedInstanceType,
      SEED_SUBNET_ID: vpc.publicSubnets[0].subnetId,
      SEED_SECURITY_GROUP_ID: seedSg.securityGroupId,
      SEED_INSTANCE_PROFILE_ARN: seedProfile.instanceProfileArn,
      HF_TOKEN_SECRET_ARN: hfSecret?.secretArn ?? '',
      SEEDER_BUCKET: seederAsset.s3BucketName,
      SEEDER_KEY: seederAsset.s3ObjectKey,
      // The boot script renders this into `shutdown -h +N` and the sweep reads
      // the same value, so the two caps cannot drift apart.
      MAX_SEED_MINUTES: String(cfg.maxSeedMinutes),
    };

    /** Launching a seed instance: run it, tag it, and pass it the seed role. */
    const seedLaunchStatements = () => [
      new iam.PolicyStatement({ actions: ['ec2:RunInstances'], resources: ['*'] }),
      new iam.PolicyStatement({
        actions: ['ec2:CreateTags'],
        resources: [`arn:${cdk.Aws.PARTITION}:ec2:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:*/*`],
        conditions: { StringEquals: { 'ec2:CreateAction': 'RunInstances' } },
      }),
      // Only the seed role, and only to EC2, so this cannot be used to hand a
      // caller a more privileged role.
      new iam.PolicyStatement({
        actions: ['iam:PassRole'],
        resources: [seedRole.roleArn],
        conditions: { StringEquals: { 'iam:PassedToService': 'ec2.amazonaws.com' } },
      }),
      // The stock AL2023 image id lives in an AWS-owned public parameter.
      new iam.PolicyStatement({
        actions: ['ssm:GetParameter'],
        resources: [
          `arn:${cdk.Aws.PARTITION}:ssm:${cdk.Aws.REGION}::parameter/aws/service/ami-amazon-linux-latest/*`,
        ],
      }),
    ];

    /** Reading and writing seed records. */
    const seedLogStatements = () => [
      new iam.PolicyStatement({
        actions: [
          'logs:DescribeLogStreams',
          'logs:GetLogEvents',
          'logs:CreateLogStream',
          'logs:PutLogEvents',
        ],
        resources: [seedLogGroup.logGroupArn, `${seedLogGroup.logGroupArn}:*`],
      }),
    ];

    /** Terminating a seed — tag-scoped, so these credentials cannot reach an inference box. */
    const seedTerminateStatement = () =>
      new iam.PolicyStatement({
        actions: ['ec2:TerminateInstances'],
        resources: ['*'],
        conditions: { StringEquals: { [`ec2:ResourceTag/${TAG_KEY}`]: SEED_TAG_VALUE } },
      });

    const runShellScriptDocArn = `arn:${cdk.Aws.PARTITION}:ssm:${cdk.Aws.REGION}::document/AWS-RunShellScript`;

    const describeStatement = new iam.PolicyStatement({
      actions: ['ec2:DescribeInstances', 'ssm:DescribeInstanceInformation', 'ssm:GetCommandInvocation'],
      resources: ['*'],
    });
    // ssm:SendCommand authorises the document AND the target instance as
    // separate resources. A tag condition can only hold on the instance — the
    // AWS-managed AWS-RunShellScript document is untagged, so putting both ARNs
    // in one conditioned statement denies the document and the whole call fails
    // with AccessDenied (which silently broke every health-check and idle
    // scrape). Grant them in two statements: instance tag-scoped, document open.
    const sendCommandStatements = () => [
      new iam.PolicyStatement({
        actions: ['ssm:SendCommand'],
        resources: [`arn:${cdk.Aws.PARTITION}:ec2:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:instance/*`],
        conditions: { StringEquals: { [`ssm:resourceTag/${TAG_KEY}`]: TAG_VALUE } },
      }),
      new iam.PolicyStatement({
        actions: ['ssm:SendCommand'],
        resources: [runShellScriptDocArn],
      }),
    ];
    // The per-environment SSM state (the deploy-config) lives under
    // /cloud-vm-llm/<env>/ — created and read at runtime, not by CDK.
    const envParamsStatement = new iam.PolicyStatement({
      actions: ['ssm:GetParameter', 'ssm:PutParameter'],
      resources: [envParamArn],
    });

    // The idle sweep only reads the deploy-config: activity history lives on
    // the instance now, so nothing in the stop path writes a parameter.
    const readEnvParamsStatement = new iam.PolicyStatement({
      actions: ['ssm:GetParameter'],
      resources: [envParamArn],
    });

    const commonEnv = {
      TAG_KEY,
      TAG_VALUE,
      ENGINE_PORT: String(cfg.enginePort),
    };

    const startFn = new nodejs.NodejsFunction(this, 'StartFn', {
      description: 'Launches an environment instance (or re-wakes a stopped one; per-AZ capacity fallback) and waits until it serves',
      entry: path.join(__dirname, '..', 'lambda', 'start', 'index.ts'),
      handler: 'handler',
      runtime: lambda.Runtime.NODEJS_22_X,
      architecture: lambda.Architecture.ARM_64,
      // 15 min (Lambda max) so a cold wake — S3 sync + weight load — can return
      // "ready" in one call rather than making outfit poll through a 503.
      timeout: cdk.Duration.seconds(900),
      memorySize: 256,
      environment: {
        ...commonEnv,
        AMI_ROLE_TAG_KEY,
        AMI_ROLE_TAG_VALUE,
        AMI_RUNNER_TAG_KEY,
        INSTANCE_TYPE: cfg.instanceType,
        SUBNET_IDS: vpc.publicSubnets.map((s) => s.subnetId).join(','),
        INSTANCE_PROFILE_ARN: instanceProfile.instanceProfileArn,
        WEIGHTS_BUCKET: weightsBucket.bucketName,
        // Log groups the instance's CloudWatch agent ships to, one env var
        // per runner by convention (logGroupEnvVar). The group is fixed; the
        // stream (<env>/<instance-id>) is filled in at boot.
        ...Object.fromEntries(
          RUNNERS.map((runner) => [logGroupEnvVar(runner), engineLogGroups[runner].logGroupName]),
        ),
        BOOT_LOG_GROUP: bootLogGroup.logGroupName,
        // The environment's EIP, security group, API key and deploy-config are
        // all found at wake by the environment name — not baked into the env.
      },
    });
    // RunInstances resource-level scoping is impractical (it spans instance,
    // volume, network-interface, subnet, sg, image, profile), so it is granted
    // broadly; CreateTags is limited to the launch. Terminate (in the stop
    // Lambda) is tag-scoped, which bounds what these credentials can destroy.
    startFn.addToRolePolicy(
      new iam.PolicyStatement({
        actions: ['ec2:RunInstances', 'ec2:AssociateAddress'],
        resources: ['*'],
      }),
    );
    // Re-wake: start a stopped instance of this environment. Tag-scoped, like
    // the stop Lambda's actions.
    startFn.addToRolePolicy(
      new iam.PolicyStatement({
        actions: ['ec2:StartInstances'],
        resources: ['*'],
        conditions: { StringEquals: { [`ec2:ResourceTag/${TAG_KEY}`]: TAG_VALUE } },
      }),
    );
    startFn.addToRolePolicy(
      new iam.PolicyStatement({
        actions: ['ec2:CreateTags'],
        resources: [`arn:${cdk.Aws.PARTITION}:ec2:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:*/*`],
        conditions: { StringEquals: { 'ec2:CreateAction': 'RunInstances' } },
      }),
    );
    // Started-At: the session start written when a stopped instance is
    // re-woken — the max-runtime cap must measure the session, not first boot.
    startFn.addToRolePolicy(
      new iam.PolicyStatement({
        actions: ['ec2:CreateTags'],
        resources: [`arn:${cdk.Aws.PARTITION}:ec2:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:*/*`],
        conditions: { StringEquals: { [`ec2:ResourceTag/${TAG_KEY}`]: TAG_VALUE } },
      }),
    );
    startFn.addToRolePolicy(
      new iam.PolicyStatement({
        actions: ['iam:PassRole'],
        resources: [instanceRole.roleArn],
        conditions: { StringEquals: { 'iam:PassedToService': 'ec2.amazonaws.com' } },
      }),
    );
    startFn.addToRolePolicy(describeStatement);
    sendCommandStatements().forEach((s) => startFn.addToRolePolicy(s));
    // Find the latest baked AMI, the environment's EIP and its security group.
    // These Describe calls have no resource-level scoping.
    startFn.addToRolePolicy(
      new iam.PolicyStatement({
        actions: ['ec2:DescribeImages', 'ec2:DescribeAddresses', 'ec2:DescribeSecurityGroups'],
        resources: ['*'],
      }),
    );
    startFn.addToRolePolicy(envParamsStatement);
    startFn.addToRolePolicy(
      new iam.PolicyStatement({
        actions: ['secretsmanager:GetSecretValue'],
        resources: [envSecretArn],
      }),
    );

    const stopFn = new nodejs.NodejsFunction(this, 'StopFn', {
      description:
        'Stops idle environment instances (scheduled sweep) and terminates them after stop retention - or stops/terminates one immediately on request',
      entry: path.join(__dirname, '..', 'lambda', 'stop', 'index.ts'),
      handler: 'handler',
      runtime: lambda.Runtime.NODEJS_22_X,
      architecture: lambda.Architecture.ARM_64,
      timeout: cdk.Duration.seconds(120),
      memorySize: 256,
      environment: {
        ...commonEnv,
        IDLE_THRESHOLD_MINUTES: String(cfg.idleThresholdMinutes),
        STOP_RETENTION_MINUTES: String(cfg.stopRetentionMinutes),
        GRACE_PERIOD_MINUTES: String(cfg.gracePeriodMinutes),
        MAX_RUNTIME_MINUTES: String(cfg.maxRuntimeMinutes),
        // The sweep's seed pass, layer three of the termination guarantee.
        MAX_SEED_MINUTES: String(cfg.maxSeedMinutes),
        SEED_STALL_MINUTES: String(cfg.seedStallMinutes),
      },
    });
    stopFn.addToRolePolicy(
      new iam.PolicyStatement({
        actions: ['ec2:TerminateInstances', 'ec2:StopInstances'],
        resources: ['*'],
        conditions: { StringEquals: { [`ec2:ResourceTag/${TAG_KEY}`]: TAG_VALUE } },
      }),
    );
    // Stopped-At: the control plane's own stop time — EC2 has no equivalent.
    stopFn.addToRolePolicy(
      new iam.PolicyStatement({
        actions: ['ec2:CreateTags'],
        resources: [`arn:${cdk.Aws.PARTITION}:ec2:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:*/*`],
        conditions: { StringEquals: { [`ec2:ResourceTag/${TAG_KEY}`]: TAG_VALUE } },
      }),
    );
    stopFn.addToRolePolicy(describeStatement);
    sendCommandStatements().forEach((s) => stopFn.addToRolePolicy(s));
    stopFn.addToRolePolicy(readEnvParamsStatement);
    // The seed pass: reap stalled or over-age seeds and record why.
    stopFn.addToRolePolicy(seedTerminateStatement());
    seedLogStatements().forEach((s) => stopFn.addToRolePolicy(s));

    // The control plane `outfit remote deploy` calls: it creates the named
    // environment's resources (EIP, security group, API key, SSM state) if
    // absent, seeds the weights if missing, and writes the environment's
    // deploy-config. outfit needs only Lambda invoke (SigV4), no SSM/EC2 perms
    // of its own.
    const deployFn = new nodejs.NodejsFunction(this, 'DeployFn', {
      description: 'Creates an environment (EIP/SG/key/state) and sets what it serves',
      entry: path.join(__dirname, '..', 'lambda', 'deploy', 'index.ts'),
      handler: 'handler',
      runtime: lambda.Runtime.NODEJS_22_X,
      architecture: lambda.Architecture.ARM_64,
      timeout: cdk.Duration.seconds(60),
      memorySize: 256,
      environment: {
        ENGINE_PORT: String(cfg.enginePort),
        VPC_ID: vpc.vpcId,
        // Seeding: the Lambda launches the disposable download instance itself
        // when the posted config names weights that are not in S3 yet, and
        // returns the seed id so the caller can follow it.
        ...seedEnv,
      },
    });
    deployFn.addToRolePolicy(envParamsStatement);
    // Read-only on the weights: the Lambda only checks whether the sentinel
    // object exists; the seed instance itself does the writing.
    weightsBucket.grantRead(deployFn, 'models/*');
    // Environment creation: allocate the EIP, create the security group and
    // reconcile its ingress, tag both at creation. Describe* are unscoped.
    deployFn.addToRolePolicy(
      new iam.PolicyStatement({
        actions: [
          'ec2:DescribeImages',
          'ec2:DescribeInstances',
          'ec2:DescribeAddresses',
          'ec2:DescribeSecurityGroups',
          'ec2:AllocateAddress',
          'ec2:CreateSecurityGroup',
          'ec2:AuthorizeSecurityGroupIngress',
          'ec2:RevokeSecurityGroupIngress',
        ],
        resources: ['*'],
      }),
    );
    seedLaunchStatements().forEach((s) => deployFn.addToRolePolicy(s));
    deployFn.addToRolePolicy(
      new iam.PolicyStatement({
        actions: ['secretsmanager:CreateSecret', 'secretsmanager:DescribeSecret'],
        resources: [envSecretArn],
      }),
    );

    // The seed control surface: start, status, list and stop. Separate from
    // DeployFn because a progress poll should not re-enter a function whose job
    // is to mutate environment state, and because seeds are account-wide —
    // nothing here takes an environment.
    const seedFn = new nodejs.NodejsFunction(this, 'SeedFn', {
      description: 'Starts, reports on, lists and stops model weight seeds',
      entry: path.join(__dirname, '..', 'lambda', 'seed', 'index.ts'),
      handler: 'handler',
      runtime: lambda.Runtime.NODEJS_22_X,
      architecture: lambda.Architecture.ARM_64,
      timeout: cdk.Duration.seconds(60),
      memorySize: 256,
      environment: {
        TAG_KEY,
        ...seedEnv,
        MAX_CONCURRENT_SEEDS: String(cfg.maxConcurrentSeeds),
      },
    });
    seedLaunchStatements().forEach((s) => seedFn.addToRolePolicy(s));
    seedLogStatements().forEach((s) => seedFn.addToRolePolicy(s));
    seedFn.addToRolePolicy(seedTerminateStatement());
    seedFn.addToRolePolicy(
      new iam.PolicyStatement({ actions: ['ec2:DescribeInstances'], resources: ['*'] }),
    );
    // Read-only on the weights: only to check whether the manifest is there.
    weightsBucket.grantRead(seedFn, 'models/*');

    const statsFn = new nodejs.NodejsFunction(this, 'StatsFn', {
      description: 'Returns instance metrics: token usage, GPU, CPU, RAM',
      entry: path.join(__dirname, '..', 'lambda', 'stats', 'index.ts'),
      handler: 'handler',
      runtime: lambda.Runtime.NODEJS_22_X,
      architecture: lambda.Architecture.ARM_64,
      timeout: cdk.Duration.seconds(90),
      memorySize: 256,
      environment: {
        ...commonEnv,
      },
    });
    statsFn.addToRolePolicy(describeStatement);
    sendCommandStatements().forEach((s) => statsFn.addToRolePolicy(s));
    statsFn.addToRolePolicy(envParamsStatement);
    // Read the API key from Secrets Manager to auth the /metrics curl.
    statsFn.addToRolePolicy(
      new iam.PolicyStatement({
        actions: ['secretsmanager:GetSecretValue'],
        resources: [envSecretArn],
      }),
    );

    const startUrl = startFn.addFunctionUrl({ authType: lambda.FunctionUrlAuthType.AWS_IAM });
    const stopUrl = stopFn.addFunctionUrl({ authType: lambda.FunctionUrlAuthType.AWS_IAM });
    const deployUrl = deployFn.addFunctionUrl({ authType: lambda.FunctionUrlAuthType.AWS_IAM });
    const statsUrl = statsFn.addFunctionUrl({ authType: lambda.FunctionUrlAuthType.AWS_IAM });
    const seedUrl = seedFn.addFunctionUrl({ authType: lambda.FunctionUrlAuthType.AWS_IAM });

    // Env Lambda — returns the API key and base URL for a running endpoint.
    // Minimal perms: read the environment's EIP and API key; no EC2 write.
    const envFn = new nodejs.NodejsFunction(this, 'EnvFn', {
      description: 'Returns base URL and API key for a running environment instance',
      entry: path.join(__dirname, '..', 'lambda', 'env', 'index.ts'),
      handler: 'handler',
      runtime: lambda.Runtime.NODEJS_22_X,
      architecture: lambda.Architecture.ARM_64,
      timeout: cdk.Duration.seconds(30),
      memorySize: 128,
      environment: {
        TAG_KEY,
        TAG_VALUE,
        ENGINE_PORT: String(cfg.enginePort),
      },
    });
    // Read the environment's EIP and API key.
    envFn.addToRolePolicy(
      new iam.PolicyStatement({
        actions: ['ec2:DescribeAddresses', 'ec2:DescribeInstances'],
        resources: ['*'],
      }),
    );
    envFn.addToRolePolicy(
      new iam.PolicyStatement({
        actions: ['secretsmanager:GetSecretValue'],
        resources: [envSecretArn],
      }),
    );

    const envUrl = envFn.addFunctionUrl({ authType: lambda.FunctionUrlAuthType.AWS_IAM });

    // Update Lambda — handles arbitrary post-provision instance commands
    // (currently: set-keep). The first command sets the Retain-Until tag.
    const updateFn = new nodejs.NodejsFunction(this, 'UpdateFn', {
      description: 'Arbitrary instance commands — currently: set-keep (retain instance)',
      entry: path.join(__dirname, '..', 'lambda', 'update', 'index.ts'),
      handler: 'handler',
      runtime: lambda.Runtime.NODEJS_22_X,
      architecture: lambda.Architecture.ARM_64,
      timeout: cdk.Duration.seconds(30),
      memorySize: 128,
      environment: {
        TAG_KEY,
        TAG_VALUE,
      },
    });
    // Describe to find the instance by tag; CreateTags to apply the Retain-Until tag.
    // ec2:CreateTags does not support resource tag conditions, so it is broad.
    updateFn.addToRolePolicy(
      new iam.PolicyStatement({
        actions: ['ec2:DescribeInstances', 'ec2:CreateTags'],
        resources: ['*'],
      }),
    );

    const updateUrl = updateFn.addFunctionUrl({ authType: lambda.FunctionUrlAuthType.AWS_IAM });

    new events.Rule(this, 'IdleCheckRule', {
      description: 'Periodic idle sweep across every environment instance',
      schedule: events.Schedule.rate(cdk.Duration.minutes(5)),
      targets: [new targets.LambdaFunction(stopFn)],
    });

    // Discovery: `outfit remote deploy` reads these stack outputs (by the
    // well-known stack name) to find the control plane from any machine with
    // account access — no local file carries them.
    new cdk.CfnOutput(this, 'StartUrl', { value: startUrl.url });
    new cdk.CfnOutput(this, 'StopUrl', { value: stopUrl.url });
    new cdk.CfnOutput(this, 'DeployUrl', { value: deployUrl.url });
    new cdk.CfnOutput(this, 'StatsUrl', { value: statsUrl.url });
    new cdk.CfnOutput(this, 'EnvUrl', { value: envUrl.url });
    new cdk.CfnOutput(this, 'SeedUrl', { value: seedUrl.url });
    new cdk.CfnOutput(this, 'UpdateUrl', { value: updateUrl.url });
    new cdk.CfnOutput(this, 'Region', { value: this.region });
    new cdk.CfnOutput(this, 'WeightsBucket', { value: weightsBucket.bucketName });
    new cdk.CfnOutput(this, 'VpcId', { value: vpc.vpcId });
    // Consumed by the deploy Lambda (via SEED_* env vars) to launch the
    // disposable seed instance.
    new cdk.CfnOutput(this, 'SeedInstanceProfileArn', { value: seedProfile.instanceProfileArn });
    new cdk.CfnOutput(this, 'SeedInstanceType', { value: cfg.seedInstanceType });
    new cdk.CfnOutput(this, 'SeedSubnetId', { value: vpc.publicSubnets[0].subnetId });
    new cdk.CfnOutput(this, 'SeedSecurityGroupId', { value: seedSg.securityGroupId });
    new cdk.CfnOutput(this, 'HfTokenSecretArn', { value: hfSecret?.secretArn ?? '' });
    // The control URLs shared by every environment. No base_url here: an
    // environment's address is its own EIP, allocated at `outfit remote
    // deploy` and returned by it.
    new cdk.CfnOutput(this, 'OutfitRemoteConfig', {
      value: `{"start_url":"${startUrl.url}","stop_url":"${stopUrl.url}","deploy_url":"${deployUrl.url}","stats_url":"${statsUrl.url}","env_url":"${envUrl.url}","seed_url":"${seedUrl.url}","update_url":"${updateUrl.url}","region":"${this.region}"}`,
    });
  }
}
