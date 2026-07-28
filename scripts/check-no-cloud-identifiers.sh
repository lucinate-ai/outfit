#!/usr/bin/env bash
# Fails when anything tracked in this repository looks like a real cloud
# identifier. This repository is PUBLIC: account numbers, ARNs, endpoint
# hostnames, allocated addresses, bucket names and resource ids belong in a
# developer's working tree (gitignored) or in the deployment's own outputs —
# never in a commit.
#
# Examples in docs and tests must use reserved documentation values instead:
#   addresses  198.51.100.0/24, 203.0.113.0/24, 192.0.2.0/24 (RFC 5737)
#   accounts   an obviously fake id — twelve zeroes, or a single digit
#   hostnames  an elided or example host (example.com, <endpoint>)
#
# Usage: scripts/check-no-cloud-identifiers.sh [ref]
# With no argument it scans the tracked working tree.
set -uo pipefail

cd "$(dirname "$0")/.."

# name=pattern. Kept deliberately narrow so ordinary prose does not trip it.
patterns=(
  "AWS account id=[^0-9A-Za-z][0-9]{12}[^0-9A-Za-z]"
  "AWS ARN=arn:aws[a-z-]*:[a-z0-9-]+:[a-z0-9-]*:[0-9]{12}:"
  "Lambda Function URL host=[a-z0-9]{20,}\.lambda-url\."
  "EC2/EBS/VPC resource id=\b(i|vol|subnet|sg|vpc|ami|eni|snap)-[0-9a-f]{8,}\b"
  "S3 bucket with a generated suffix=[a-z0-9-]*[0-9a-f]{8}-[a-z0-9]{12,}"
)

# Reserved documentation ranges are the sanctioned stand-ins, so an address is
# only reported when it is outside them.
doc_ranges='(198\.51\.100\.|203\.0\.113\.|192\.0\.2\.|127\.0\.0\.|0\.0\.0\.0|10\.|192\.168\.|169\.254\.)'

status=0
# Lock files are nothing but hashes and pseudo-versions, which no pattern can
# tell from a generated resource name, and they never carry cloud identifiers.
files=$(git ls-files | grep -vE '(^|/)(go\.sum|go\.mod|pnpm-lock\.yaml|package-lock\.json)$')

report() {
  if [ "$status" -eq 0 ]; then
    echo "Cloud identifiers must not be committed to this public repository." >&2
    echo >&2
  fi
  status=1
  echo "$1" >&2
}

for entry in "${patterns[@]}"; do
  name=${entry%%=*}
  pattern=${entry#*=}
  # shellcheck disable=SC2086
  if hits=$(echo "$files" | xargs grep -nEI "$pattern" 2>/dev/null); then
    report "$name:"
    echo "$hits" | sed 's/^/  /' >&2
  fi
done

# A public IPv4 literal that is not a documentation/private address.
if hits=$(echo "$files" | xargs grep -nEI '\b([0-9]{1,3}\.){3}[0-9]{1,3}\b' 2>/dev/null |
  grep -vE "$doc_ranges" | grep -vE '\b(0|1|2)\.[0-9]+\.[0-9]+\.[0-9]+\b'); then
  report "Public IP address (use a documentation range from RFC 5737):"
  echo "$hits" | sed 's/^/  /' >&2
fi

if [ "$status" -ne 0 ]; then
  echo >&2
  echo "Replace them with the documentation values listed at the top of $0." >&2
  exit 1
fi

echo "No cloud identifiers found in tracked files."
