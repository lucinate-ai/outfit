import * as cdk from 'aws-cdk-lib';
import {
  aws_ec2 as ec2,
  aws_iam as iam,
  aws_imagebuilder as imagebuilder,
  aws_ssm as ssm,
} from 'aws-cdk-lib';
import { Construct } from 'constructs';
import {
  AMI_ROLE_TAG_KEY,
  AMI_ROLE_TAG_VALUE,
  AMI_RUNNER_TAG_KEY,
  type LlmConfig,
} from './config';

export interface ImageStackProps extends cdk.StackProps {
  config: LlmConfig;
}

// A plain Ubuntu 24.04 base — the bake adds only the NVIDIA driver and the
// chosen runner, so each AMI stays small and snapshots quickly.
const UBUNTU_SSM_PARAMETER =
  '/aws/service/canonical/ubuntu/server/24.04/stable/current/amd64/hvm/ebs-gp3/ami-id';

// Root device of the Ubuntu 24.04 AMI (verify with describe-images if a bake
// adds a second volume instead of resizing the root).
const AMI_ROOT_DEVICE = '/dev/sda1';

// Per-runner recipe/component version. Image Builder treats a version as
// immutable, so bump a runner's version to force a fresh AMI for just it.
const RUNNER_VERSION = { vllm: '3.3.2', llamacpp: '3.3.2' } as const;

/**
 * Bakes a slim, model-agnostic AMI **per runner** — vLLM (a `uv` venv) and
 * llama.cpp (a prebuilt CUDA `llama-server` from ai-dock) — each on the NVIDIA
 * driver, no Docker. The model is NOT baked in; it lives in S3 and is synced at
 * boot. Each runner has its own recipe/pipeline and its AMIs are tagged with
 * the runner, so the runtime picks the right one. Defines Image Builder
 * *pipelines*, so deploying never runs a build; trigger one with `pnpm bake`.
 */
