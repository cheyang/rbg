# stress-testing-tuning-doc — verification of PR #390 review findings

Reproducible evidence for the findings raised while reviewing
https://github.com/sgl-project/rbg/pull/390 (docs-only PR: 4 new markdown files
under `doc/best-practice/{en,zh}/`, no code changes).

Layers run against the **code under review** (`origin/pr/390` = b38e9d82), plus a
comparison against the **merge target** (`origin/main`) for staleness findings:

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| 1. Static | doc-vs-code consistency: KWOK stage inventory, helm-rendered controller args, referenced paths, stress-client flags, helm value keys, markdownlint | `bash scripts/10-static-checks.sh` |
| 2. Integration | the real toolchain the docs document: `go build ./test/stress/`, `go test ./test/stress/...`, `helm template` with the doc's exact `--set` flags | `bash scripts/20-integration-checks.sh` |
| 3. Live | (skipped this round — see below) | — |

> Test polarity: every automated check is a **contract** test — it asserts the docs
> match the code they document. A red result *is* the confirmed finding; it goes
> green when the author updates the docs. No bug-canaries in this harness.

> **Live layer skipped per the debate-setup instructions** (unit + integration only).
> A probe *did* find a reachable 3-node cluster, so the skip is by instruction, not
> because no cluster was reachable. No cluster state was touched by this harness:
> L1/L2 are read-only w.r.t. the cluster (helm *template* never contacts it).

## Problem premise (P0)

Docs-only PR → premise validation is light by design (per the review pipeline's
docs-only exception), but the claim is still checkable:

| | |
|---|---|
| Claimed task | "doc: add doc best-practice/stress-testing-and-tuning" |
| Linked issue | none (no `fixes/closes` in PR body; PR body is empty) |
| Reported component | documentation |
| Patched component | documentation only (4 new `.md` files) |
| Component match | Yes |
| **Verdict** | **Confirmed** — `doc/best-practice/{en,zh}` has no `09-*` entry at base cb40703a, and the toolchain the docs describe all exists at the PR head (verified: paths FX1, flags FX2, helm keys FX3 all PASS) |
| Evidence | `results/10-static-checks.log` |

## Summary of results

| ID | Claim | Layer | Verdict | Evidence |
|----|-------|-------|---------|----------|
| F1 | Docs' KWOK stage inventory (6 Stages: 2 Node + 4 Pod) is accurate at the PR head but stale against the merge target | 1 | **Confirmed** | `origin/main` adds `pod-ready-blocked` (PR #461, not in base): 5 Pod stages, not 4 — `results/10-static-checks.log` |
| F2 | Summary table cites an "Operation 5 note" about update-latency regression that does not exist; the regression (P50 393→1871.5 ms, P99 770→2692 ms) is never explained | reading | **Confirmed** (reading-based) | en guide L536 vs Operation 5 body; zh guide L530 (`见操作五说明`) vs 操作五 body |
| F3 | Guide's "expected controller startup args" omits `--enable-deprecated-workload-types=true`, which the chart renders by default | 1 | **Confirmed** | `helm template` with the doc's own `--set` flags renders 10 args; doc lists 9 — `results/10-static-checks.log` |
| F4 | Broken/odd bold: `` `/stress-test`**Skill** `` renders bold only on "Skill"; sibling bullets use `**label**` | reading | Confirmed (nit) | en concept L13, zh concept L15 |
| F5 | "`/stress-test` is a Claude Code built-in Skill" — it is a project skill at `.claude/skills/stress-test/`, not built into Claude Code | reading | Confirmed (nit) | en concept L489+ appendix, zh equivalents |
| F6 | "Related Documents" unlinked with stale TODO ("not created yet") although 3 of 4 listed docs exist — matches series-wide convention | reading | Confirmed (nit) | en concept tail; same pattern in 01/03/04 docs |
| F7 | `open /tmp/.../report.html` is macOS-only; prerequisites (helm/kubectl/go/curl) don't cover Linux | reading | Confirmed (nit) | en concept L291/L481, en guide L227 |
| F8 | Concept doc background says sub-resources are "Deployment/LeaderWorkerSet/Service" while the documented flow exercises RoleInstanceSet | reading | Confirmed (nit) | en concept L80+ vs guide's `--lws-roles` note |
| — | markdownlint (CI parity) | 1 | Pass (0 errors) | `results/10-static-checks.log` |
| — | `go build ./test/stress/` + `go test ./test/stress/...` | 2 | Pass | `results/20-integration-checks.log` |
| — | chart renders with doc's `--set` flags; pprof port 6060 + `--enable-pprof=true` present; resources 8/16Gi limits + 100m/256Mi requests | 2 | Pass | `results/20-integration-checks.log` |

What was checked and found **accurate** (worth stating — most of the PR is correct):
every referenced repo path exists; every stress-client flag used in the docs is
defined in `test/stress/main.go`; every `--set` key resolves in
`deploy/helm/rbgs/values.yaml`; the 600-pod expectation (100 RBGs × (1 LWS role ×
size 4 + 2 standalone × 1)) matches `test/stress/templates.go`; the KWOK stage
delays (0/500/1000/500 ms ≈ 1.5 s to Ready) match `kwok-stage.yaml`; the
stage-fast chart + custom overrides + `pod-complete` deletion produce exactly the
6 stages the docs claim **at the PR head**; the args/resources/labels the guide
tells the reader to verify match the chart otherwise.

## Per-finding detail

