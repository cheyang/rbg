# Verification — PR #549 — client-go apply-configuration schema (Reviewer: Claude)

PR: https://github.com/sgl-project/rbg/pull/549
Head reviewed: `b719f3c86e327c53558b5309ab7bbe917d194ba4`
Base: `98d3c4be8c4c018ddbeb516b66a4eba4981c225c`
Harness branch: `verify/clientgo-apply-schema-claude` (this branch; additive only, production code untouched)

## Premise (P0): CONFIRMED

The PR claims that `fake.NewClientset()` (the field-managed fake) rejects every
Create/Update/Patch/Apply of an RBG object because
`client-go/applyconfiguration/internal/internal.go` carries no schema for RBG
types. Reproduced on the **base** branch (no patch):

```
failed to convert new object (default/seeded; workloads.x-k8s.io/v1alpha2, Kind=RoleBasedGroup)
to smd typed: schema error: no type found matching: io.k8s.sigs.rbgs.api.workloads.v1alpha2.RoleBasedGroup
```

Seeding (`NewClientset(objs...)`) and `Get` work, exactly as claimed. The
problem is real, the component matches the patch, and the PR's own tests plus
the harness confirm the head does fix that symptom (writes and applies through
`fake.NewClientset()` now succeed). The premise is **Confirmed** — but the fix
as shipped embeds the *wrong* schema, which turns a loud error into silent
data loss.

## Observed vs expected

| ID | Claim | Layer | Observed at head | Expected | Verdict |
|----|-------|-------|------------------|----------|---------|
| P0 | base: `fake.NewClientset()` rejects RBG writes | unit | Update/Create fail with `no type found matching` (on base) | failure reproduces | **Confirmed** |
| F1 | embedded schema ≠ kube-apiserver CRD schema → `Extract<Kind>` drops schemaless subtree content; extract→apply deletes live fields | unit + integration | `ExtractRoleInstance` returns `{"components":[{"name":"prefill","size":1,"template":{"spec":{}}}]}`; re-apply deletes the live container (`containers now = []`); field sets diverge for 7 of 13 kinds | parity for every served kind; extract keeps what the manager applied | **Confirmed (broken)** |
| F2 | v1alpha1 `spec.components`: embedded merge-by-name vs server atomic list | unit + integration | server field set has one leaf `.spec.components`; embedded has per-key members; `ExtractInstance` returns `spec = {}` | atomic on both sides; extract keeps components | **Confirmed (broken)** |
| F3 | PR body describes a revision that is not the head | inspection | body claims `internal/clientgo` tests, no `pkg/generated/openapi`, CRD-derived schema, 57.6k-line internal.go; head has none of that | body matches the diff | **Confirmed (mismatch)** |

## How the harness works

`internal/verifyschema/` (additive, test-only) rebuilds the OpenAPI models
kube-apiserver uses for server-side apply on the RBG CRDs from
`config/crd/bases` (the CRD `openAPIV3Schema` per version, plus the
apiVersion/kind/metadata envelope and the
`x-kubernetes-group-version-kind` extension that
`k8s.io/apimachinery/pkg/util/managedfields` indexes by). It then:

1. **Field-set parity** (`TestServerParityFieldSets`,
   `TestServerParityRoleInstanceContainers`,
   `TestServerParityV1alpha1ComponentsAtomicity`): converts the same
   schema-driven full-coverage object with both converters (server vs
   `applyconfiguration.NewTypeConverter`, i.e. the embedded Go-derived
   schema) and compares `TypedValue.ToFieldSet()` — the exact bookkeeping
   the API server does when recording managed fields.
2. **End-to-end data loss** (`TestExtractRoleInstanceKeepsComponentTemplates`,
   `TestExtractInstanceV1alpha1KeepsComponents`): runs a real server-side
   apply through `managedfields.NewDefaultCRDFieldManager` (the constructor
   `customresource_handler.go` uses, with the same fakes the apimachinery
   test suite uses), then feeds the server-recorded managed fields into the
   generated `ExtractRoleInstance`/`ExtractInstance` helpers and re-applies
   the extract.