export class ImageStack extends cdk.Stack {
  constructor(scope: Construct, id: string, props: ImageStackProps) {
    super(scope, id, props);
    const cfg = props.config;

    // Fail fast at synth/deploy, not 20 minutes into a bake. outfitVersion
    // defaults to the latest git release tag (see config.ts); it comes back
    // empty when this stack is deployed from a checkout that reaches no tag
    // (e.g. a feature branch, or one without tags fetched). The bake would
    // then curl a bogus /download/v/ URL — so refuse to build a pipeline that
    // bakes an empty version. Pass -c outfitVersion=<x.y.z> to override.
    if (!cfg.outfitVersion) {
      throw new Error(
        'outfitVersion is empty: deploy the image stack from a checkout that reaches a release tag, ' +
          'or pass -c outfitVersion=<x.y.z>',
      );
    }

    // Minimal public VPC for the transient builder — internet + SSM, no NAT.
    // Pinned to the first configured AZ so it doesn't default to us-east-1a,
    // which lacks the builder instance type for this account.
    const vpc = new ec2.Vpc(this, 'BuilderVpc', {
      availabilityZones: [cfg.availabilityZones[0]],
      natGateways: 0,
      restrictDefaultSecurityGroup: false,
      subnetConfiguration: [{ name: 'public', subnetType: ec2.SubnetType.PUBLIC }],
    });
    const builderSg = new ec2.SecurityGroup(this, 'BuilderSg', {
      vpc,
      description: 'Image Builder bake instance - outbound only',
      allowAllOutbound: true,
    });

    const builderRole = new iam.Role(this, 'BuilderRole', {
      assumedBy: new iam.ServicePrincipal('ec2.amazonaws.com'),
      managedPolicies: [
        iam.ManagedPolicy.fromAwsManagedPolicyName('EC2InstanceProfileForImageBuilder'),
        iam.ManagedPolicy.fromAwsManagedPolicyName('AmazonSSMManagedInstanceCore'),
      ],
    });
    const builderProfile = new iam.CfnInstanceProfile(this, 'BuilderProfile', {
      roles: [builderRole.roleName],
    });

    // One infrastructure config shared by both runners' pipelines.
    const infrastructure = new imagebuilder.CfnInfrastructureConfiguration(this, 'Infra', {
      name: `${this.stackName}-infra`,
      instanceProfileName: builderProfile.ref,
      instanceTypes: [cfg.builderInstanceType],
      subnetId: vpc.publicSubnets[0].subnetId,
      securityGroupIds: [builderSg.securityGroupId],
      terminateInstanceOnFailure: true,
    });

    const parentImage = ssm.StringParameter.valueForStringParameter(this, UBUNTU_SSM_PARAMETER);
    const nvidiaParam = { name: 'NvidiaDriverPackage', value: [cfg.nvidiaDriverPackage] };
    const outfitParam = { name: 'OutfitVersion', value: [cfg.outfitVersion] };

    const runnerBuilds = [
      {
        runner: 'vllm',
        data: vllmComponentDoc(),
        parameters: [{ name: 'VllmVersion', value: [cfg.vllmVersion] }, nvidiaParam, outfitParam],
      },
      {
        runner: 'llamacpp',
        data: llamacppComponentDoc(),
        parameters: [{ name: 'LlamacppRelease', value: [cfg.llamacppRelease] }, nvidiaParam, outfitParam],
      },
    ];

    for (const build of runnerBuilds) {
      const component = new imagebuilder.CfnComponent(this, `${build.runner}Component`, {
        name: `${this.stackName}-${build.runner}-bake`,
        platform: 'Linux',
        version: RUNNER_VERSION[build.runner as keyof typeof RUNNER_VERSION],
        data: build.data,
      });

      const recipe = new imagebuilder.CfnImageRecipe(this, `${build.runner}Recipe`, {
        name: `${this.stackName}-${build.runner}-recipe`,
        version: RUNNER_VERSION[build.runner as keyof typeof RUNNER_VERSION],
        parentImage,
        components: [{ componentArn: component.attrArn, parameters: [...build.parameters] }],
        blockDeviceMappings: [
          {
            deviceName: AMI_ROOT_DEVICE,
            ebs: {
              volumeSize: cfg.imageVolumeGb,
              volumeType: 'gp3',
              deleteOnTermination: true,
              encrypted: true,
            },
          },
        ],
      });

      // Tag each AMI with the role the runtime filters on AND the runner it was
      // baked for, so the two runners' AMIs never collide under one tag.
      const distribution = new imagebuilder.CfnDistributionConfiguration(
        this,
        `${build.runner}Distribution`,
        {
          name: `${this.stackName}-${build.runner}-distribution`,
          distributions: [
            {
              region: this.region,
              amiDistributionConfiguration: {
                Name: `cloud-vm-llm-${build.runner}-{{ imagebuilder:buildDate }}`,
                AmiTags: {
                  [AMI_ROLE_TAG_KEY]: AMI_ROLE_TAG_VALUE,
                  [AMI_RUNNER_TAG_KEY]: build.runner,
                },
              },
            },
          ],
        },
      );

      const pipeline = new imagebuilder.CfnImagePipeline(this, `${build.runner}Pipeline`, {
        name: `${this.stackName}-${build.runner}-pipeline`,
        imageRecipeArn: recipe.attrArn,
        infrastructureConfigurationArn: infrastructure.attrArn,
        distributionConfigurationArn: distribution.attrArn,
        // No GPU test — the builder has no GPU; the driver is validated at
        // runtime and the DKMS build is checked in the bake.
        imageTestsConfiguration: { imageTestsEnabled: false },
        status: 'ENABLED',
      });

      new cdk.CfnOutput(this, `${build.runner}PipelineName`, { value: pipeline.name! });
    }
  }
}

/**
 * Shared bake preamble: grow the root fs, install build tools + the AWS CLI,
 * and the NVIDIA driver (open kernel modules, required for Ada/L40S). Both
 * runners need the driver; the DKMS build needs build-essential + headers.
 */
