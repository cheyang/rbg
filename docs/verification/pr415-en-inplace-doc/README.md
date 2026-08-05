# pr415-en-inplace-doc — doc-accuracy verification

Reproducible evidence for the findings raised while reviewing
[sgl-project/rbg#415](https://github.com/sgl-project/rbg/pull/415)
*"doc: add en doc best-practice/configuring-inplace-update-and-scheduling-policies"*.

PR #415 is a **translation PR**: it adds the English `04-configuring-inplace-update-and-scheduling-policies{,-guide}.md`
and replaces the TODO placeholder in the already-merged Chinese `04-...md` with real links.
So the findings are of two kinds — *translation fidelity* (does `en` faithfully mirror `zh`?)
and *technical accuracy* (do either side's claims match the code?).

All layers run against the **code under review**: PR head `285761e9`
(`285761e948b81c2533d0fb91c84c3989f95ec3db`).

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| **1. Unit** | update-strategy branch selection (`applyTargetUpdate`), controller-side default back-fill, generated CRD schema | `go test ./pkg/reconciler/... -run 'PR415' -count=1 -v` |
| **1.5 Doc checks** | zh/en parity, link resolution, TODO/link self-consistency, comment/command agreement — over the whole `doc/best-practice/` tree | `bash scripts/check-*.sh` (exit 0 = pass, non-zero = defect found; prints `file:line`) |
| **2. Live cluster** | does the apiserver actually accept a misspelled `rollingUpdate.type`? | `bash scripts/l2-crd-accepts-bogus-strategy.sh` (needs `$KUBECONFIG`; `--dry-run=server` only, creates nothing) |
| 3. Live rollout | *not run* — see [Environment limits](#environment-limits) | — |

> **Test polarity.** *Contract* tests assert the intended correct behavior: they are RED on
> defective input and GREEN once fixed. *Bug-canary* tests assert the current (wrong or
> merely undocumented) behavior: they are GREEN now and FLIP TO RED when the underlying code
> changes, at which point they must be inverted. **"All green" is not the same as "fixed"** —
> read the [polarity table](#polarity-table) before interpreting a re-run.

## Summary of results

| ID | Sev | Claim | Layer | Verdict | Evidence |
|----|-----|-------|-------|---------|----------|
| **F1** | major | `en:76` claims `InPlaceOnly` *"does not fall back to Pod recreation and the update gets stuck"*. Actual: `InPlaceOnly` has **no distinct code path** — the guard is `!= RecreatePod`, so it behaves exactly like `InPlaceIfPossible` and does fall back to recreate. | 1 | **Confirmed** | 4 tests PASS; `InPlaceOnly` referenced 0 times under `internal/`+`pkg/` — `results/l1-unit-final.txt` |
| **F2** | minor | `en:371` keeps a TODO saying the linked docs *"have not been created yet"*, while both links below it already resolve. `zh` deleted exactly this line in this PR; the `en` side missed it. | 1.5 | **Confirmed** | `check-todo-consistency.sh` exit **1**, 1 self-contradicting placeholder at `en:371` → `results/l15-final.txt` |
| **F3** | minor | Both sides (`zh:121`/`en:121`) state `rollingUpdate.type` defaults to `InPlaceIfPossible`. Actual: the CRD has **neither `enum` nor `default`** — the default is back-filled by the controller, so the apiserver silently accepts a misspelled strategy and treats it as in-place update. | 1 + 2 | **Confirmed** | CRD schema: `enum present: NO`, `default present: NO`; apiserver ACCEPTED `TotallyBogusValue` and the realistic typo `InPlaceIfPosible` — `results/l2-crd-accepts-bogus-strategy.txt` |
| **F4** | nit | `zh guide:344` comment says *"确认所有 Pod 使用新镜像"* but the command inspects `env[?(@.name=="new_env")]`, and step 3 never changes the image. **The `en` translation of this line is the correct one** — `zh` is copy-paste residue from step 1. | 1.5 | **Confirmed** (defect is in `zh`, not in the new `en`) | `check-comment-command-agreement.sh` exit **1**: 226 comment+command pairs scanned, 1 mismatch, at `zh guide:344` — `results/l15-final.txt` |
| **F5** | — | *Positive baseline:* the new `en/04` pair is a faithful translation of `zh/04` — code blocks byte-identical, structure aligned, no dead links. | 1.5 | **Confirmed correct** | see [Translation-fidelity baseline](#translation-fidelity-baseline-f5) |

Nothing was **Not-reproduced**. One finding from the review round was *downgraded* rather than
confirmed: see [Downgraded / needs author input](#downgraded--needs-author-input).

## Per-finding detail

### F1 — `en:76`'s "update gets stuck" claim is false (major)

`en:76` reads:

> **Note**: The `InPlaceOnly` strategy is not recommended. When changes exceed image scope,
> this strategy **does not fall back to Pod recreation and the update gets stuck**.
> Use `InPlaceIfPossible` instead.

`zh:76` says only that `InPlaceOnly` is deprecated and not recommended. The causal claim is
new in the translation, and it is wrong. The mechanism:

- `pkg/reconciler/roleinstanceset/statefulmode/stateful_instance_set_control.go:749` and
  `pkg/reconciler/roleinstanceset/statelessmode/sync/update.go:182` both branch on
  `set.Spec.UpdateStrategy.Type != RecreatePodUpdateStrategyType` — i.e. `RecreatePod` is
  the **only** excluded strategy.
- The in-place attempt is followed by `if !inplacing { deleteInstance(...) }`, so a change
  outside image scope falls back to recreation regardless of which non-`RecreatePod` value
  was set.
- `InPlaceOnlyUpdateStrategyType` is referenced **nowhere** in `internal/` or `pkg/` — only
  at its own constant definition in `api/workloads/v1alpha2/rolebasedgroup_types.go:113-114`.

Tests (all `POLARITY: contract` except the last):

| Test | Asserts |
|------|---------|
| `TestVerifyPR415_F1_InPlaceOnlyFallsBackToRecreate` | with `InPlaceOnly` and a non-image change, the instance **is** deleted (recreated) |
| `TestVerifyPR415_F1_InPlaceOnlyAndIfPossibleAreIndistinguishable` | both strategies produce the same observable outcome |
| `TestVerifyPR415_F1_RecreatePodIsTheOnlyExcludedStrategy` | table over all strategy values: only `RecreatePod` skips the in-place attempt |
| `TestVerifyPR415_F1_InPlaceOnlyHasNoImplementationReferences` | **`POLARITY: canary`** — 0 references under `internal/`+`pkg/`; flips red the day someone gives `InPlaceOnly` a real code path, at which point this doc note must be rewritten |

**Suggested replacement for `en:76`** — stay faithful to `zh` and drop the invented causality:

> **Note**: The `InPlaceOnly` strategy is deprecated and not recommended. Use
> `InPlaceIfPossible` instead.

### F2 — stale TODO left in `en` (minor)

The whole point of this PR's `zh` change was to remove the TODO placeholder:

```diff
-<!-- TODO: 以下文档尚未创建，待文档完成后统一添加链接 -->
-+ 使用 RBG 部署推理服务
-+ 配置滚动更新策略
++ [使用 RBG 部署推理服务](./01-deploy-inference-service.md)
++ [配置滚动更新策略](./03-configuring-rolling-updates.md)
```

`en:371` still carries the English equivalent of that comment, directly above two links that
already resolve on disk. `scripts/check-todo-consistency.sh` generalizes this: it flags any
TODO that claims documents are missing while links beneath it resolve.

```
FAIL self-contradicting TODO placeholder:
     doc/best-practice/en/04-configuring-inplace-update-and-scheduling-policies.md:371
     but these link(s) below it already resolve on disk:
       ...:373  ->  ./01-deploy-inference-service.md
       ...:374  ->  ./03-configuring-rolling-updates.md
```

**Suggested change**: delete `en:371`. If the intent was to flag the still-unlinked
`RBG Warmup` entry, move the comment directly above that entry and reword it to name only it.

### F3 — the documented default is controller-side only, and typos are accepted (minor)

Both language versions present `InPlaceIfPossible` as the default of `rollingUpdate.type`.
It is a real default — but applied by
`pkg/reconciler/roleinstanceset_reconciler.go:232-234` (`if rollingUpdate.Type == "" { ... }`),
not by the schema. The live CRD carries the promise only in prose:

```
{"description": "Type indicates the type of the InstanceSetUpdateStrategy.\nDefault is InPlaceIfPossible.", "type": "string"}
  enum present   : NO
  default present: NO
```

Consequences, both measured against the real apiserver with `--dry-run=server`:

```
ACCEPTED  type: TotallyBogusValue  (NOT a strategy at all)
ACCEPTED  type: InPlaceIfPosible   (realistic typo, one "s")
```

and with the field omitted, the object the apiserver returns has `{"type": "<ABSENT>"}` — the
default never materializes server-side. A typo therefore lands in the `!= RecreatePod` branch
and is silently treated as in-place update.

Tests — both **`POLARITY: canary`**:
`TestVerifyPR415_F3_ControllerBackfillsEmptyRollingUpdateType` (L1, the back-fill exists) and
`TestVerifyPR415_F3_CRDHasNoEnumOrDefaultOnRollingUpdateType` (L1, reads
`config/crd/bases/workloads.x-k8s.io_rolebasedgroups.yaml`), plus the L2 script against the
live cluster.

**Suggested change**: add a footnote under the parameter table, e.g.

> Note: this default is applied by the controller when `type` is empty; the CRD does not
> enumerate valid values, so a misspelled strategy name is accepted silently and handled as
> non-`RecreatePod` (i.e. in-place update).

Arguably the better fix is on the API side (`+kubebuilder:validation:Enum`) — that is the
maintainers' call, which is why these are canaries rather than contract tests.

### F4 — `zh guide:344` comment contradicts its command; `en` already fixed it (nit)

| Side | Line 344 |
|------|----------|
| `zh` | `# 确认所有 Pod 使用新镜像` |
| `en` | `# Confirm all Pods contain the new environment variable` |

The command under both is identical and inspects
`{.spec.containers[0].env[?(@.name=="new_env")].value}`; step 3 of the guide never touches the
image. So this is the one place where **the translation corrected the original** — no change
needed in `en`.

`scripts/check-comment-command-agreement.sh` scanned 226 comment+command pairs across the
whole `doc/best-practice/` tree and found exactly this one mismatch.

**Suggested change (in `zh`, not in this PR's `en`)**: `# 确认所有 Pod 已包含新增环境变量`,
matching the wording already used at `zh:236`. Verified to turn the check green — see
BITE 4b in `results/harness-bites.txt`.

### Translation-fidelity baseline (F5)

Rather than eyeballing 750 lines, the three parity checks are scripted and generalized to the
whole `doc/best-practice/` tree, so they keep working as `05` (#402) and `07` (#400) land.
They skip files that exist on only one side and report them instead of failing.

For **this PR's own files** (`--only 04-configuring-inplace`), all three pass:

```
ok   04-configuring-inplace-update-and-scheduling-policies-guide.md: 23 blocks parity-clean (23 code byte-identical, 0 diagram structure-identical)
ok   04-configuring-inplace-update-and-scheduling-policies.md:       9 blocks parity-clean  (6 code byte-identical, 3 diagram structure-identical)
RESULT: PASS   (29 code blocks + 3 diagram blocks, comment text normalized away)

ok   04-...-guide.md: headings 26/26 level-aligned, table rows 4/4,  bullets 35/35
ok   04-...md:        headings 26/26 level-aligned, table rows 40/40, bullets 20/20
RESULT: PASS

--- link check summary ---   markdown files 20, relative links 14, dead 0   RESULT: PASS
```

So no YAML field, replica count, image tag, `gracePeriodSeconds`, or `maxUnavailable` value
drifted in translation, and no section was dropped or invented.

> **Correction to the review round.** The review reported the `04` structure as "31/31 and
> 40/40 headings". The scripted count is **26/26 headings** for both files, with 40/40 *table
> rows* for the main doc and 4/4 for the guide. The conclusion (perfect alignment) is
> unchanged; the earlier numbers conflated headings with table rows. The numbers above come
> from `results/l15-final.txt` and are the ones to trust.

#### Side effect: two pre-existing drifts outside this PR's scope

Run over the whole tree, `check-codeblock-parity.sh` exits **1** — but **not** because of
`04`. It surfaces drift that was already on `main` before this PR:

| File | Block | Drift |
|------|-------|-------|
| `03-configuring-rolling-updates-guide.md:415` | #023 | `en` has one extra sample line, `> pd-rollout-demo-prefill-0   0/1     Error   0   50m`, that `zh` lacks |
| `10-deploy-mooncake-store-with-rbg-guide.md:329` | #009 | `zh`/`en` sample log lines differ (`CreateMoveTask:(Req=…)` vs `CreateMoveTask=(Req=…)`, `PutRevoke:` vs `PutRevoke=`) |
| `10-deploy-mooncake-store-with-rbg.md:42,59` | #001,#002 | diagram/prose blocks differ in line count (zh=10/en=11, zh=20/en=23) |

These are **not** PR #415's responsibility and should not block it. They are recorded here
because the harness found them and they are cheap follow-ups. Use `--only 04-configuring-inplace`
to scope the check to this PR.

## Polarity table

| Test / script | Polarity | Now | After a fix |
|---------------|----------|-----|-------------|
| `..._F1_InPlaceOnlyFallsBackToRecreate` | contract | PASS | stays PASS (RED would mean `InPlaceOnly` gained a real code path) |
| `..._F1_InPlaceOnlyAndIfPossibleAreIndistinguishable` | contract | PASS | stays PASS |
| `..._F1_RecreatePodIsTheOnlyExcludedStrategy` | contract | PASS | stays PASS |
| `..._F1_InPlaceOnlyHasNoImplementationReferences` | **canary** | PASS | **FLIPS RED** if `InPlaceOnly` gets implemented → then invert it and rewrite the doc note |
| `check-todo-consistency.sh` | contract | **FAIL (= F2 reproduced)** | **turns GREEN** when `en:371` is dropped |
| `..._F3_ControllerBackfillsEmptyRollingUpdateType` | **canary** | PASS | **FLIPS RED** if the default moves into the schema → invert |
| `..._F3_CRDHasNoEnumOrDefaultOnRollingUpdateType` | **canary** | PASS | **FLIPS RED** once an `enum`/`default` is added → invert to assert the enum members |
| `l2-crd-accepts-bogus-strategy.sh` | **canary** | PASS (bogus accepted) | **FLIPS RED** once the apiserver rejects typos |
| `check-comment-command-agreement.sh` | contract | **FAIL (= F4 reproduced)** | **turns GREEN** when `zh guide:344` is reworded |
| `check-codeblock-parity.sh --only 04-configuring-inplace` | contract | PASS | stays PASS (RED = translation drift regressed) |
| `check-heading-parity.sh` | contract | PASS | stays PASS |
| `check-links.sh` | contract | PASS | stays PASS |

Two checks are **red on purpose right now** — that redness *is* the reproduction of F2 and F4.
A clean re-run of those two is the signal the PR was fixed.

## Harness-bites

Each check was proven to actually bite by mutating one thing, re-running, and restoring —
full transcript in `results/harness-bites.txt`.

| Bite | Mutation | Result |
|------|----------|--------|
| 1 (F1 contract) | add `InPlaceOnly` to the exclusion guard, i.e. implement what `en:76` claims | all 4 F1 tests → **RED**; restored → GREEN |
| 2 (F1 canary) | add one `InPlaceOnly` reference under `pkg/` | canary → **RED** (`CANARY FLIPPED … 1 site(s)`); restored → GREEN |
| 3 (F2 contract) | delete the stale TODO at `en:371` | check → **GREEN (0)**; restored → RED (1) — so it is not permanently red |
| **4b** (F4 contract) | reword `zh guide:344` to describe the env var | check → **GREEN (0)**, `mismatches: 0`; restored → RED (1) |
| 5a (F3 canary) | inject `enum: [InPlaceIfPossible, InPlaceOnly, RecreatePod]` into the generated CRD | canary → **RED** (`CANARY FLIPPED: rollingUpdate.type now has an enum`); restored → GREEN |
| 5b (F3 canary, live arm) | — | **NOT PERFORMED**, deliberately: it would require adding an enum to the shared cluster's CRD. 5a exercises the same detection logic; what stays unproven is only the script's `kubectl` plumbing. |
| 6 (F5 contract) | change one `replicas:` value inside an `en/04` code block (`2` → `9`) | parity check → **RED**; restored → GREEN |

> An earlier attempt at bite 4 is preserved in the transcript and **marked invalid**: its
> mutation never applied (`AssertionError: anchor count=2`), so its "fixed → GREEN" line had
> measured the unmutated file. It was redone as 4b, addressing the line by number.

The tracked tree was re-checked with an actual `git diff --stat` after every bite —
`TRACKED-TREE-CLEAN: yes` throughout, never a printed reassurance in place of a diff.

## Environment limits

Recorded honestly rather than worked around:

1. **No live rollout was performed (Layer 3 skipped).** The sandbox cluster's deployed
   controller and CRDs disagree about `RoleInstance.spec.restartPolicy` (controller writes a
   string, CRD requires an object), so RoleInstances are rejected and no Pod is ever created;
   additionally, locally-built `rbgs` binaries have been observed to silently reconcile
   nothing on this cluster. Consequently the guide's **timing and placement assertions are
   unverified**: sequential update from highest ordinal down (1 → 0), the ~30 s spacing
   between instances, `AGE` not resetting while `RESTARTS` increments, and
   `Preferred`/`Required` affinity returning a Pod to its original node. These need a healthy
   cluster; they are plausible from the code but **not proven here**.
2. **The live CRD was not modified.** Layer 2 uses `--dry-run=server` exclusively and creates
   nothing; the enum-injection bite ran against the repo's generated CRD, not the cluster's.

Cluster used for Layer 2: `v1.36.1-aliyun.1`, 3 nodes.

## Downgraded / needs author input

- **Is `InPlaceOnly` officially deprecated?** `zh:76` says it is, but v1alpha2 carries no
  `// Deprecated:` marker and no enum exclusion. The likely origin of the claim is that
  v1alpha1 dropped the value entirely (`api/workloads/v1alpha1/constant.go:229-241` lists only
  `Recreate` and `InPlaceIfPossible`). The author/maintainer should decide: add a `Deprecated:`
  marker to the v1alpha2 API, or reword the docs to "retained but not separately implemented;
  behaves identically to `InPlaceIfPossible`". **This choice determines the final wording of
  F1** — hence it is flagged, not silently resolved.
- **`role-inplace-scheduling-avoid` is undocumented on both sides**
  (`api/workloads/constants/annotation.go:221-227`; injects a `DoesNotExist` hard exclusion).
  This is a coverage gap in the original `zh` document, not a translation defect, so it need
  not block this PR.
- **`gracePeriodSeconds` default `0`** (`en:122`) holds as written — the CRD has no default and
  the Go field has no `+kubebuilder:default`, so the zero value is `0`. Not chased into
  `SetOptionsDefaults` (`pkg/inplace/instance/inplaceupdate/inplace_update_defaults.go:33`) to
  confirm nothing special-cases `0`.

## Proposed fixes (deliberately NOT applied here)

- **F1** — replace `en:76` with a faithful translation of `zh:76` (text above); optionally
  correct both sides once the maintainers settle the deprecation question.
- **F2** — delete `en:371`, or move it above the genuinely unlinked `RBG Warmup` entry.
- **F3** — add the controller-side-default footnote to both parameter tables; consider
  `+kubebuilder:validation:Enum` on the API as the real fix.
- **F4** — reword `zh guide:344` (this is a follow-up to merged #399, not a change to this PR).
- **Out of scope** — the two pre-existing parity drifts in `03-*-guide.md` and `10-*` .

## Continuing after the fix (possibly on another machine)

The harness lives on `verify/pr415-en-inplace-doc` in the reviewer's fork
(`https://github.com/cheyang/rbg`) with **production code and the reviewed documents
untouched**, so it grafts cleanly onto whatever the fixed code is.

```bash
# one line, resolves the current PR head from verify-manifest.json's "pr"
bash docs/verification/pr415-en-inplace-doc/scripts/re-verify.sh
```

Manually, or on a fresh clone:

```bash
git clone https://github.com/cheyang/rbg && cd rbg
git fetch origin verify/pr415-en-inplace-doc
git fetch https://github.com/sgl-project/rbg pull/415/head && git checkout FETCH_HEAD
git checkout origin/verify/pr415-en-inplace-doc -- \
  docs/verification/pr415-en-inplace-doc \
  pkg/reconciler/zz_verify_pr415_updatestrategy_test.go \
  pkg/reconciler/roleinstanceset/statefulmode/zz_verify_pr415_inplaceonly_test.go

go test ./pkg/reconciler/... -run 'PR415' -count=1 -v          # L1
for s in check-codeblock-parity check-heading-parity check-links \
         check-todo-consistency check-comment-command-agreement; do
  bash docs/verification/pr415-en-inplace-doc/scripts/$s.sh; echo "$s exit=$?"
done                                                            # L1.5
KUBECONFIG=~/.kube/config \
  bash docs/verification/pr415-en-inplace-doc/scripts/l2-crd-accepts-bogus-strategy.sh   # L2
```

Prerequisites: L1 needs only the Go toolchain; L1.5 needs `bash` + `python3`; L2 needs a
reachable cluster with the RBG CRDs installed (`$KUBECONFIG`); L3 needs a cluster whose
controller actually reconciles — see [Environment limits](#environment-limits).

Read results through the [polarity table](#polarity-table): `check-todo-consistency.sh` and
`check-comment-command-agreement.sh` going **green** means F2/F4 are fixed; a canary going
**red** means the implementation moved and the assertion must be inverted, not "a new bug".

`.last-reviewed` holds `285761e9`, the head reviewed this round; `re-verify.sh` prints the
`last-reviewed..head` delta to review incrementally, plus the one-liner to advance the marker.
Commit the advanced marker so the next round resumes without anyone typing a sha.

### Kickoff prompt for a fresh agent

```text
Continue a verification task on branch verify/pr415-en-inplace-doc of
https://github.com/cheyang/rbg (remote: origin; upstream is sgl-project/rbg — never push there).
Background: reviewing https://github.com/sgl-project/rbg/pull/415 (a zh->en translation PR for
the in-place update / scheduling best-practice doc) produced findings F1..F5; this harness
reproduced them. Read docs/verification/pr415-en-inplace-doc/README.md, section "Continuing
after the fix", and follow it: graft the harness onto the current PR head (or just run
scripts/re-verify.sh), run L1 + L1.5 + L2, and report an observed-vs-expected table.

Mind the polarity table: check-todo-consistency.sh (F2) and check-comment-command-agreement.sh
(F4) are RED on purpose right now — they turn green only when the PR is fixed. The F1 and F3
canaries are GREEN now and must be INVERTED if they flip red (that means the implementation
changed, not that a bug appeared). Re-run the harness-bites check before trusting a verdict.

L3 (real rollout timing/placement) is still unverified: this cluster's controller and CRDs
disagree on RoleInstance.spec.restartPolicy so no Pods are created. If you have a healthy
cluster, that is the highest-value gap to close.

Use --dry-run=server for cluster checks; scope anything you create to your own namespace and
delete it; no cluster-wide destructive actions. Do not modify production code or the reviewed
documents. Do not open a PR or post any GitHub comment.
```

## Layout

```
docs/verification/pr415-en-inplace-doc/
  README.md                                   this file
  verify-manifest.json                        finding -> tests -> polarity -> layer; "pr" links back to #415
  .last-reviewed                              285761e9
  scripts/
    re-verify.sh                              one-line re-run for the next round
    check-codeblock-parity.sh                 F5 — zh/en code blocks byte-identical (comments normalized)
    check-heading-parity.sh                   F5 — heading levels / table rows / bullets aligned
    check-links.sh                            F5 — every relative link resolves
    check-todo-consistency.sh                 F2 — no TODO claiming missing docs whose links resolve
    check-comment-command-agreement.sh        F4 — code-block comments match what the command does
    l2-crd-accepts-bogus-strategy.sh          F3 — live apiserver accepts a misspelled strategy
  results/
    l1-unit-final.txt                         L1 on the clean tree (6 tests, exit 0)
    l15-final.txt                             all five doc checks with exit codes
    l2-crd-accepts-bogus-strategy.txt         live-cluster evidence for F3
    harness-bites.txt                         every bite, with restore verified by real diffs
    l15-*.txt                                 per-check first-run captures
pkg/reconciler/zz_verify_pr415_updatestrategy_test.go                                  (additive)
pkg/reconciler/roleinstanceset/statefulmode/zz_verify_pr415_inplaceonly_test.go         (additive)
```

---

# Round 2 addendum — the L3 gap is now closed

**Date:** 2026-08-05. **Reviewed head:** unchanged (`285761e9`) — this round adds
evidence, not a new review of new code.

When this harness was first written, Layer 3 was skipped: the sandbox cluster's
controller (`v0.8.0-cea2a47`, commit `cea2a472`, 23 commits behind main and older
than #394) wrote `restartPolicy` as a bare string while the already-upgraded CRDs
required an object, so **no Pod was ever created**. That has been fixed by
upgrading to upstream `main`'s chart:

```bash
helm upgrade rbgs deploy/helm/rbgs -n rbg-system \
  --set controller.features.portAllocator.enabled=true
# chart 0.8.0-alpha.3, image rolebasedgroup/rbgs-controller:v0.8.0-47cfe17
```

It was only ever a controller-image lag — the CRDs were already correct, and the
cluster happened to hold zero `RoleInstanceSet`s so no data migration was needed.
RBGs now reconcile end to end, and every previously-unverified assertion in this
document has been measured on real Pods.

## What the live layer confirmed

| Doc claim | Location | Live result | Verdict |
|-----------|----------|-------------|---------|
| Image-only change updates in place; Pod AGE not reset, RESTARTS increases | zh/en:370 summary, guide step 1 | `startTime` identical at T0 and T+75s (`06:31:12`), Pod **uid unchanged**, `restartCount` 0→1, image swapped, node unchanged | **Correct** |
| Controller sets `InPlaceUpdateReady` | zh/en:170 step 1 | `readinessGates: [InPlaceUpdateReady, InstancePodReady]` (controller-injected — the manifest declared none), condition `InPlaceUpdateReady=True` | **Correct** |
| Instances update one at a time from high to low ordinal | zh/en guide:87 | 4 replicas, `maxUnavailable: 1`: restarted in order **3 → 2 → 1 → 0**, strictly sequential | **Correct** |
| `Preferred` injects `preferredDuringScheduling` with **weight=100** toward the historical node | en guide:206 / zh guide:206 | recreated Pod carries `PREFERRED: weight=100 kubernetes.io/hostname In ['cn-hongkong.10.39.55.151']` | **Correct** |
| `Required` injects `requiredDuringScheduling` toward the historical node | en guide:313 / zh guide:313 | recreated Pod carries `REQUIRED: kubernetes.io/hostname In ['cn-hongkong.10.39.55.150']` | **Correct** |
| ~30 s between two consecutive instance updates | zh/en guide:87 | measured **60 s, 62 s, 60 s** | **Wrong — new finding F6** |

Artifacts: `results/l3-inplace-update-basic.txt`,
`results/l3-inplace-update-order-and-spacing.txt`,
`results/l3-inplace-scheduling-affinity.txt`. Scripts:
`scripts/l3-inplace-update-order-and-spacing.sh`,
`scripts/l3-inplace-scheduling-affinity.sh`.

## F6 — the "~30 seconds between instances" figure is the wrong quantity (new, minor)

- **POLARITY: contract.** Green once the doc states the right interval.
- **Location:** `doc/best-practice/zh/04-...-guide.md:87` and
  `doc/best-practice/en/04-...-guide.md:87`.
- **Doc says:** zh — 「按序号从高到低逐个更新实例（1 → 0），**两个实例更新间隔约 30 秒**」;
  en — "Instances are updated one by one from high to low ordinal (1 → 0), with
  **approximately 30 seconds between the two instance updates**".
- **Actually:** with the guide's own settings (`maxUnavailable: 1`,
  `inPlaceUpdateStrategy.gracePeriodSeconds: 30`) the measured gap between
  consecutive restarts is **~60 s**, consistently about twice the figure given:

  ```
  t=+61s   o-backend-3  restartCount -> 1
  t=+121s  o-backend-2  restartCount -> 1
  t=+183s  o-backend-1  restartCount -> 1
  t=+243s  o-backend-0  restartCount -> 1

  o-backend-3 -> o-backend-2 : 60s
  o-backend-2 -> o-backend-1 : 62s
  o-backend-1 -> o-backend-0 : 60s
  ```

  The mechanism explains it: each instance costs `gracePeriodSeconds` (30 s of
  draining while `InPlaceUpdateReady=False`) **plus** the container restart and
  the wait to become Ready again; only then does `maxUnavailable: 1` release the
  next instance. So 30 s is the *per-Pod drain*, not the *inter-instance gap*.
- **Note this is a different statement from lines 136 and 370**, which say each
  Pod waits ~30 s *between becoming NotReady and the image update*. That one
  describes the drain itself and is consistent with `gracePeriodSeconds: 30`; it
  is **not** part of this finding. Only line 87 conflates the two.
- **Suggested change** (zh:87): 「按序号从高到低逐个更新实例（1 → 0）。每个实例先等待
  `gracePeriodSeconds`（示例中 30 秒）排空连接，再原地更新并等待重新 Ready，因此**相邻两个
  实例的更新间隔约为 60 秒**，即 grace 时长加上容器重启与就绪时间。」 and the
  matching en text.
- **Measured with 4 replicas rather than the guide's 2**, deliberately: it yields
  three consecutive gaps instead of one, so the ~60 s figure rests on three
  samples rather than a single measurement.

## Two honest caveats about this round

1. **My first in-place-scheduling run was invalid and is not included as
   evidence.** I put `role-inplace-scheduling` on the RBG's
   `metadata.annotations`, but the doc puts it at `spec.roles[].annotations`
   (en guide:164, :272) alongside `rollingUpdate.type: RecreatePod`. No affinity
   was injected, and all Pods still showed "same node" — which would have read
   as a confirmation. It was not one: with replicas spread over a 3-node cluster,
   the scheduler distributes one per node anyway, so **node placement alone
   cannot distinguish affinity from coincidence**. The corrected run
   (`scripts/l3-inplace-scheduling-affinity.sh`) therefore asserts on the
   *injected affinity in the Pod spec*, which is independent of where the Pod
   lands, and says so in its own output.
2. **The affinity-polling arm of that script has a false-positive.** It uses
   `jsonpath='{.spec.affinity.nodeAffinity}'` as a presence test, which reports
   non-empty even when the structure carries nothing, so its `t=+3s ... HAS
   nodeAffinity` lines are followed by `<NONE>`. The authoritative reading is the
   "affinity on the settled pods" block at the end. Worth fixing before this
   script is reused.

Also visible in the corrected run: only the `-backend-1` Pods had been recreated
(`env=v2`) when it settled, while `-backend-0` was still `env=v1` — a third
independent corroboration of high-ordinal-first sequencing.

## Still not verified

- The guide's sample `kubectl get pods` **output blocks** (node names `node-A`..`node-D`,
  the `2/2` READY column) were not reproduced; the sandbox has 3 nodes, not 4.
  The behavior they illustrate is confirmed, the literal transcript is not.
- `gracePeriodSeconds: 0` behavior, and whether `SetOptionsDefaults`
  (`pkg/inplace/instance/inplaceupdate/inplace_update_defaults.go:33`)
  special-cases `0`, is still untested.
- Whether `InPlaceOnly` is *officially* deprecated remains an open question for
  the maintainers — see [Downgraded / needs author input](#downgraded--needs-author-input).
  F1's live behavior is unchanged by this round.
