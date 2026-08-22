import { describe, expect, it, vi } from 'vitest';
import type { RunInstancesCommandInput } from '@aws-sdk/client-ec2';

// The runtime instance's root volume: launched as the AMI's root, with the
// gp3 throughput provisioned so the weights sync and the daemon's page-cache
// prewarm run at the volume's real ceiling instead of the 125 MiB/s baseline.
// The EC2 client is stubbed so no call leaves the process.

const { send } = vi.hoisted(() => ({ send: vi.fn() }));

vi.mock('@aws-sdk/client-ec2', () => {
  // aws.ts news these, so each must be a class, not a factory.
  const command =
    (cmdName: string) =>
    class {
      name = cmdName;
      constructor(readonly input: unknown) {}
    };
  return {
    EC2Client: vi.fn(() => ({ send })),
    AssociateAddressCommand: command('AssociateAddressCommand'),
    CreateTagsCommand: command('CreateTagsCommand'),
    DescribeImagesCommand: command('DescribeImagesCommand'),
    DescribeInstancesCommand: command('DescribeInstancesCommand'),
    RunInstancesCommand: command('RunInstancesCommand'),
    StartInstancesCommand: command('StartInstancesCommand'),
    StopInstancesCommand: command('StopInstancesCommand'),
    TerminateInstancesCommand: command('TerminateInstancesCommand'),
  };
});

import { findLatestAmi, runInstance } from '../lambda/shared/aws';

send.mockImplementation((cmd: { name: string; input: unknown }) => {
  if (cmd.name === 'DescribeImagesCommand') {
    return {
      Images: [
        {
          State: 'available',
          ImageId: 'ami-test1',
          CreationDate: '2026-08-01T00:00:00.000Z',
          BlockDeviceMappings: [{ DeviceName: '/dev/sda1', Ebs: { VolumeSize: 80, VolumeType: 'gp3' } }],
        },
      ],
    };
  }
  return { Instances: [{ InstanceId: 'i-test' }] };
});

const spec = {
  imageId: 'ami-test1',
  instanceType: 'g6e.xlarge',
  subnetId: 'subnet-test',
  securityGroupId: 'sg-test',
  instanceProfileArn: 'arn:aws:iam::0:instance-profile/test',
  userData: '#!/bin/bash\necho boot',
  tags: { Name: 'test' },
};

const runInstancesInput = (): RunInstancesCommandInput =>
  (send.mock.calls.at(-1)?.[0] as { input: RunInstancesCommandInput }).input;

describe('runInstance root volume', () => {
  it('provisions the gp3 throughput when a root volume is requested', async () => {
    await runInstance({ ...spec, rootVolume: { volumeSize: 80, throughput: 1000 } });
    const input = runInstancesInput();
    expect(input.BlockDeviceMappings).toEqual([
      {
        DeviceName: '/dev/sda1',
        Ebs: { VolumeSize: 80, VolumeType: 'gp3', Encrypted: true, Throughput: 1000 },
      },
    ]);
  });

  it('carries provisioned IOPS when given them', async () => {
    await runInstance({ ...spec, rootVolume: { volumeSize: 80, iops: 6000, throughput: 1000 } });
    const ebs = runInstancesInput().BlockDeviceMappings?.[0]?.Ebs;
    expect(ebs?.Iops).toBe(6000);
    expect(ebs?.Throughput).toBe(1000);
  });

  it('launches the AMI root as-is when no root volume is requested', async () => {
    await runInstance(spec);
    expect(runInstancesInput().BlockDeviceMappings).toBeUndefined();
  });
});

describe('findLatestAmi root size', () => {
  it('reads the AMI root volume size off its own block device mapping', async () => {
    const ami = await findLatestAmi([]);
    expect(ami).toEqual({ imageId: 'ami-test1', rootVolumeSizeGb: 80 });
  });

  it('reports no root size for an AMI with no readable mapping', async () => {
    send.mockImplementationOnce(() => ({
      Images: [{ State: 'available', ImageId: 'ami-test2', CreationDate: '2026-08-01T00:00:00.000Z' }],
    }));
    const ami = await findLatestAmi([]);
    expect(ami).toEqual({ imageId: 'ami-test2', rootVolumeSizeGb: 0 });
  });
});
