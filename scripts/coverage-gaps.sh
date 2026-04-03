#!/usr/bin/env bash
# coverage-gaps.sh — Identify functions below a coverage threshold
#
# Usage:
#   scripts/coverage-gaps.sh [threshold]
#
# Arguments:
#   threshold  Minimum acceptable coverage % (default: 70)
#
# Output:
#   Table of functions below threshold, sorted by coverage ascending.
#   Exit code 1 if any gaps found, 0 otherwise.

set -euo pipefail

THRESHOLD="${1:-70}"
COVER_PROFILE="${COVER_PROFILE:-coverage.out}"

# Run tests with coverage if profile doesn't exist or is stale
if [[ ! -f "$COVER_PROFILE" ]] || [[ -n "$(find . -name '*.go' -newer "$COVER_PROFILE" 2>/dev/null | head -1)" ]]; then
    go test ./... -coverprofile="$COVER_PROFILE" -covermode=atomic > /dev/null 2>&1
fi

# Parse coverage output
FUNC_OUTPUT=$(go tool cover -func="$COVER_PROFILE")

# Extract per-package summaries
echo "## Package Coverage"
echo ""
echo "$FUNC_OUTPUT" | grep "^total:" | awk '{printf "  Total: %s\n", $NF}'
echo ""

# Show per-package totals from test output
go test ./... -cover 2>/dev/null | while IFS= read -r line; do
    if [[ "$line" == ok* ]] && [[ "$line" == *"coverage:"* ]]; then
        pkg=$(echo "$line" | awk '{print $2}')
        cov=$(echo "$line" | grep -oE '[0-9]+\.[0-9]+%')
        printf "  %-50s %s\n" "$pkg" "${cov:-0.0%}"
    elif [[ "$line" == *"[no test files]"* ]]; then
        pkg=$(echo "$line" | awk '{print $2}')
        printf "  %-50s %s\n" "$pkg" "no tests"
    fi
done

echo ""

# Find functions below threshold
GAPS=$(echo "$FUNC_OUTPUT" \
    | grep -v "^total:" \
    | awk -v thresh="$THRESHOLD" '{
        pct = $NF
        gsub(/%/, "", pct)
        if (pct + 0 < thresh + 0) {
            printf "  %-55s %-30s %s\n", $1, $2, $NF
        }
    }')

if [[ -z "$GAPS" ]]; then
    echo "## Gaps: None"
    echo "  All functions at or above ${THRESHOLD}% coverage."
    exit 0
fi

GAP_COUNT=$(echo "$GAPS" | wc -l | tr -d ' ')

echo "## Gaps: ${GAP_COUNT} functions below ${THRESHOLD}%"
echo ""
printf "  %-55s %-30s %s\n" "FILE" "FUNCTION" "COVERAGE"
printf "  %-55s %-30s %s\n" "----" "--------" "--------"
# Sort by coverage ascending (numeric on last field)
echo "$GAPS" | sort -t'%' -k1 -n | while IFS= read -r line; do
    echo "$line"
done
echo ""

# Categorize gaps
echo "## Gap Analysis"
echo ""

ZERO_FUNCS=$(echo "$FUNC_OUTPUT" \
    | grep -v "^total:" \
    | awk '{if ($NF == "0.0%") print "  - " $1 " " $2}')

if [[ -n "$ZERO_FUNCS" ]]; then
    echo "  ### Untested (0.0%)"
    echo "$ZERO_FUNCS"
    echo ""
fi

PARTIAL_FUNCS=$(echo "$FUNC_OUTPUT" \
    | grep -v "^total:" \
    | awk -v thresh="$THRESHOLD" '{
        pct = $NF; gsub(/%/, "", pct)
        if (pct + 0 > 0 && pct + 0 < thresh + 0) {
            printf "  - %s %s (%s)\n", $1, $2, $NF
        }
    }')

if [[ -n "$PARTIAL_FUNCS" ]]; then
    echo "  ### Partially covered (1-${THRESHOLD}%)"
    echo "$PARTIAL_FUNCS"
    echo ""
fi

exit 1