### F1 — stage inventory stale at merge time
- Mechanism: PR is based on cb40703a. PR #461 (`41e40cb0`, "leave Pod Ready
  condition to kubelet; controller writes only readiness gates") landed after the
  base and added a 5th Pod Stage `pod-ready-blocked` (1000 ms delay, flips Ready
  to False when a readiness gate is False) to `test/stress/templates/kwok-stage.yaml`.
- Both guides' "Verification" blocks and Summary tables (en L75/L532, zh L75/L530)
  and the concept docs' stage tables claim 6 Stages / 4 Pod-related. A reader
  running the guide on merged code sees 7 stages and an unexplained
  `pod-ready-blocked`.
- Observed (checker): `F1/PR head: PASS` (docs correct at base) vs
  `F1/merge target origin/main: FAIL — undocumented: pod-ready-blocked`.
- Fix sketch: add `pod-ready-blocked` (and its gate-aware semantics) to the stage
  tables and the expected `kubectl get stages` output in all four files, and
  update the counts 6→7 / 4→5.

### F3 — expected controller args missing one entry
- Mechanism: `deploy/helm/rbgs/templates/manager/manager.yaml` always renders
  `--enable-deprecated-workload-types={{ ... }}` (default `true` in values.yaml,
  independent of anything the guide sets). The guide's verification sample lists
  only the other 9 args.
- Observed: `helm template` with the guide's exact `--set` flags renders
  `--enable-deprecated-workload-types=true`; the doc's expected list omits it.
- Fix sketch: add `>--enable-deprecated-workload-types=true` to the expected args
  block in both guides (en L149-ish, zh equivalent).

### F2 — dangling cross-reference + unexplained regression (reading-based)
- Both guides' final Summary rows say "Update latency regressed (see Operation 5
  note)" / "Update 延迟回退（见操作五说明）". Operation 5 contains no such note; its
  conclusion is "the tuning goal is met". The raw numbers in the same section show
  update P50 393→1871.5 ms and P99 770→2692 ms after "tuning" — a ~4.7× P50
  regression the prose never addresses, even though the companion concept doc's
  own analysis guidance says update should be fast (<500 ms).
- Fix sketch: add the promised note in Operation 5 (likely cause: higher reconcile
  concurrency + QPS changed in-place update contention), or at least explain the
  trade-off the Summary claims exists.

## Harness-bites check (Step 4)

Applied the proposed doc fixes temporarily (added `pod-ready-blocked` to the en
guide's stage list and `--enable-deprecated-workload-types=true` to its expected
args block), re-ran the checker: **0 failures** (F1/merge-target and F3/args
flipped to PASS). Reverted with `git checkout -- doc/best-practice/`; `git diff
origin/pr/390 -- doc/` is empty — the PR's files are untouched on this branch.
Recorded in `results/harness-bites.log`.

## Live run notes

Not run this round (debate setup: unit + integration layers only). Cluster probe
found 3 Ready nodes (v1.36.2), so a live round is possible later; nothing was
created or mutated on the cluster by this harness.

## Proposed fixes (NOT applied to production here)

- **F1**: document `pod-ready-blocked` and update stage counts (4 files).
- **F2**: add the Operation 5 note the Summary promises (2 files).
- **F3**: add `--enable-deprecated-workload-types=true` to expected args (2 files).
- **F4-F8**: optional polish — bold the full `/stress-test Skill` label; rephrase
  "built-in" to "project-provided (`.claude/skills/stress-test`)"; link the 3
  existing related docs; mention `xdg-open` for Linux; align the background
  section's sub-resource list with RoleInstanceSet.

## Continuing after the fix (possibly on another machine)

The harness lives on branch `verify/stress-testing-tuning-doc-claude` (fork
`cheyang/rbg`), production code (the PR's 4 doc files) untouched. All durable
state — manifest, `.last-reviewed`, results — is on that branch.

1. Get it onto the fixed code:
   ```bash
   git fetch https://github.com/cheyang/rbg.git verify/stress-testing-tuning-doc-claude
   git checkout <fixed-branch>
   git checkout FETCH_HEAD -- docs/verification/stress-testing-tuning-doc
   ```
2. Prereqs: python3, helm, go, jq; markdownlint-cli2 optional (else L1b skips).
3. Re-run: `bash docs/verification/stress-testing-tuning-doc/scripts/re-verify.sh`
   (auto-fetches the current PR head from the manifest's `pr` URL) — or
   `re-verify.sh <sha>` for a specific ref. It grafts the harness onto that ref,
   runs L1+L2, and prints per-finding **Fixed / Still-broken / Harness-update**.
4. Polarity: all contract checks — every finding's checks must PASS for "Fixed".
5. Harness-bites: re-verified this round (see above); re-check after any checker
   edit by flipping a doc claim and confirming the check goes red.

### Kickoff prompt for a fresh agent
```text
Continue a verification task on branch verify/stress-testing-tuning-doc-claude
(fork cheyang/rbg). Background: a review of sgl-project/rbg PR #390 (docs-only)
produced findings F1/F2/F3 (+nits F4-F8); a harness at
docs/verification/stress-testing-tuning-doc reproduced F1 and F3 automatically
and F2 by reading. The PR docs may now be fixed at a newer head. Read
docs/verification/stress-testing-tuning-doc/README.md ("Continuing after the
fix"), run scripts/re-verify.sh (no args), report the per-finding
Fixed/Still-broken table, advance .last-reviewed, commit and push to the fork
branch. Do not touch the PR itself.
```
