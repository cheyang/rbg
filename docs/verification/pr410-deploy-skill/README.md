# Verification: sgl-project/rbg PR #410 — `rbg-inference-deploy` agent skill + CLAUDE.md

Reviewer-side verification harness. **No PR content or production code is modified by this
branch** — everything here is additive (`docs/verification/pr410-deploy-skill/`, two test
files). `git diff --stat pr410 -- . ':(exclude)docs/verification' ':(exclude)test/verification'`
is empty apart from the harness test files.

- PR: <https://github.com/sgl-project/rbg/pull/410> (`chore: add rbg-inference-deploy agent skill and CLAUDE.md`)
- Reviewed head: `cb4837063c1723ee62a54025973fabcd31dcbcd0` (see `.last-reviewed`)
- Base at review time: merge-base `aca4b7dd`; upstream `main` had already moved to `47cfe17e`
- Diff under review: 6 new files, +1736 / −0, **all markdown** — `CLAUDE.md` plus
  `.claude/skills/rbg-inference-deploy/{SKILL,prerequisites,deployment-analysis,yaml-rules,benchmark}.md`

## Why this PR needs verification at all

The diff contains no executable code, so CI (all green) proves nothing about it. But the
content *is* executable in the sense that matters: it is a wizard an agent follows verbatim to
generate and apply RBG YAML against a user's cluster. Every command, Makefile target, API
field and enum value it names is a factual claim about this repo, and one of them —
"tolerations are silently dropped by the RBG controller when going through `templateRef`" — is
the sole justification for a rule that changes what YAML users get on the most common kind of
GPU cluster. So the claims were tested the same way code would be.

## Observed vs. expected

