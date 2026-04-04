# Test Audit Report -- agent-pool v0.5 (branch scope)

**Branch:** `feat/v0.5-concierge-plugin` vs `main`
**Date:** 2026-04-04
**Scope:** Files changed on branch only

## Summary

- **Source files changed:** 5 | **Test files changed:** 2 | **Ratio:** 2.5:1
- **Package coverage:** `internal/mcp` 78.0%, `internal/expert` 84.8%
- **New functions:** All have tests (none at 0%)
- **Uncovered paths:** 13 (high risk: 2, medium: 4, low: 3, skip: 4)
- **Quality issues:** 4 (P0: 0, P1: 1, P2: 3)

---

## Coverage Gaps by Risk

### High Risk

| Function | File:Line | Coverage | Classification | Uncovered Path | Why It Matters |
|----------|-----------|----------|---------------|----------------|----------------|
| `pollForCompletion` | `concierge_tools.go:150` | 75.0% | I/O BOUNDARY | Timeout (context deadline) | Primary failure mode when daemon is down or expert hangs |
| `pollForCompletion` | `concierge_tools.go:150` | 75.0% | STATE MACHINE | `StatusCancelled` branch | Real operational path; error includes cancel note |

### Medium Risk

| Function | File:Line | Coverage | Classification | Uncovered Path | Why It Matters |
|----------|-----------|----------|---------------|----------------|----------------|
| `handleAskExpert` | `concierge_tools.go:73` | 84.6% | I/O BOUNDARY | `os.WriteFile` error | Filesystem full/permissions; user gets opaque error |
| `handleSubmitPlan` | `concierge_tools.go:222` | 84.0% | I/O BOUNDARY | `os.WriteFile` error | Same filesystem failure class |
| `handleListExperts` | `concierge_tools.go:357` | 78.6% | CONFIGURATION | `config.LoadPool` error | Missing pool.toml is common misconfiguration |
| `readExpertResult` | `concierge_tools.go:210` | 83.3% | I/O BOUNDARY | `expert.ReadLog` error | Missing log file (race with daemon) |

### Low Risk

| Function | File:Line | Coverage | Classification | Uncovered Path | Why It Matters |
|----------|-----------|----------|---------------|----------------|----------------|
| `handleAskExpert` | `concierge_tools.go:73` | 84.6% | I/O BOUNDARY | `mail.Compose` error | Requires invalid message fields; validated upstream |
| `handleSubmitPlan` | `concierge_tools.go:222` | 84.0% | I/O BOUNDARY | `mail.Compose` error | Same -- requires nil/invalid fields |
| `handleListExperts` | `concierge_tools.go:357` | 78.6% | CONFIGURATION | `json.MarshalIndent` error | Impossible with valid Go string slices |

### Not Worth Unit Testing (skipped)

| Function | File:Line | Pattern | Rationale |
|----------|-----------|---------|-----------|
| `RegisterConciergeTools` nil guard | `concierge_tools.go:31` | Defensive check | Single-line guard; callers never pass nil |
| `handleCheckStatus` marshal errors | `concierge_tools.go:287` | Framework glue | `json.MarshalIndent` on `*taskboard.Task` can't fail with valid data |
| `handleSubmitPlan` marshal error | `concierge_tools.go:222` | Framework glue | Same -- valid structs don't fail marshal |
| `cmd/agent-pool/main.go` at 8.7% | `main.go` | Entry point | CLI glue; `parseFlagsFromArgs` (the logic) is at 100% |

---

## Quality Issues

### P0 -- Likely Catching Zero Bugs

None found.

### P1 -- Masking Real Issues

| File | Lines | Pattern | Detail |
|------|-------|---------|--------|
| `concierge_tools_test.go` | 152, 241 | Sleep in test | `time.Sleep(50ms)` in goroutines polling the postoffice. Mitigated by retry loop (polls up to 50 times), but nonzero flake risk on slow CI. |

### P2 -- Test Debt

| File | Lines | Pattern | Detail |
|------|-------|---------|--------|
| `concierge_tools_test.go` | 397-407 | Existence-only assertion | `TestCheckStatus_SingleTask` uses `strings.Contains` for task ID and status. Doesn't validate JSON structure -- a malformed response containing those substrings would pass. |
| `concierge_tools_test.go` | 165-180 | No subtests | `TestAskExpert_MissingParams` tests two cases sequentially. If the first fails, the second never runs. Should use `t.Run` for isolation. |
| `concierge_tools_test.go` | 172, 255, 412+ | `time.Now()` in fixtures | Used for `CreatedAt`/`CompletedAt` in test data. Not a flake risk (no time-dependent assertions) but a determinism code smell -- prefer fixed time constants. |

### P3 -- Notes

| File | Lines | Pattern | Detail |
|------|-------|---------|--------|
| `concierge_tools_test.go` | 109 | Cross-file helper | Reuses `listArchitectToolNames` from `architect_tools_test.go`. Works (same package) but creates implicit coupling. |

---

## Recommended Next Steps

1. **Run `write-tests`** for 2 high-risk gaps -- `pollForCompletion` timeout and cancellation paths. Biggest bang for the buck.
2. **Add `TestListExperts_MissingConfig`** -- don't write pool.toml to temp dir, verify error. Easy win for medium-risk gap.
3. **Refactor `TestAskExpert_MissingParams` into subtests** -- `t.Run("missing_expert", ...)` for isolation. Low effort P2 fix.
4. `make-testable` is NOT needed -- all functions are testable via the MCP server interface.
5. `setup-coverage` is NOT needed -- Makefile already has `test-cover` and `test-gaps` targets.
