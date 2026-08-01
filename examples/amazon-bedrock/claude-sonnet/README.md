# Claude 3.5 Sonnet on AWS Bedrock

Point opencode at Anthropic's Claude running on [AWS Bedrock](https://aws.amazon.com/bedrock/),
using the [`Outfit`](Outfit) in this directory. Bedrock authenticates with your
**AWS credentials**, so there is no API key to set — `outfit` injects none.

## Prerequisites

- An AWS account with Bedrock enabled in your region, and **model access**
  granted for the Claude model you want (request it once in the Bedrock console
  under *Model access*).
- AWS credentials the SDK can find, via any of the usual sources:
  - `aws configure` (a profile in `~/.aws`), then optionally `AWS_PROFILE`,
  - environment credentials (`AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`,
    plus `AWS_SESSION_TOKEN` for temporary creds),
  - or an SSO session (`aws sso login`).
- IAM permission to invoke the model (`bedrock:InvokeModel` and, for streaming,
  `bedrock:InvokeModelWithResponseStream`).

## 1. Choose a region

Bedrock is regional, and model availability differs by region. The provider
defaults to `us-east-1`; override it for this shell with `AWS_REGION`:

```sh
export AWS_REGION=eu-west-2
```

## 2. Apply the Outfit

```sh
outfit apply examples/amazon-bedrock/claude-sonnet/Outfit
# or, from this directory:
outfit apply
```

The Outfit is:

```dockerfile
PROVIDER amazon-bedrock
MODEL    anthropic.claude-3-5-sonnet-20241022-v2:0
CONTEXT  200k
OUTPUT   8k
```

- `MODEL` is the Bedrock model id opencode calls — so use the exact id, not a
  nickname. Swap in another the account has access to, e.g.
  `anthropic.claude-3-5-haiku-20241022-v1:0`. Some models are only reachable
  through a cross-region *inference profile*, whose id is prefixed with the
  region group (e.g. `us.anthropic.claude-3-5-sonnet-20241022-v2:0`).
- `CONTEXT`/`OUTPUT` set opencode's context window and output cap.

No key is written anywhere: the AWS SDK resolves your credentials at run time.

## 3. Run it

```sh
opencode
```

Select `amazon-bedrock/anthropic.claude-3-5-sonnet-20241022-v2:0`. If a call fails with an access or
credentials error, check that your AWS credentials are active
(`aws sts get-caller-identity`), the region is right, and model access is granted.