| # | Claim under test | Layer | Verdict | Evidence |
|---|---|---|---|---|
| **F1** | `tolerations` cannot reach the Pod via `templateRef.patch` — "verified in source code" (`yaml-rules.md:6`, `:570`, `:575`; `SKILL.md:230`; PR description) | L1 unit + L3 live | **NOT REPRODUCED — the claim is false** | `results/L1-unit.txt`, `results/L3-f1-tolerations.txt` |
| **F2** | `make build-cli` / `make build-cli-all` exist (`CLAUDE.md:14-15`) | L1 | **CONFIRMED broken** — no such targets | `results/L1-doc-claims-gotest.txt` |
| **F3** | `kubectl rbg llm config init` / `model pull` / `svc run` / `svc chat` / `benchmark run` work (`CLAUDE.md:130-135`) | L1 | **CONFIRMED broken** — `llm` is not registered on the CLI root | `results/L1-doc-claims.txt` (built binary's own `--help`) |
| **F4** | `RecreateRBGOnPodRestart` is a valid `restartPolicy`, and `restartPolicy` is a role-level field (`CLAUDE.md:120-123`) | L1 + L3 live | **CONFIRMED broken** — enum rejects the value; field lives under the pattern | `results/L3-api-validation.txt` |
| **F5** | `SKILL.md` renders as written | L1 (+ `marked` cross-check) | **CONFIRMED broken** — odd fence count; Phase 4 / 4.1 / 4.2 never render | `results/L1-doc-claims.txt` |
| **F8** | *Counter-check* of the Copilot comment on `yaml-rules.md:7` claiming `templateRef.patch` is optional | L1 + L3 live | **PR #410 is right, the comment is wrong** — patch is required | `results/L1-unit.txt`, `results/L3-api-validation.txt` |

### F1 in detail — the load-bearing claim is inverted

`GetResolvedTemplate` (`api/workloads/v1alpha2/helper.go:315`) applies a plain
`strategicpatch.StrategicMergePatch` over the **whole** `PodTemplateSpec`
(`helper.go:343-369`), and `ConstructPodTemplateSpecApplyConfiguration`
(`pkg/reconciler/pod_reconciler.go:128-136`) converts it with the generic unstructured
converter. There is no field allow-list anywhere on the path, and every workload reconciler
(sts / deploy / lws / roleinstanceset) funnels through it. No code drops tolerations.

Live confirmation on a real cluster — one RBG, two roles sharing one `roleTemplate`:

```
--- OBSERVED: tolerations on each Pod (kubernetes-injected defaults filtered out) ---
f1-templateref-tol-decode-0	[]
f1-templateref-tol-prefill-0	[{"effect":"NoSchedule","key":"verify.rbg.io/pr410","operator":"Equal","value":"from-patch"}]

--- OBSERVED: nodeSelector on each Pod ---
f1-templateref-tol-decode-0	null
f1-templateref-tol-prefill-0	{"kubernetes.io/os":"linux"}

RESULT F1: REFUTED — the toleration from templateRef.patch IS present on the Pod (1 match).
```

`prefill` got its toleration **only** from `templateRef.patch`; `decode` (same base template,
`patch: {}`) correctly got none. Both Pods reached `Running`.

The one real caveat the docs could have made instead: `corev1.PodSpec.Tolerations` carries no
`patchMergeKey`, so a patched `tolerations` list **replaces** the base list rather than merging
into it (`TestF1_TolerationsListIsReplacedNotDropped`). That is worth documenting — "replace",
not "drop".

Why this matters beyond accuracy: cloud GPU node pools are usually tainted, so the skill's rule
means roleTemplates is effectively never used in exactly the environment the wizard targets,
and users get duplicated inline templates per role for no reason.

## Layers and how to run them

Everything runs from a checkout of this branch at the repo root.

### L1 — deterministic (no cluster, no envtest)

```bash
go test ./api/workloads/v1alpha2/ -run 'TestF1_|TestF8_' -v   # F1 behavior, F8 counter-check
go test ./test/verification/pr410/ -v                          # F1 doc side, F2, F3, F4, F5
bash docs/verification/pr410-deploy-skill/scripts/20-doc-claims.sh   # same claims, shell form;
                                                                     # also builds the CLI and
                                                                     # asks it for --help
```

`20-doc-claims.sh` optionally cross-checks F5 with a real markdown parser. To enable:
`npm --prefix /tmp install marked`.

### L3 — live cluster (needs `KUBECONFIG` + the RBG controller installed)

```bash
export KUBECONFIG=/path/to/config
bash docs/verification/pr410-deploy-skill/scripts/00-setup.sh              # creates ns rbg-verify-pr410 only
bash docs/verification/pr410-deploy-skill/scripts/10-scenario-f1-tolerations.sh   # F1
bash docs/verification/pr410-deploy-skill/scripts/30-live-api-validation.sh       # F4, F8
bash docs/verification/pr410-deploy-skill/scripts/99-teardown.sh          # always
```

Scoped and idempotent: the only thing created outside `--dry-run=server` is the namespace
`rbg-verify-pr410` (override with `VERIFY_NS`), deleted by the teardown. Nothing cluster-wide
is touched. Override the test image with `VERIFY_IMAGE` if the default is unreachable.

Verified against: ACK `cn-hongkong`, k8s `v1.36.1-aliyun.1`, controller
`rolebasedgroup/rbgs-controller:v0.8.0-cea2a47` in namespace `rbg-system`, 3 untainted nodes.
Recorded in `results/L3-00-setup.txt`.

## Test polarity

All findings are **contract** tests — no bug-canaries — but F1 is mixed on purpose:

| Test | Now | After the docs are fixed |
|---|---|---|
| `TestF1_TolerationsSurviveTemplateRefPatch` and the other 3 behavior tests | PASS (this is the refutation) | still PASS — they now guard against a real regression |
| `TestF1_DocsDoNotClaimTolerationsAreDropped` | FAIL | PASS |
| `TestF2_` / `TestF3_` / `TestF4_` / `TestF5_` | FAIL | PASS |
| `TestF4c_RestartPolicyLivesUnderThePatternNotTheRole` | PASS (records the structural fact) | PASS, or skips if the API grows a role-level field |
| `TestF8_TemplateRefWithoutPatchIsRejected` | PASS (confirms the PR) | still PASS |

"All green" therefore does mean fixed for this topic. See `results/harness-bites.txt` for the
Step-4 proof that each red result is red for the right reason: the F1 tests were re-run with a
temporary probe that actually nulls `merged.Spec.Tolerations` (all four flipped red, each on
its own assertion), and the doc tests were re-run with the proposed fixes applied (all green).
Both probes were reverted and the production diff re-checked as empty.

## Proposed fixes

1. **F1** — delete the "tolerations cannot pass through `templateRef.patch`" rule and the
   "if taints exist you must use inline `template`" constraint that rests on it
   (`yaml-rules.md:6`, `:173-176`, `:570`, `:575`; `SKILL.md:229-231`), plus the matching
   paragraph in the PR description. Replace with the true caveat: a patched `tolerations`
   list replaces rather than merges. Same for the blanket `affinity` warning at
   `yaml-rules.md:577` ("behavior not sufficiently verified") — it goes through the identical
   merge path.
2. **F2** — drop `make build-cli` / `make build-cli-all`, or add the targets. Note no Makefile
   target builds `cmd/cli` at all today.
3. **F3** — drop the five `kubectl rbg llm ...` lines, or register `llm.NewLLMCmd` in
   `cmd/cli/cmd/root.go`. The commands the CLI really exposes are `status`, `rollout`
   (`kubectl rbg llm benchmark` exists in the tree but is unreachable — a pre-existing bug in
   the repo, worth its own issue either way).
4. **F4** — remove `RecreateRBGOnPodRestart` (deprecated per `keps/inactive-pod-handling/README.md:453`,
   and rejected by the API server), and say that `restartPolicy` sits under
   `leaderWorkerPattern` / `customComponentsPattern`. Pre-existing related debt outside this
   PR: `doc/reference/api.md:125` still lists the value, `examples/deprecated/v1alpha1/basics/restart-policy.yaml`
   still uses it, and `pod_controller.go:283` still compares against a constant the enum can
   never produce.
5. **F5** — delete the stray fence at `SKILL.md:200`. One line; it restores parity for the
   whole file.
6. **F8** — no change; the PR is correct here. Worth replying to the review comment.

## Not verified

- **F6** — `SKILL.md:17` suggests emitting a bilingual switch notice while `SKILL.md:369`
  forbids mixing languages in one response. A self-consistency reading; nothing to execute.
- **F7** — `SKILL.md` declares "Default language: English" yet 4 of the 5 reference files are
  Chinese-only, so an English-speaking user's wizard is driven by Chinese instructions. A
  policy question for the maintainers, not a falsifiable claim.
- The `llmctl` command surface used in `prerequisites.md`, `deployment-analysis.md` and
  `benchmark.md` belongs to the external `rolebasedgroup/inference-ext-cli` project and was
  **not** cross-checked against it. The `helm` install snippet at `prerequisites.md:7-21` *was*
  checked and is correct (latest release `v0.8.0-alpha.3` ships `rbgs-0.8.0-alpha.3.tgz`, and
  the `sed` correctly strips the leading `v`).

## Continuing after the fix (another machine, another round)

All durable state is on this branch: harness, `verify-manifest.json`, `.last-reviewed`,
`results/`. Nothing machine-local travels (kubeconfig, `gh` auth, `npm` cache).

```bash
git fetch <reviewer-fork> verify/pr410-deploy-skill
git switch verify/pr410-deploy-skill
bash docs/verification/pr410-deploy-skill/scripts/re-verify.sh          # resolves the current
                                                                       # PR head from the
                                                                       # manifest's "pr" URL
```

It grafts the harness onto the new PR head, runs the L1 layer, and prints
**Fixed / Still-broken / Partial / Harness-update** per finding. The L1 verdicts are
deterministic on any machine; L3 needs a cluster (see `liveNote` in the manifest for the exact
observable signal to look for). After the round, advance the marker and push:

```bash
git rev-parse FETCH_HEAD > docs/verification/pr410-deploy-skill/.last-reviewed
git commit -am 'verify(pr410): round N — advance .last-reviewed' && git push
```

Kickoff prompt for a fresh agent:

> Continue the review pipeline for https://github.com/sgl-project/rbg/pull/410. The
> verification branch `verify/pr410-deploy-skill` on my fork holds the harness; read
> `docs/verification/pr410-deploy-skill/README.md`, run `scripts/re-verify.sh`, report
> Fixed/Still-broken per finding honoring the polarity table, then incrementally review the
> `.last-reviewed..head` delta and add any new findings to the harness.
