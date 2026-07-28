import * as cdk from 'aws-cdk-lib';
import {
  aws_ec2 as ec2,
  aws_events as events,
  aws_events_targets as targets,
  aws_iam as iam,
  aws_lambda as lambda,
  aws_lambda_nodejs as nodejs,
  aws_s3 as s3,
  aws_secretsmanager as secretsmanager,
  aws_ssm as ssm,
} from 'aws-cdk-lib';
import { Construct } from 'constructs';
import * as path from 'path';
import {
  AMI_ROLE_TAG_KEY,
  AMI_ROLE_TAG_VALUE,
  AMI_RUNNER_TAG_KEY,
  type LlmConfig,
} from './config';
import { UNCONFIGURED_DEPLOY_CONFIG, weightsPrefixFor } from '../lambda/shared/deploy-config';

export interface LlmStackProps extends cdk.StackProps {
  config: LlmConfig;
}

// Instances the start Lambda launches carry this tag; the idle/status Lambdas
// find "the current instance" by it.
const TAG_KEY = 'cloud-vm-llm';
const TAG_VALUE = 'endpoint';

/**
 * The scale-to-zero runtime. It holds no EC2 instance of its own: the start
 * Lambda launches one from the slim baked AMI (found by tag) on demand, trying
 * each g6e AZ until one has capacity, and the idle Lambda terminates it. The
 * model weights live in the S3 bucket here (seeded once by `pnpm seed-model`)
 * and are synced onto the instance at boot, so the instance is stateless and
 * the AMI is model-agnostic.
 */
export class LlmStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props: LlmStackProps) {
    super(scope, id, props);
    const cfg = props.config;

    // Where the weights live in S3. modelId contains "/", which is a fine S3
    // key prefix. Seeded by `pnpm seed-model`, synced down at boot.
    const weightsBucket = new s3.Bucket(this, 'Weights', {
      // Retain on destroy — the weights are ~30 GB and re-seeding takes time.
      removalPolicy: cdk.RemovalPolicy.RETAIN,
      encryption: s3.BucketEncryption.S3_MANAGED,
      blockPublicAccess: s3.BlockPublicAccess.BLOCK_ALL,
      enforceSSL: true,
    });
    // Same derivation the deploy Lambda uses, so the initial config and the
    // seed agree on where a model's weights live.
    const weightsPrefix = weightsPrefixFor(cfg.runner, cfg.modelId, '');

    // One public subnet per g6e AZ, so the start Lambda has a target in each
    // zone to try. No NAT gateways — the instance gets a public IP directly.
    const vpc = new ec2.Vpc(this, 'Vpc', {
      availabilityZones: cfg.availabilityZones,
      natGateways: 0,
      restrictDefaultSecurityGroup: false,
      subnetConfiguration: [{ name: 'public', subnetType: ec2.SubnetType.PUBLIC }],
    });

    const securityGroup = new ec2.SecurityGroup(this, 'VllmSg', {
      vpc,
      description: 'vLLM endpoint - ingress restricted to the allowed CIDR',
      allowAllOutbound: true, // S3, Secrets Manager, SSM agent outbound
    });
    securityGroup.addIngressRule(
      ec2.Peer.ipv4(cfg.allowedCidr),
      ec2.Port.tcp(cfg.vllmPort),
      'vLLM OpenAI-compatible API',
    );

    // vLLM's API key. The launched instance reads it via its role at boot; the
    // start Lambda returns it to the client.
    const apiKey = new secretsmanager.Secret(this, 'ApiKey', {
      description: 'API key for the vLLM endpoint',
      generateSecretString: {
        excludePunctuation: true,
        includeSpace: false,
        passwordLength: 48,
      },
    });

    // Optional Hugging Face token, used only by the seed job for gated repos.
    let hfSecret: secretsmanager.Secret | undefined;
    if (cfg.hfToken) {
      hfSecret = new secretsmanager.Secret(this, 'HfToken', {
        description: 'Hugging Face token used only when seeding weights to S3',
        secretStringValue: cdk.SecretValue.unsafePlainText(cfg.hfToken),
      });
    }