3. **Control** (`TestExtractControlEmbeddedRecordingKeepsTemplates`): the
   same extract workflow with the managed fields recorded by the *embedded*
   schema keeps the template. This is the harness-bites check: the failing
   tests are red because of the schema divergence, not because the
   workflow is broken. When the embedded schema is fixed to match the
   server, the contract tests go green and this control stays green.

### Fixture realism (cross-check against the real kube-apiserver builder)

`crosscheck/` is a standalone module that builds the server models with the
**real** apiextensions-apiserver code path
(`builder.BuildOpenAPIV3` + `builder.MergeSpecsV3`, as in
`customresource_handler.go:buildOpenAPIModelsForApply`) and prints the field
set for the same RoleInstance. Output
(`results/real-apiserver-crosscheck.txt`):

```
.spec.components[name="prefill"]
.spec.components[name="prefill"].name
.spec.components[name="prefill"].size
.spec.components[name="prefill"].template.spec
.spec.components[name="prefill"].template.spec.containers   ← whole list, no per-container members
```

identical to the hand-rolled models used by the in-repo harness, and
different from the embedded schema's per-container tracking
(`containers[name="c0"].image`, `containers[name="c0"].name`). Requires
network (`go mod tidy`); run with
`cd crosscheck && go run . ../../.. /config/crd/bases`.

## Root cause and proposed fix

The schema that ends up in `internal.go` is generated by openapi-gen from the
**Go API types**. kube-apiserver builds its server-side-apply schema for
custom resources from the **CRD** (`config/crd/bases`), and the two disagree
wherever the CRD declares `x-kubernetes-preserve-unknown-fields` (component
pod templates, instance templates, template patches) or omits list-type
markers (v1alpha1 `InstanceSpec.Components`). `Extract<Kind>` interprets
server-recorded managed fields with the embedded schema, so granularity
mismatches silently drop ownership and the documented
extract → modify → apply workflow then deletes live fields.

The fix is what the PR body already describes (for a revision that is not
pushed): derive the apply schema from the CRDs — `BuildOpenAPIV3` per
version + `MergeSpecsV3`, renamed to the Go-type REST names — and feed that
to `applyconfiguration-gen --applyconfig-openapi-schema`. That also removes
`pkg/generated/openapi` and the `+k8s:openapi-gen` markers.

## Continuing after the fix

```bash
# from a checkout of this branch
bash docs/verification/clientgo-apply-schema/scripts/re-verify.sh   # no arg: fetches current PR head
# or against an explicit ref:
bash docs/verification/clientgo-apply-schema/scripts/re-verify.sh <sha-or-branch>
```

All harness tests are **contract tests** (assert the intended correct
behavior): red = still broken, green = fixed. Exit 0 iff all findings are
fixed. `TestExtractControlEmbeddedRecordingKeepsTemplates` is a control and
must stay green. After the marker advances
(`git rev-parse HEAD > docs/verification/clientgo-apply-schema/.last-reviewed`,
committed), the next round picks up the delta automatically.

Prerequisites: Go (repo toolchain), no cluster, no KUBECONFIG. The in-repo
harness uses only vendored dependencies. The `crosscheck/` module needs
network once.

### Copy-paste kickoff prompt for a fresh agent

> You are resuming a PR review verification for
> https://github.com/sgl-project/rbg/pull/549. Check out branch
> `verify/clientgo-apply-schema-claude`, read
> `docs/verification/clientgo-apply-schema/README.md` and
> `verify-manifest.json`, then run
> `bash docs/verification/clientgo-apply-schema/scripts/re-verify.sh`.
> All tests are contract tests: red means the finding is still broken, green
> means fixed. Report per finding (F1, F2, F3) as Fixed / Still-broken /
> Partial / Harness-update, update the observed-vs-expected table above,
> advance `.last-reviewed` to the head you verified, and commit + push.