function commonPreamble(): string {
  return `              set -euxo pipefail
              export DEBIAN_FRONTEND=noninteractive

              ROOT_SRC=$(findmnt -no SOURCE /)
              ROOT_DISK=$(lsblk -no PKNAME "$ROOT_SRC")
              ROOT_PARTNUM=$(echo "$ROOT_SRC" | grep -oE '[0-9]+$')
              growpart "/dev/$ROOT_DISK" "$ROOT_PARTNUM" || true
              resize2fs "$ROOT_SRC" || true
              df -h /

              apt-get update
              apt-get install -y build-essential "linux-headers-$(uname -r)" \\
                curl ca-certificates gnupg unzip

              # AWS CLI v2 — the runtime uses it to sync weights from S3 and read
              # the API key from Secrets Manager.
              curl -fsSL https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip -o /tmp/awscliv2.zip
              unzip -q /tmp/awscliv2.zip -d /tmp
              /tmp/aws/install
              aws --version

              # NVIDIA driver. The host needs only the driver; the runner brings
              # its own CUDA (vLLM's torch wheels, or the ai-dock llama-server
              # tarball). The DKMS module loads on the GPU instance at boot.
              curl -fsSL https://developer.download.nvidia.com/compute/cuda/repos/ubuntu2404/x86_64/cuda-keyring_1.1-1_all.deb -o /tmp/cuda-keyring.deb
              dpkg -i /tmp/cuda-keyring.deb
              apt-get update
              apt-get install -y {{ NvidiaDriverPackage }}
              dkms status | grep -i nvidia

              # CloudWatch agent — ships the engine and boot logs to CloudWatch,
              # so a crash on an ephemeral instance survives its termination.
              # Baked here (avoids an install on every cold start); its config
              # carries the environment name, so it is written and started at
              # boot, not enabled here.
              curl -fsSL https://amazoncloudwatch-agent.s3.amazonaws.com/ubuntu/amd64/latest/amazon-cloudwatch-agent.deb -o /tmp/amazon-cloudwatch-agent.deb
              dpkg -i -E /tmp/amazon-cloudwatch-agent.deb
              test -x /opt/aws/amazon-cloudwatch-agent/bin/amazon-cloudwatch-agent-ctl

              # Bound the engine log file on disk: the CloudWatch agent only
              # tails it, so without rotation a crash loop could fill the root
              # volume. copytruncate because the engine holds the file open in
              # append mode; a size trigger on a 15-min timer (not the daily
              # default) so a chatty engine can't outrun it. The boot log is
              # deliberately excluded — it is written once and stays small.
              # The engine log lives under the daemon's pinned config dir
              # (OUTFIT_CONFIG_DIR=/var/lib/outfit — see lambda/runners/daemon-boot.ts);
              # keep this path in step with DAEMON_CONFIG_DIR there.
              apt-get install -y logrotate
              mkdir -p /etc/llm /var/log/llm /var/lib/outfit/daemon
              cat >/etc/llm/logrotate.conf <<'LOGROTATE'
              /var/lib/outfit/daemon/engine.log {
                  size 200M
                  rotate 2
                  compress
                  missingok
                  notifempty
                  copytruncate
              }
              LOGROTATE
              cat >/etc/systemd/system/llm-logrotate.service <<'UNIT'
              [Unit]
              Description=Rotate llm engine logs
              [Service]
              Type=oneshot
              ExecStart=/usr/sbin/logrotate /etc/llm/logrotate.conf
              UNIT
              cat >/etc/systemd/system/llm-logrotate.timer <<'UNIT'
              [Unit]
              Description=Rotate llm engine logs every 15 minutes
              [Timer]
              OnBootSec=15min
              OnUnitActiveSec=15min
              [Install]
              WantedBy=timers.target
              UNIT
              systemctl enable llm-logrotate.timer

              # outfit itself — the daemon that hosts the engine and answers the
              # control Lambdas over its loopback API. A pinned release,
              # checksum-verified against the release's own manifest. The version
              # defaults to the latest git release tag; if it could not be
              # resolved (no tags on the build machine) fail here with a clear
              # message rather than curling a bogus /download/v/ URL.
              OUTFIT_VERSION='{{ OutfitVersion }}'
              test -n "$OUTFIT_VERSION" || { echo "outfitVersion unresolved — publish a release (git tag) or bake with -c outfitVersion=<x.y.z>" >&2; exit 1; }
              OUTFIT_URL="https://github.com/lucinate-ai/outfit/releases/download/v$OUTFIT_VERSION"
              mkdir -p /tmp/outfit-dl
              curl -fsSL "$OUTFIT_URL/outfit_linux_amd64.tar.gz" -o /tmp/outfit-dl/outfit_linux_amd64.tar.gz
              curl -fsSL "$OUTFIT_URL/checksums.txt" -o /tmp/outfit-dl/checksums.txt
              (cd /tmp/outfit-dl && grep ' outfit_linux_amd64.tar.gz$' checksums.txt | sha256sum -c -)
              tar -xzf /tmp/outfit-dl/outfit_linux_amd64.tar.gz -C /tmp/outfit-dl
              install -m 0755 /tmp/outfit-dl/outfit /usr/local/bin/outfit
              /usr/local/bin/outfit version
              rm -rf /tmp/outfit-dl

              # Crash nudge: the daemon reports a crashed engine but never
              # restarts it (that is outfit's contract), so a baked timer asks
              # for a start when — and only when — status says crashed. A
              # deliberate stop stays stopped. Enabled by the boot script once
              # the daemon's unit exists.
              cat >/usr/local/bin/outfit-nudge <<'SCRIPT'
              #!/bin/sh
              # Only a crashed engine is nudged; a deliberate stop stays stopped.
              curl -s --max-time 5 http://127.0.0.1:4242/v1/status | grep -q '"state":"crashed"' || exit 0
              curl -s --max-time 15 -X POST http://127.0.0.1:4242/v1/start
              SCRIPT
              chmod 0755 /usr/local/bin/outfit-nudge
              cat >/etc/systemd/system/outfit-nudge.service <<'UNIT'
              [Unit]
              Description=Restart a crashed engine via the outfit daemon
              [Service]
              Type=oneshot
              ExecStart=/usr/local/bin/outfit-nudge
              UNIT
              cat >/etc/systemd/system/outfit-nudge.timer <<'UNIT'
              [Unit]
              Description=Check for a crashed engine every 30 seconds
              [Timer]
              OnBootSec=2min
              OnUnitActiveSec=30s
              [Install]
              WantedBy=timers.target
              UNIT`;
}

