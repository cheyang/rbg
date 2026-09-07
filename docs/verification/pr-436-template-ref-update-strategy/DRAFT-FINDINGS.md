# PR #436 — Draft findings (stage ①)

PR: https://github.com/sgl-project/rbg/pull/436
"fix: align template ref and update strategy validation" (googs1025), base main, 1 commit.

## Intent
Two alignment fixes to the v1alpha2 API shape:
1. Stop rejecting `templateRef` without `patch` at the controller layer (patch is optional in the
   API type and template resolution already handles nil/empty patches).
2. Add kubebuilder `Enum={RecreatePod,InPlaceIfPossible,InPlaceOnly}` to two `UpdateStrategyType`
   fields that previously accepted arbitrary strings.

## Code-level analysis (proven by reading source)
- `applyStrategicMergePatch` (api/workloads/v1alpha2/helper.go) returns a deep copy of the base
  template when the patch is nil/empty. → removing the controller "patch required" check is
  consistent with resolution behavior. **Confirmed.**
- The enum covers *all* `UpdateStrategyType` constants (RecreatePod/InPlaceIfPossible/InPlaceOnly). **Complete.**
- `patch` is optional in the CRD schema (only `name` is required; no CEL requires patch) and the
  validating webhook `ValidateCreate` does **not** call `ValidateRoleTemplateReferences`. → the
  removed controller check was the *only* gate; removing it is the complete fix, no other layer
  blocks patch-less templateRef. **Confirmed.**
- `ValidateRoleTemplateReferences` is called only in the controller
  (`internal/controller/workloads/rolebasedgroup_controller.go:324`), not the webhook. So on the
  pre-PR controller a patch-less templateRef RBG is *admitted* but rejected by the reconciler
  (stays NotReady).
- The default "InPlaceIfPossible" for `RollingUpdate.Type` / `RoleInstanceSetUpdateStrategy.Type`
  is applied at runtime (controller), **not** via a CRD `default` (no `+kubebuilder:default`
  marker). The RBG reconciler defaults `""` → `InPlaceIfPossible` before creating the underlying
  RIS (`pkg/reconciler/roleinstanceset_reconciler.go:241`). → controller-created RIS always carry
  a valid type.
- `ValidateRollingUpdate` (webhook) checks maxSurge/maxUnavailable/partition but **not** `Type`.
  So the enum is the **sole** gate for the type value — no webhook/controller defense-in-depth.
  Author documented this as intentional ("enum-only").
- `deploy/kubectl/manifests.yaml` regenerated consistently (enum present in all 3 bundled CRDs).

## Findings to verify

### F1 [major→verify] Enum vs default-omitted create path
Adding `Enum` to an `omitempty` field with no CRD default. The common pattern of omitting `type`
(relying on documented "Default is InPlaceIfPossible") must still pass admission. Absent optional
fields skip enum validation (standard), so this *should* pass — but the author never ran envtest
(asset fetch failed), and no e2e covers it. **Prove on real cluster.**

### F2 [major→verify] Upgrade hazard for objects storing a now-invalid type
Pre-PR, `updateStrategy.type`/`rollingUpdate.type` accepted arbitrary strings (no enum, no webhook
check on Type). A directly-created RIS/RBG role could store a value outside the new enum. After
applying the enum CRD, any spec-bearing update re-runs admission against the stored value → the
object could become un-updatable. Controller-created RIS are safe (controller defaults valid).
**Prove via old-CRD-store-bogus → new-CRD → update-rejected on real cluster.**

### F3 [the PR's goal — verify] Intended rejections land at CRD admission
- RBG `rolloutStrategy.rollingUpdate.type` invalid → rejected by enum.
- Direct RIS `updateStrategy.type` invalid → rejected by enum.
- Valid values + omitted → accepted.
**Prove on real cluster (kubectl apply).**

### F4 [major→verify] templateRef-without-patch reaches Ready end-to-end
Controller-side change; needs PR-head controller binary. Pre-PR controller admits (webhook) but
rejects in reconciler → NotReady. With PR-head controller → should reach Ready. **Prove by
deploying PR-head image to the cluster (or envtest fallback).**

### F5 [minor] v1alpha1 left inconsistent
The PR relaxes v1alpha2 (patch no longer required) but v1alpha1
`roletemplate_validation.go:134` still rejects templateRef without templatePatch. If v1alpha1 is
still served/supported, the "alignment" is half-done. Question for author.

### F6 [minor/coverage] No e2e; envtest unverified by author
PR body admits `make test-envtest` failed locally. The 3 new envtest cases + the unit test were
never run by the author. No `test/e2e/` coverage for this functional change. Closed by this
verification (live cluster for A/B/C, plus envtest run).

## Preliminary verdict
Likely **COMMENT** with F1/F2/F4 proven and F5 as a question — unless F2 or F4 fail live, in which
case the corresponding finding escalates to a blocker/major request-changes.