    // Stable address so the endpoint base URL survives across launches. The
    // start Lambda associates it with each freshly launched instance.
    const eip = new ec2.CfnEIP(this, 'Eip', { domain: 'vpc' });
    const baseUrl = `http://${eip.attrPublicIp}:${cfg.vllmPort}/v1`;

    const idleState = new ssm.StringParameter(this, 'IdleState', {
      parameterName: '/cloud-vm-llm/idle-state',
      stringValue: '{}',
    });

    // What to serve — runner/model/context/serveArgs. The start Lambda reads it
    // at each wake, so switching model/runner is a parameter write, not a
    // redeploy. The parameter is outfit/manual-owned: CDK creates it with a
    // constant placeholder (below) so a later `cdk deploy` never clobbers a
    // real config, and `pnpm deploy` seeds this cfg-derived initial config over
    // the placeholder ONLY while it is still unconfigured (see
    // scripts/seed-deploy-config.mjs). The initial config is emitted as an
    // output rather than baked into the parameter value for that reason.
    const vllmServeArgs = [
      ...cfg.vllmExtraArgs.split(/\s+/).filter(Boolean),
      ...(cfg.toolCallParser
        ? ['--enable-auto-tool-choice', '--tool-call-parser', cfg.toolCallParser]
        : []),
      ...(cfg.reasoningParser ? ['--reasoning-parser', cfg.reasoningParser] : []),
    ];
    const initialDeployConfig = {
      runner: cfg.runner,
      modelId: cfg.modelId,
      quant: '',
      weightsPrefix,
      contextSize: cfg.maxModelLen,
      servedModelName: cfg.modelId,
      // llama.cpp's initial serveArgs come from the Outfit via `outfit remote
      // deploy`; there is no sensible CDK-side default for them.
      serveArgs: cfg.runner === 'vllm' ? vllmServeArgs : [],
    };
    const deployConfigParam = new ssm.StringParameter(this, 'DeployConfig', {
      parameterName: '/cloud-vm-llm/deploy-config',
      stringValue: UNCONFIGURED_DEPLOY_CONFIG,
    });

    // Role assumed by the launched runtime instance — SSM (for the Lambdas'
    // health/idle checks), read the API-key secret, and read the weights.
    const instanceRole = new iam.Role(this, 'InstanceRole', {
      assumedBy: new iam.ServicePrincipal('ec2.amazonaws.com'),
      managedPolicies: [
        iam.ManagedPolicy.fromAwsManagedPolicyName('AmazonSSMManagedInstanceCore'),
      ],
    });
    apiKey.grantRead(instanceRole);
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
    const seedProfile = new iam.InstanceProfile(this, 'SeedProfile', { role: seedRole });

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

    const commonEnv = {
      TAG_KEY,
      TAG_VALUE,
      VLLM_PORT: String(cfg.vllmPort),
      BASE_URL: baseUrl,
      STATE_PARAM_NAME: idleState.parameterName,
    };

