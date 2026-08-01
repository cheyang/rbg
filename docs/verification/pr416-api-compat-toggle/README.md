# Verification — PR #416 (`feat: restrict v1alpha1 API compatibility RBAC via Helm toggle`)

Reviewer-private verification harness for
<https://github.com/sgl-project/rbg/pull/416>.

| | |
|---|---|
| Reviewed head | `a69ceada83126c3a7dbd1ab9e1578b3e10dc6ed4` |
| Base | `8a54787d698f76f3f754e844342d5dbd71244885` |
| Author | @diw-zw, head branch `0730-api-compat` |
| Upstream CI | **all 10 checks passing** — no upstream signal this round |
| Round | 1 (third round of the PR #413 → #414 → #416 lineage) |

**Production code on this branch is untouched.** The only additions are
`internal/controller/workloads/pr416_compat_verify_test.go` and this
`docs/verification/` tree.

---

## Observed vs. expected

| ID | Severity | Claim | Verdict | Evidence |
|----|----------|-------|---------|----------|
| **F1** | **blocker** | Shipped Helm default is the opposite of the documented default, so no RBAC is removed by default | **Reproduced** | [`f1-helm-default-rbac.txt`](results/f1-helm-default-rbac.txt) |
| **F2a** | **blocker** | A pre-existing legacy RBG can never be reconciled again — the controller's own write is denied by this PR's webhook, forever, silently | **Reproduced** | [`l1-unit.txt`](results/l1-unit.txt) |
| F2b | — | Harness-bites check for F2a (denial is the real validator's; enabled-control accepts) | **Green** | [`l1-unit.txt`](results/l1-unit.txt) |
| **F2c** | major | Grandfathering removed outright: an existing legacy RBG cannot even be scaled | **Reproduced** | [`l1-unit.txt`](results/l1-unit.txt) |
| **F3** | major | Reconcile path has no compat gate; legacy reconcilers still built, LWS watch re-arms, both hit removed RBAC | **Reproduced** | [`l1-unit.txt`](results/l1-unit.txt) |
| **F4** | major | Upgrade guard hard-fails the command the repo documents in 10 places, incl. its own README | **Reproduced** | [`f4-upgrade-guard.txt`](results/f4-upgrade-guard.txt) |
| F5 | non-blocking | `make manifests` no longer syncs the chart ClusterRole; only an `@echo` guards it | **Note** (no drift at this head: 209 == 209) | [`03-rbac-drift.txt`](results/03-rbac-drift.txt) |
| F5b | — | Generated artefacts are in sync | **Green** | [`f5b-manifests-freshness.txt`](results/f5b-manifests-freshness.txt) |
| **F6** | major | New compat block is unguarded, so a partial `compatibility:` block hard-fails the render — or *silently strips* the RBAC | **Reproduced** | [`f6-render-all-shapes.txt`](results/f6-render-all-shapes.txt), [`f6b-value-shape-semantics.txt`](results/f6b-value-shape-semantics.txt) |
| F7 | — | Regression guard: PR #414's render blocker does **not** recur | **Green** | [`l3-live-dryrun.txt`](results/l3-live-dryrun.txt) |
| N1 | non-blocking | No CI job renders the chart or installs it twice → F1/F4/F6 all invisible to CI | Open (carried from #414) | — |
| N2 | non-blocking | `deleteOrphanRoles` still skips legacy cleanup when disabled (#414's F6) | Carried, unchanged | — |

Net: **2 blockers, 4 major, 2 notes.** Two things the PR genuinely fixes: PR #414's
uninstallable-chart blocker is gone, and RoleBasedGroupSet finally gets a real validating
webhook (#414's F7) wired into **both** the Helm and kustomize manifests.

---

## The two blockers in one paragraph each

**F1 — the PR does not do what it says.** `values.yaml` ships
`compatibility.v1alpha1.enabled: true`. The chart README's value table, the chart README
prose ("By default … the chart ships in restricted mode for security") and the PR description
("default: `false`") all say `false`. A default `helm install` therefore still grants all 8
legacy RBAC entries and never passes `--disable-v1alpha1-compatibility`. Since the PR's stated
sole purpose is "to remove the excessive RBAC permissions … before the upcoming release", the
shipped default defeats it. This is the third consecutive round where the effective default has
disagreed with the documentation.

**F2a — disabling compatibility strands every RBG that already exists.** The new webhook rule
covers `UPDATE` on `rolebasedgroups`. `ensureDiscoveryConfigMode` patches an annotation onto
the *main* RBG resource on the first reconcile of any RBG lacking it — i.e. every object
created before this build. With compatibility off that patch is denied by
`validateNoLegacyWorkloads`, `Reconcile` returns the error, and controller-runtime retries
forever. PR #414 at least set a terminal `LegacyWorkloadsDisabled` condition; PR #416 deleted
`handleLegacyWorkloads` entirely, so there is no condition and no event. This is not an exotic
state: the PR's own prescribed update path is uninstall-and-reinstall, and the CRDs are *not*
Helm-managed (no `crds/` dir — they come from the `crd-upgrade` Job), so every pre-existing RBG
survives and lands here.

---

## Layers

| Layer | What it proves | How to run |
|-------|----------------|------------|
| **script** (offline) | F1, F4, F5, F5b, F6, F7 — pure Helm/generator facts | `for s in 01 02 03 04 05 07; do bash scripts/$s-*.sh; done` |
| **unit** | F2a, F2b, F2c, F3 — controller + validator behaviour | `go test ./internal/controller/workloads/ -run TestVerifyPR416 -v -count=1` |
| **live** (read-only) | F7 — a real API server accepts every rendered document | `bash scripts/20-live-dryrun.sh` |
| **bite check** | F1 and F3 flip when the proposed fix is applied | `bash scripts/90-bite-check.sh` |

Environment used: go 1.24.1, helm v3.16.3, kubectl v1.36.2; live cluster ACK cn-hongkong
k8s **v1.36.1**-aliyun.1.

### Why the harness is trustworthy

* **Every reproduction has a passing control.** F1's conditional is shown to work when set
  explicitly; F3's factory is shown to work for the supported type; F2c shows migration *away*
  from a legacy type is still allowed (so the finding is not overstated); F6's controls show
  `controller.features=null` and `global=null` still render fine, isolating the defect to the
  new block; the live check's `apiVersion`-less control is correctly rejected.
* **F2a's admission stand-in is validated against the real validator** (F2b), and F2a fails
  loudly if the controller never attempts the patch, so it cannot pass vacuously.
* **`90-bite-check.sh`** applies the proposed fix for F1 and F3, confirms each test flips, then
  reverts and asserts the production diff is empty.

### Harness defects found and fixed this round

Recorded because each one briefly produced a wrong answer:

1. `02-render-all-shapes.sh` fed helm's **stderr** into the YAML parser, so a kubeconfig
   warning was misreported as a render defect in three shapes. Only after fixing it did the
   real nil-deref failures (F6) stand out.
2. F2a/F2b built `RoleBasedGroupValidator` with a **nil `Client`**, tripping the validator's own
   nil-client guard and contaminating the injected denial. Fixed with a fake client — which also
   turned F2b's control green.
3. `20-live-dryrun.sh` applied namespaced objects into `rbgs-system`, which does not exist on
   the review cluster; 7 of 12 objects were rejected for that reason alone and were briefly
   misread as a chart defect. Fixed by retargeting to `default` rather than creating a namespace
   on shared infrastructure.
4. `04-upgrade-guard.sh` grepped only for literal `helm upgrade` and missed `Makefile:253`
   (`$(HELM) upgrade`). Call-site count went 9 → 10.

### What was deliberately NOT run

The mutating live arms. This chart's `ClusterRole`, `ClusterRoleBinding` and
`ValidatingWebhookConfiguration` have **fixed names**, and an unrelated `rbgs-controller-manager`
is already live in `rbg-system` on the shared review cluster — a real `helm install` of the PR
head would overwrite its cluster-scoped objects, and the webhook config has
`failurePolicy: Fail`. So:

* **F4** rests on `helm template --is-upgrade` (deterministic, exact) plus a static call-site
  inventory. The end-to-end "install then upgrade" arm adds confirmation, not evidence.
* **F2a/F3** rest on the unit layer, which asserts the mechanism directly with the real
  validator.

To run the mutating arms, use a throwaway cluster — see `liveNote` in
[`verify-manifest.json`](verify-manifest.json) for the exact two scenarios.

---

## Lineage — PR #413 → #414 → #416

PR #413 and #414 shared head branch `0727-lagacy` and are both **closed**. PR #416 is a
redesign on a **new** branch off the same base, so #414's head is not an ancestor. Full
carry-over table is in `verify-manifest.json` under `lineage.carriedOver`; the highlights:

| From #414 | Now |
|---|---|
| F1 — chart uninstallable (apiVersion in a comment) | **Fixed**, kept as regression guard F7 |
| F7 — RBGSet has no validating webhook | **Fixed** — and present in *both* Helm and kustomize manifests |
| F8 — legacy type list triplicated | **Improved** — now switches on the shared constants |
| F4/F5 — whole-group terminal stop, frozen status | **Moot but the replacement is worse**: `handleLegacyWorkloads` is gone, so there is now *no* guard → F2a/F3 |
| F9 — cache selector dropped (latent) | **Escalated to reachable**, folded into F3 |
| F10 — grandfathering too loose | **Moot; opposite problem** → F2c |
| D1 — Helm nil-deref (disproved twice) | **Flipped to reproduced** → F6 |

> Note on D1/F6: Copilot raised a Helm nil-deref on both #413 and #414 and was **wrong both
> times**, because every block in the chart used the defensive `| default dict` idiom. The new
> `compatibility` block does not, so the concern is real this time — for that block only.

---

## Continuing after the fix

Everything durable lives on this branch: the harness, `verify-manifest.json`, the
`.last-reviewed` marker and the table above. Pull before a round, commit and push after
(including the advanced marker). One active reviewer at a time.

```bash
git fetch origin && git checkout verify/pr416-api-compat-toggle
bash docs/verification/pr416-api-compat-toggle/scripts/re-verify.sh
```

`re-verify.sh` takes **no sha** — it resolves the current PR head from `manifest.pr` and the
delta start from `.last-reviewed`, grafts the harness onto that ref, runs the unit layer, and
prints Fixed / Still-broken / Partial / Harness-update per finding. The script layer and the
live layer are run separately (they need `helm` and a cluster respectively).

**Polarity:** every finding here is a **contract** test — all of them should turn **green** when
fixed. There are no bug-canaries this round, so "all green" genuinely means "all fixed". (If a
future round adds a canary, it is fixed only when it *flips to red* and must then be inverted.)

Per-finding, after a fix lands:

* **F1** → `01-helm-default-rbac.sh` exits 0.
* **F2a** → the controller makes progress, or sets a condition whose reason/message mentions
  legacy/compat/v1alpha1 (the test accepts that as "partially addressed" and says so).
* **F2c** → scaling an existing legacy RBG is accepted.
* **F3** → `getOrCreateWorkloadReconciler` refuses legacy types with compat off.
* **F4** → no `helm upgrade` call site remains, or the guard becomes conditional.
* **F6** → all 8 value shapes render, and the "silent strip" shape disappears.

### Kickoff prompt for a fresh agent

> Continue the review pipeline for <https://github.com/sgl-project/rbg/pull/416>. State lives on
> branch `verify/pr416-api-compat-toggle` in the `cheyang/rbg` fork; read
> `docs/verification/pr416-api-compat-toggle/README.md` and run `scripts/re-verify.sh` (no sha
> needed). The live layer must stay read-only on any shared cluster — this chart's
> cluster-scoped object names are fixed and would clobber an existing install.