const CLEANUP = `              apt-get clean
              rm -rf /var/lib/apt/lists/* /tmp/aws /tmp/awscliv2.zip /tmp/cuda-keyring.deb /tmp/amazon-cloudwatch-agent.deb
              echo "bake complete"`;

/** vLLM AMI: the driver + a vLLM venv via uv (no Docker, no model). */
function vllmComponentDoc(): string {
  return `name: cloud-vm-llm-vllm-bake
description: Slim AMI with the NVIDIA driver and vLLM (via uv), model-agnostic
schemaVersion: 1.0
parameters:
  - VllmVersion:
      type: string
  - NvidiaDriverPackage:
      type: string
  - OutfitVersion:
      type: string
phases:
  - name: build
    steps:
      - name: Bake
        action: ExecuteBash
        inputs:
          commands:
            - |
${commonPreamble()}

              # python3.12-dev (Python.h) is a runtime need: Qwen3.6's
              # linear-attention path imports Triton, which JIT-compiles a CUDA
              # stub against the Python headers on first load.
              apt-get install -y python3.12 python3.12-venv python3.12-dev

              # uv + vLLM into a venv baked into the AMI.
              curl -LsSf https://astral.sh/uv/install.sh | env UV_INSTALL_DIR=/usr/local/bin sh
              /usr/local/bin/uv venv --python 3.12 /opt/llm/venv
              /usr/local/bin/uv pip install --python /opt/llm/venv "vllm=={{ VllmVersion }}"
              # Verify via package metadata — do NOT run "vllm --version", which
              # probes for a GPU and fails on the CPU-only builder.
              /opt/llm/venv/bin/python -c "import importlib.metadata as m; print('vllm', m.version('vllm'))"
              # The daemon launches the engine as plain "vllm" — put the venv's
              # entrypoint on the PATH so no venv path leaks into outfit.
              ln -sf /opt/llm/venv/bin/vllm /usr/local/bin/vllm

${CLEANUP}
`;
}

/**
 * llama.cpp AMI: the driver + a prebuilt CUDA `llama-server` from
 * ai-dock/llama.cpp-cuda (tracks upstream; CUDA 12.8). No toolkit, no build —
 * just download, extract, and install the binary plus its bundled libs.
 */
function llamacppComponentDoc(): string {
  return `name: cloud-vm-llm-llamacpp-bake
description: Slim AMI with the NVIDIA driver and a prebuilt CUDA llama-server
schemaVersion: 1.0
parameters:
  - LlamacppRelease:
      type: string
  - NvidiaDriverPackage:
      type: string
  - OutfitVersion:
      type: string
phases:
  - name: build
    steps:
      - name: Bake
        action: ExecuteBash
        inputs:
          commands:
            - |
${commonPreamble()}

              # CUDA runtime libraries the prebuilt llama-server links against.
              # The ai-dock tarball ships the llama.cpp/ggml libs but NOT the CUDA
              # runtime; the driver provides libcuda, these provide
              # libcudart/libcublas + NCCL (from the cuda repo added above).
              apt-get install -y cuda-cudart-12-8 libcublas-12-8 libnccl2

              # Prebuilt CUDA llama-server (ai-dock/llama.cpp-cuda, CUDA 12.8).
              # Install the binary and every shared lib it ships beside it, then
              # register the path so the runtime resolver finds them.
              mkdir -p /opt/llm/bin /tmp/llama
              REL='{{ LlamacppRelease }}'
              curl -fsSL "https://github.com/ai-dock/llama.cpp-cuda/releases/download/$REL/llama.cpp-$REL-cuda-12.8-amd64.tar.gz" -o /tmp/llama.tar.gz
              tar -xzf /tmp/llama.tar.gz -C /tmp/llama
              SRV=$(find /tmp/llama -type f -name llama-server | head -1)
              test -n "$SRV"
              install -D -m 0755 "$SRV" /opt/llm/bin/llama-server
              find "$(dirname "$SRV")" -name '*.so*' -exec cp -a {} /opt/llm/bin/ \\;
              echo /opt/llm/bin > /etc/ld.so.conf.d/llm.conf
              ldconfig
              ln -sf /opt/llm/bin/llama-server /usr/local/bin/llama-server
              # Verify it is an ELF binary (no GPU on the builder to run it).
              file /opt/llm/bin/llama-server | grep -q ELF

              rm -rf /tmp/llama /tmp/llama.tar.gz
${CLEANUP}
`;
}
