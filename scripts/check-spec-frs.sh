#!/usr/bin/env sh
# Verifies that every FR-xxx defined in docs/SPEC.md (and every FR with acceptance
# criteria) has at least one automated test referencing its ID (Phase 13 Task 3).
set -eu

SPEC_FILE="${1:-docs/SPEC.md}"

if [ ! -f "$SPEC_FILE" ]; then
  echo "error: spec file not found: $SPEC_FILE" >&2
  exit 1
fi

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

# Extract all defined FR-xxx headings from docs/SPEC.md (strictly excluding NFR-xxx).
awk '
  /\*\*FR-[0-9]{3}/ {
    if (match($0, /FR-[0-9]{3}/)) {
      cur = substr($0, RSTART, RLENGTH)
      print cur > all_file
    }
    next
  }
  /^- AC:/ && cur != "" {
    print cur > ac_file
  }
  /^### / || /^## / {
    cur = ""
  }
' all_file="$tmpdir/all_frs.txt" ac_file="$tmpdir/ac_frs.txt" "$SPEC_FILE"

sort -u "$tmpdir/all_frs.txt" -o "$tmpdir/all_frs.txt"
sort -u "$tmpdir/ac_frs.txt" -o "$tmpdir/ac_frs.txt"

# Collect every FR-xxx referenced in Go, Vitest, or Playwright test files.
{
  find internal cmd -type f -name '*_test.go'
  find web/src -type f \( -name '*.test.ts' -o -name '*.test.tsx' \)
  find web/e2e -type f -name '*.spec.ts'
} | sort | while IFS= read -r f; do
  grep -oE '(^|[^A-Z])FR-[0-9]{3}' "$f" | grep -oE 'FR-[0-9]{3}' || true
done | sort -u > "$tmpdir/tested_frs.txt"

missing_ac=$(comm -23 "$tmpdir/ac_frs.txt" "$tmpdir/tested_frs.txt")
missing_all=$(comm -23 "$tmpdir/all_frs.txt" "$tmpdir/tested_frs.txt")

total_all=$(wc -l < "$tmpdir/all_frs.txt" | tr -d ' ')
total_ac=$(wc -l < "$tmpdir/ac_frs.txt" | tr -d ' ')
total_tested=$(comm -12 "$tmpdir/all_frs.txt" "$tmpdir/tested_frs.txt" | wc -l | tr -d ' ')

echo "Spec FR coverage: ${total_tested}/${total_all} total FRs referenced in tests (${total_ac} with explicit AC)"

if [ -n "$missing_ac" ]; then
  echo "FAIL: FRs with acceptance criteria missing test references:" >&2
  echo "$missing_ac" >&2
  exit 1
fi

if [ -n "$missing_all" ]; then
  echo "FAIL: FRs missing test references:" >&2
  echo "$missing_all" >&2
  exit 1
fi

echo "PASS: all ${total_all} FRs in ${SPEC_FILE} are referenced by automated tests."
