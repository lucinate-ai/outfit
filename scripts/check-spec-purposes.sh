#!/usr/bin/env bash
# Fails when a spec's Purpose section is missing, empty, or still a
# placeholder. `openspec validate` checks that the section EXISTS; it cannot
# tell a real purpose from the stub the archive step writes, so five specs sat
# saying "TBD - created by archiving change <id>. Update Purpose after archive."
# for months while validating cleanly. That instruction is addressed to a human
# who never came back; this is what makes it stick.
#
# A Purpose says what the capability is for and why it exists. If there is
# nothing to say beyond the capability's own name, the capability probably does
# not need its own spec.
#
# Usage: scripts/check-spec-purposes.sh
# Scans the main specs, plus the delta specs of unarchived changes. Archived
# changes are history and are deliberately left alone.
set -uo pipefail

cd "$(dirname "$0")/.." || exit 1

# Words that mean "not written yet". None of them belong in a finished Purpose,
# so a match is reported wherever it appears in the section.
placeholder='TBD|TODO|FIXME|created by archiving|Update Purpose after archive'

status=0

report() {
  if [ "$status" -eq 0 ]; then
    echo "Every spec needs a Purpose that says what its capability is for." >&2
    echo >&2
  fi
  status=1
  echo "  $1" >&2
}

# purpose_of prints the body of a file's "## Purpose" section: everything up to
# the next second-level heading, blank lines squeezed out.
purpose_of() {
  awk '
    /^## Purpose[[:space:]]*$/ { inside = 1; next }
    /^## / { inside = 0 }
    inside && NF { print }
  ' "$1"
}

# check_purpose reports on one file that is required to have a filled-in
# Purpose. $2 is the hint shown when the section is missing entirely.
check_purpose() {
  local file=$1 missing=$2 purpose hit
  if ! grep -q '^## Purpose[[:space:]]*$' "$file"; then
    report "$file: $missing"
    return
  fi
  purpose=$(purpose_of "$file")
  if [ -z "$purpose" ]; then
    report "$file: the Purpose section is empty"
    return
  fi
  if hit=$(echo "$purpose" | grep -nEi "$placeholder"); then
    report "$file: the Purpose is still a placeholder:"
    # shellcheck disable=SC2001  # multi-line indent; matches the sibling check
    echo "$hit" | sed 's/^/      /' >&2
  fi
}

# Every main spec needs one.
for spec in $(git ls-files 'openspec/specs/*/spec.md'); do
  check_purpose "$spec" "no ## Purpose section"
done

# A delta needs one only when it introduces a capability, which is exactly when
# no main spec for that capability exists yet — on archive, that delta becomes
# the new spec, and a delta with no Purpose is what mints the stub this check
# exists to prevent. A delta against an existing capability is only editing
# requirements, so it correctly has none.
for delta in $(git ls-files 'openspec/changes/*/specs/*/spec.md' | grep -v '^openspec/changes/archive/'); do
  capability=$(basename "$(dirname "$delta")")
  [ -f "openspec/specs/$capability/spec.md" ] && continue
  check_purpose "$delta" \
    "introduces the new capability '$capability' but has no ## Purpose — it would archive into a stub"
done

if [ "$status" -ne 0 ]; then
  echo >&2
  echo "Write what the capability is for and why it exists — the originating" >&2
  echo "proposal's \"Why\" section is usually the right source." >&2
  exit 1
fi

echo "All spec Purpose sections are filled in."