    const startFn = new nodejs.NodejsFunction(this, 'StartFn', {
      description: 'Launches the baked AMI (per-AZ capacity fallback) and waits until vLLM serves',
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
        SECURITY_GROUP_ID: securityGroup.securityGroupId,
        INSTANCE_PROFILE_ARN: instanceProfile.instanceProfileArn,
        EIP_ALLOCATION_ID: eip.attrAllocationId,
        API_KEY_SECRET_ARN: apiKey.secretArn,
        WEIGHTS_BUCKET: weightsBucket.bucketName,
        // The model/runner/context/serveArgs come from the deploy-config
        // parameter, read at wake — not baked into the Lambda env.
        DEPLOY_CONFIG_PARAM: deployConfigParam.parameterName,
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
    startFn.addToRolePolicy(
      new iam.PolicyStatement({
        actions: ['ec2:CreateTags'],
        resources: [`arn:${cdk.Aws.PARTITION}:ec2:${cdk.Aws.REGION}:${cdk.Aws.ACCOUNT_ID}:*/*`],
        conditions: { StringEquals: { 'ec2:CreateAction': 'RunInstances' } },
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
    // Find the latest baked AMI by tag. DescribeImages has no resource-level
    // scoping, so it is granted broadly.
    startFn.addToRolePolicy(
      new iam.PolicyStatement({ actions: ['ec2:DescribeImages'], resources: ['*'] }),
    );
    idleState.grantRead(startFn);
    idleState.grantWrite(startFn);
    deployConfigParam.grantRead(startFn);
    apiKey.grantRead(startFn);

    const stopFn = new nodejs.NodejsFunction(this, 'StopFn', {
      description: 'Terminates the instance - immediately (manual) or after idle (scheduled)',
      entry: path.join(__dirname, '..', 'lambda', 'stop', 'index.ts'),
      handler: 'handler',
      runtime: lambda.Runtime.NODEJS_22_X,
      architecture: lambda.Architecture.ARM_64,
      timeout: cdk.Duration.seconds(120),
      memorySize: 256,
      environment: {
        ...commonEnv,
        IDLE_THRESHOLD_MINUTES: String(cfg.idleThresholdMinutes),
        GRACE_PERIOD_MINUTES: String(cfg.gracePeriodMinutes),
        MAX_RUNTIME_MINUTES: String(cfg.maxRuntimeMinutes),
        // The idle scrape reads the runner from the deploy-config to pick the
        // right /metrics names (vLLM and llama.cpp expose different ones).
        DEPLOY_CONFIG_PARAM: deployConfigParam.parameterName,
      },
    });
    stopFn.addToRolePolicy(
      new iam.PolicyStatement({
        actions: ['ec2:TerminateInstances'],
        resources: ['*'],
        conditions: { StringEquals: { [`ec2:ResourceTag/${TAG_KEY}`]: TAG_VALUE } },
      }),
    );
    stopFn.addToRolePolicy(describeStatement);
    sendCommandStatements().forEach((s) => stopFn.addToRolePolicy(s));
    idleState.grantRead(stopFn);
    idleState.grantWrite(stopFn);
    deployConfigParam.grantRead(stopFn);

    // The control plane `outfit remote deploy` calls: it validates the posted
    // DeployConfig and writes it to the deploy-config parameter. outfit needs
    // only Lambda invoke (SigV4), no SSM/EC2 perms of its own.
    const deployFn = new nodejs.NodejsFunction(this, 'DeployFn', {
      description: 'Sets the deploy-config (runner/model/context) the next wake serves',
      entry: path.join(__dirname, '..', 'lambda', 'deploy', 'index.ts'),
      handler: 'handler',
      runtime: lambda.Runtime.NODEJS_22_X,
      architecture: lambda.Architecture.ARM_64,
      timeout: cdk.Duration.seconds(60),
      memorySize: 256,
      environment: {
        DEPLOY_CONFIG_PARAM: deployConfigParam.parameterName,
        // Seeding: the Lambda launches the disposable download instance itself
        // when the posted config names weights that are not in S3 yet.
        WEIGHTS_BUCKET: weightsBucket.bucketName,
        SEED_INSTANCE_TYPE: cfg.builderInstanceType,
        SEED_SUBNET_ID: vpc.publicSubnets[0].subnetId,
        SEED_SECURITY_GROUP_ID: securityGroup.securityGroupId,
        SEED_INSTANCE_PROFILE_ARN: seedProfile.instanceProfileArn,
        HF_TOKEN_SECRET_ARN: hfSecret?.secretArn ?? '',
        AMI_ROLE_TAG_KEY,
        AMI_ROLE_TAG_VALUE,
        AMI_RUNNER_TAG_KEY,
      },
    });
    deployConfigParam.grantRead(deployFn);
    deployConfigParam.grantWrite(deployFn);
    // Read-only on the weights: the Lambda only checks whether the sentinel
    // object exists; the seed instance itself does the writing.
    weightsBucket.grantRead(deployFn, 'models/*');
    deployFn.addToRolePolicy(
      new iam.PolicyStatement({
        actions: ['ec2:DescribeImages', 'ec2:RunInstances', 'ec2:CreateTags'],
        resources: ['*'],
      }),
    );
    // Only the seed role, and only to EC2 — so this cannot be used to hand the
    // Lambda's caller a more privileged role.
    deployFn.addToRolePolicy(
      new iam.PolicyStatement({
        actions: ['iam:PassRole'],
        resources: [seedRole.roleArn],
        conditions: { StringEquals: { 'iam:PassedToService': 'ec2.amazonaws.com' } },
      }),
    );

    const startUrl = startFn.addFunctionUrl({ authType: lambda.FunctionUrlAuthType.AWS_IAM });
    const stopUrl = stopFn.addFunctionUrl({ authType: lambda.FunctionUrlAuthType.AWS_IAM });
    const deployUrl = deployFn.addFunctionUrl({ authType: lambda.FunctionUrlAuthType.AWS_IAM });

    new events.Rule(this, 'IdleCheckRule', {
      description: 'Periodic idle check for the vLLM instance',
      schedule: events.Schedule.rate(cdk.Duration.minutes(5)),
      targets: [new targets.LambdaFunction(stopFn)],
    });

    new cdk.CfnOutput(this, 'StartUrl', { value: startUrl.url });
    new cdk.CfnOutput(this, 'StopUrl', { value: stopUrl.url });
    new cdk.CfnOutput(this, 'DeployUrl', { value: deployUrl.url });
    new cdk.CfnOutput(this, 'BaseUrl', { value: baseUrl });
    new cdk.CfnOutput(this, 'EipAddress', { value: eip.attrPublicIp });
    new cdk.CfnOutput(this, 'ModelId', { value: cfg.modelId });
    new cdk.CfnOutput(this, 'Region', { value: this.region });
    // Consumed by `pnpm seed-model` to launch the disposable seed instance.
    new cdk.CfnOutput(this, 'WeightsBucket', { value: weightsBucket.bucketName });
    // No WeightsPrefix output: the prefix is derived per deployed model and
    // stored in the deploy-config parameter, so a stack-level value would only
    // ever be a guess from the CDK defaults.
    // `pnpm deploy`'s seed step (scripts/seed-deploy-config.mjs) writes this
    // cfg-derived initial config over the parameter's `unconfigured`
    // placeholder the first time, and never overwrites an outfit/manual value.
    new cdk.CfnOutput(this, 'DeployConfigParam', { value: deployConfigParam.parameterName });
    new cdk.CfnOutput(this, 'InitialDeployConfig', { value: JSON.stringify(initialDeployConfig) });
    new cdk.CfnOutput(this, 'SeedInstanceProfileArn', { value: seedProfile.instanceProfileArn });
    new cdk.CfnOutput(this, 'SeedInstanceType', { value: cfg.builderInstanceType });
    new cdk.CfnOutput(this, 'SeedSubnetId', { value: vpc.publicSubnets[0].subnetId });
    new cdk.CfnOutput(this, 'SeedSecurityGroupId', { value: securityGroup.securityGroupId });
    new cdk.CfnOutput(this, 'HfTokenSecretArn', { value: hfSecret?.secretArn ?? '' });
    // `pnpm write-config` reads this from the outputs file to generate
    // remote.json; the stable BaseUrl above becomes the Outfit's BASEURL.
    new cdk.CfnOutput(this, 'OutfitRemoteConfig', {
      value: `{"start_url":"${startUrl.url}","stop_url":"${stopUrl.url}","deploy_url":"${deployUrl.url}","region":"${this.region}"}`,
    });
  }
}
