# KEP-543 Segment — Verification Harness (Reviewer: Claude, PR #569)

PR: https://github.com/sgl-project/rbg/pull/569 (docs-only: `keps/543-segment/{README.md,README.zh-CN.md,kep.yaml}`)
Reviewed head: `bd378ebd` · base/merge-base: `ea5e82ae` · Branch: `verify/kep543-segment-claude`
Production code on this branch is **untouched** — the diff is this harness only.

## Premise (P0) — Confirmed

Issue [#543](https://github.com/sgl-project/rbg/issues/543) is OPEN and asks for exactly this
design: a rule-level `segment` field in CoordinatedPolicy, `groupIndex = ordinal / ratio`
assignment, frozen identity labels, complete-Segment operation for gang/scaling/rolling-update,
and a KEP under `keps/<n>-segment/`. Symptom, mechanism, and component match the PR one-to-one
(evidence: `results/premise.txt`). The PR legitimately says "Related to #543" (not "fixes"), so
nothing auto-closes; this is a design proposal, consistent with the issue's KEP follow-up ask.

## Observed vs expected

| ID | Claim (falsifiable) | Layer / test | Polarity | Observed | Verdict |
| --- | --- | --- | --- | --- | --- |
| P0 | Issue #543 open + matches the KEP scope | `scripts/check_premise.sh` (gh) | contract | OPEN, title/body match | **Confirmed** |
| AC2 | markdownlint 0 issues on both READMEs | `check_docs.sh [1]` | contract | 0 issues | **Pass** |
| AC3 | all relative links + TOC anchors resolve | `check_docs.sh [2]` | contract | 7 targets exist, 0 bad anchors | **Pass** |
| AC4 | EN/ZH content parity | `check_docs.sh [3]` (check_parity.py) | contract | 104 headings, 28 code blocks, 28 tables, 13 key numbers all match | **Pass** |
| AC5 | every "existing behavior" claim resolves to real code | `check_code_refs.sh` | contract | 16/16 symbols found at head | **Pass** |
| F1 | design names a discovery mechanism/owner + integration/e2e coverage for the Segment discovery invariant | `check_f1_discovery.sh` | contract (red = gap) | (a) FAIL, (b) FAIL | **Reproduced** |
| F1 bites | the F1 check fails for the right reason | simulated fix in scratch copy | — | flips green under proposed fix (`results/f1-bites-check.txt`) | **Bites** |

## Findings carried by this harness

- **F1 [major]** — the KEP requires "discovery must publish only the aligned base-group
  target; no residual or surge identities" but assigns no mechanism/owner and has one
  unit-test bullet. Current discovery (`discovery.NewConfigBuilder`, called at
  `internal/controller/workloads/rolebasedgroup_controller.go:492`) enumerates
  `[0, spec.roles[].replicas)` from the RBG spec, so an unaligned desired state would
  publish never-to-be-created residual addresses — exactly the partial-deployment failure
  mode issue #543 set out to fix. Fix the doc: add the discovery subsection + tests.
- **F2 [minor/question]** — static `segBatchSize >= minSegments` webhook check over-rejects
  steady-state configs (RBG already at/above threshold). Reasoning-only; no harness.
- **F3 [minor/question]** — scale-in below `minSegments`: the doc says "reported through the
  existing incompatible-Gang path" but that path halts role reconciliation entirely; the
  user-visible outcome (scale-in blocked) should be stated. Reasoning + code trace.
- **F4 [minor]** — rule rename re-keys fleet-wide segment labels and transiently splits the
  Volcano `matchLabelKeys=[segment]` subGroup partition; gang dispatch can stall during the
  convergence window; no test bullet covers rename under an active gang. Reasoning-only.
- **F5 [nit]** — PR body stale: the `review-discussion.zh-CN.md` note describes be3a6f4, not
  the head bd378eb (link already removed). Evidence: `results/f5-pr-body-staleness.txt`.

## How to re-run

From a checkout of this branch:

```sh
bash docs/verification/kep543-segment/scripts/re-verify.sh        # current PR head
bash docs/verification/kep543-segment/scripts/re-verify.sh <ref>  # specific commit
```

Re-verify grafts the scripts onto the fetched PR head in a temp worktree (additive only),
runs P0 + doc-quality + code-refs + F1, prints per-finding verdicts, and prints the
`last-reviewed..head` delta range with the command to advance `.last-reviewed`.

Individual checks:

```sh
bash docs/verification/kep543-segment/scripts/check_docs.sh <repo-root>
bash docs/verification/kep543-segment/scripts/check_code_refs.sh <repo-root> <ref>
bash docs/verification/kep543-segment/scripts/check_f1_discovery.sh <repo-root>
bash docs/verification/kep543-segment/scripts/check_premise.sh
```

## Polarity table (what goes green / what flips after a fix)

| Check | Now | After the F1 doc fix |
| --- | --- | --- |
| check_premise.sh | pass (needs gh) | pass |
| check_docs.sh (lint/links/parity) | pass | pass — keep as regression suite for doc edits |
| check_code_refs.sh | pass | pass — fails if the code reuse claims drift from reality |
| check_f1_discovery.sh | **red (reproduction)** | **green** — requires a named discovery mechanism/entry point AND integration/e2e discovery coverage in the test plan |

## Continuing after the fix

Requirements: git, python3, `gh` (premise check only — SKIPPED otherwise),
`npx markdownlint-cli2` (lint — SKIPPED otherwise). No cluster, no Go build: this is a
docs-only PR, so unit + "integration" here means repo-tree-level doc validation against
the actual code the doc cites.

Kickoff prompt for a fresh agent resuming from this branch alone:

> You are resuming the KEP-543 / PR #569 verification (branch
> `verify/kep543-segment-claude`). Read
> `docs/verification/kep543-segment/verify-manifest.json` and this README, run
> `bash docs/verification/kep543-segment/scripts/re-verify.sh` (no args; it fetches the
> current PR head), apply the polarity table above when interpreting results, then commit
> the advanced `.last-reviewed` and push to the reviewer fork remote `fork`.

## CI note (attribution)

`envtest` failed on head (`--- FAIL: TestRestartPolicy (215.68s)`), but the PR adds zero Go
files, and the identical Go tree at merge-base `ea5e82ae` is green (run 43176898037). The
failure is an unrelated flake, not a PR finding. All doc-relevant checks (markdown-check,
lint, build, unit-test, copyright) are green. See `results/ci-envtest.txt`.
