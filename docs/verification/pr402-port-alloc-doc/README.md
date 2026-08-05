# pr402-port-alloc-doc — documentation-finding verification

Reproducible evidence for the findings raised while reviewing
**[PR #402 — doc: add port allocation and service discovery best practice](https://github.com/sgl-project/rbg/pull/402)**
(head `b412ed6c6789cf87083c2c6f6fbdbc3c879157d0`).

PR #402 is a **documentation-only** PR. It adds

- `doc/best-practice/zh/05-port-allocation-and-service-discovery.md`
- `doc/best-practice/en/05-port-allocation-and-service-discovery.md`
- `doc/best-practice/zh/05-port-allocation-and-service-discovery-guide.md`
- `doc/best-practice/en/05-port-allocation-and-service-discovery-guide.md`

Because it is a doc PR, **the implementation is the source of truth**. Every finding below is
a claim the *documentation* makes that the *code* does not honour. The harness therefore pins
the code's real behaviour and shows, line by line, where the prose diverges.

> **Production code and the reviewed documents are untouched by this branch.** The only files
> added are four `zz_verify_pr402_*_test.go` harness files plus this `docs/verification/`
> directory. `git status --short` shows nothing but untracked additions; `git diff --stat`
> over tracked files is empty (see `results/harness-bites.txt`, section "RESTORE VERIFICATION",
> and `results/l1-unit-final.txt`).

---

## Layers

| Layer | What it exercises | How to run |
|-------|-------------------|------------|
| **L1 unit** | the real production functions the doc describes: `InjectPortsIntoPod`, `newRandomAllocator`/`AllocateBatch`/`Release`, `AllocatePodScopedPorts`, `InjectComponentDiscovery`/`resolvePortRef`, `ConfigBuilder.Build`, `GetCompatibleHeadlessServiceName`, `GetServiceName` | `go test ./pkg/port-allocator/... ./pkg/component-discovery/... ./pkg/discovery/... ./pkg/utils/... -run 'PR402' -v -count=1` |
| **L2 live** | a real cluster: server-side dry-run of every YAML block in the four doc files, then one minimal RBG in a scratch namespace to observe the generated headless Services (F5) and the discovery ConfigMap payload (F4) | `export KUBECONFIG=…` then `scripts/l2-live-service-and-configmap.sh --live` |
| **L3 pod-level** | Pod env-var injection (`references` for F1, `*_ADDR` FQDNs for F3) | **NOT COMPLETED** — see [Environment limitations](#environment-limitations) |

L1 is the layer that carries the verdicts. All 14 top-level tests pass on the reviewed code
(`results/l1-unit-final.txt`, exit 0, 14 `--- PASS`, 0 `--- FAIL`).

### Running L1

```bash
cd <repo root>
go test ./pkg/port-allocator/... ./pkg/component-discovery/... \
        ./pkg/discovery/... ./pkg/utils/... -run 'PR402' -v -count=1
```

No external dependency: no envtest, no cluster, no network. `fake.NewClientBuilder()` backs the
F4/F5 tests. The F2 canary uses the **pigeonhole principle** (range smaller than the number of
pods), not a birthday-paradox collision, so it is deterministic and flake-free.

### Running L2

```bash
export KUBECONFIG=/root/.kube/config       # or wherever your kubeconfig lives
docs/verification/pr402-port-alloc-doc/scripts/l2-live-service-and-configmap.sh          # preflight + dry-run only
docs/verification/pr402-port-alloc-doc/scripts/l2-live-service-and-configmap.sh --live   # also create/observe in a namespace
NS=my-scratch-ns ... --live                # override the scratch namespace (default: pr402-verify)
```

The script honours `$KUBECONFIG` (falling back to `~/.kube/config`), aborts early with
`FATAL: cannot reach the cluster` if `kubectl version` fails, and **deletes its scratch
namespace on exit** via a trap. It never touches anything outside that namespace. Stage `[A]`
records the two environment limitations, `[B]` dry-runs the doc's YAML, `[C]` creates one
two-role RBG, `[D]` attempts the pod-level checks and SKIPs (rather than hanging) when no Pod
appears. Raw output: `results/l2-live.txt`.

### Re-verifying after a fix

```bash
docs/verification/pr402-port-alloc-doc/scripts/re-verify.sh <fixed-ref> --layers unit
# or, to auto-discover the current PR #402 head from manifest.pr:
docs/verification/pr402-port-alloc-doc/scripts/re-verify.sh --layers unit
```

`re-verify.sh` reads `verify-manifest.json`, grafts the harness onto the fixed ref, runs L1 and
prints a per-finding FIXED / STILL-BROKEN / FIXED(flipped) table honouring polarity. It refuses
to run on a dirty tree. `--layers unit` is required because this topic has no integration layer.

---

## Observed vs expected

| ID | Sev | The doc says (file:line) | The code actually does | Proving test | Verdict |
|----|-----|--------------------------|------------------------|--------------|---------|
| **F1** | major | `references` can take "already-allocated ports of other components in the same role"; the parameter table places **no scope constraint** on the target — while the doc's own worked example allocates `leader-grpc` as `RoleScoped`. `zh:313-344`, `en:316-347` | `InjectPortsIntoPod` (`pkg/port-allocator/manager.go:125-143`) builds **only** the PodScoped key `FormatPodScopedPortKey(refPodName, portName)`. A RoleScoped target yields the hard error `referenced port not found: prefill.leader.leader-grpc (key: pd-server-prefill-0-leader-0.leader-grpc)`, which bubbles out of `instance_scale.go:275-277` and **blocks Pod creation for the whole component**. Exactly **one** `key:` appears in the message — no fallback was attempted. Contrast `component-discovery`'s `resolvePortRef`, which tries both key shapes and reports `tried keys: "…", "…"`. | `TestVerifyPR402_F1_ReferencesOnlyResolvePodScoped_Canary` (2 subtests) + supporting `TestVerifyPR402_F1_PortAllocatorHasNoRoleScopedFallback_Contract`, `TestVerifyPR402_F3_PortRefResolvesBothScopes_Contract` | **Confirmed** |
| **F2** | major | dynamic allocation is presented as *the* solution to hostNetwork port conflicts (ASCII diagram: "RBG dynamically allocates a different port for each Pod"), and `PodScoped` is described as "each Pod gets its own port". `zh:222-245`, `en:225-248`, `zh:278`. `zh:249` says `random` is the only strategy. | `RandomAllocator`'s own comment (`pkg/port-allocator/random.go:28-31`) states uniqueness holds *within a single `AllocateBatch` call* but **not across calls or instances**; `manager.go:243-253` calls `AllocateBatch(1)` **once per Pod** — precisely the unguaranteed case. Measured (pigeonhole, `results/l1-unit-final.txt`): 9 pods over a range of 8 → duplicates `{30003:[2,7,8], 30004:[1,4], 30006:[0,6]}`; 33 pods over 32 → 11 duplicated ports; 2 pods over 1 → both got `30500`. `Release()` is an unconditional `return nil` (`random.go:81-83`) and silently accepts `1`, `65535`, `-1` — no accounting exists. `random` *is* the only registered strategy (confirms `zh:249`). | `TestVerifyPR402_F2_RandomAllocatorNotUniqueAcrossCalls_Canary` (3 subtests), `TestVerifyPR402_F2_ReleaseIsNoOp_Canary` + supporting `…_F2_SingleBatchIsUnique_Contract`, `…_F2_PodScopedAllocatesPerPod_Contract`, `…_F2_RandomIsTheOnlyRegisteredStrategy_Contract` | **Confirmed** |
| **F3** | minor | `LEADER_ADDR=pd-server-prefill-0-...s-pd-server-prefill.default.svc.cluster.local` — the ellipsis eats the `leader-0` pod-name segment **and the separating dot**, producing an illegal FQDN. In the router table the three rows `LEADER_ADDR` / `WORKER_0_ADDR` / `WORKER_1_ADDR` truncate to the **same byte string**. `zh:597/606/614/616/618`, `en:600/609/617/619/621` | `component_discovery.go:140-141` emits `<instanceName>-<component>-<index>.<svcName>.<ns>.svc.cluster.local`. Measured: `cd-demo-prefill-0-leader-0.s-cd-demo-prefill.pr402-verify.svc.cluster.local`, `…-worker-0.…`, `…-worker-1.…` — three **distinct** values, each a valid FQDN whose every label passes `validation.IsDNS1123Label` and which fits the 253-byte limit. The doc's printed string fails the FQDN regex. | `TestVerifyPR402_F3_ComponentAddressFQDNShape_Contract` (3 subtests), `TestVerifyPR402_F3_TruncatedDocRowsAreIndistinguishable_Contract` | **Confirmed** (doc defect; implementation correct) |
| **F4** | minor | the guide prints `/etc/rbg/config.yaml` with key order `size` → `roles` under `group`, and `size` → `instances` under each role. `zh:183-208`, `en:184-209`. The **same guide** prints the correct alphabetical order at `zh:228-239` — self-contradictory. | `config_builder.go:91` marshals via `sigs.k8s.io/yaml`, which round-trips through JSON and therefore emits mapping keys **alphabetically**. Measured: top level `group, roles`; `group` → `name, roles, size`; each role body → `instances, size`; the `roles` map sorted `decode` before `prefill` regardless of spec order. Byte-stable across 10 builds. The **live cluster** produced the same order (`results/l2-live.txt`, section `[C]`). | `TestVerifyPR402_F4_ConfigMapYAMLKeysAreAlphabetical_Contract` (2 subtests), `TestVerifyPR402_F4_KeyOrderIsStableAcrossBuilds_Contract`, plus L2 live ConfigMap dump | **Confirmed** (doc defect; implementation correct & deterministic) |
| **F5** | minor | "the naming rule is `s-{rbgName}-{roleName}`", stated unconditionally, e.g. `pd-inference`/`prefill` → `s-pd-inference-prefill`. `zh:32-38`, `en:33-39` | `GetCompatibleHeadlessServiceName` (`pkg/utils/service_utils.go:30-45`) first `Get`s a Service under the **legacy** scheme `{rbgName}-{roleName}` and, if it exists, **keeps the legacy name**. Measured: with `pd-inference-prefill` pre-existing → returns `pd-inference-prefill`, not `s-pd-inference-prefill`. Separately, `GetServiceName`/`GetWorkloadName` (`api/workloads/v1alpha2/helper.go:106-116`) truncate to 63 chars and strip a trailing hyphen, so a 58-char rbg name yields a name that is a *prefix* of the naive one, not the naive one. The doc has no compat and no length caveat. **L2 live** confirmed the *no-legacy* branch on a fresh cluster: `s-pd-inference-prefill`, `s-pd-inference-decode` (both `ClusterIP: None`). | `TestVerifyPR402_F5_HeadlessServiceNameHasLegacyFallback_Contract` (4 subtests), `TestVerifyPR402_F5_ServiceNameTruncatedAt63_Contract` (3 subtests), plus L2 live Service listing | **Confirmed** (doc defect; compat branch is intentional) |
| **F6** | nit | `en:666` renders `In-Place Update and In-Place Scheduling` as **plain text**, not a link, while its two siblings at `en:664-665` are links. | Checked with `ls doc/best-practice/en/`: only `01,02,03,05,10` exist — **there is no `04-*`**. The author deliberately avoided a dead link. | none needed (structural check) | **Not a defect — verified correct** |

L2 dry-run: the single extractable Kubernetes-object YAML block in the doc
(`05-…-01.yaml`, the `RoleBasedGroup` `pd-server`) was **accepted** by server-side admission
(1 accepted, 0 rejected). The `-guide.md` files contain no fenced YAML blocks at all.

---

## Polarity table

| ID | Polarity | Tests | Behaviour if the maintainers fix the **doc** (expected) | Behaviour if they instead change the **code** |
|----|----------|-------|--------------------------------------------------------|-----------------------------------------------|
| F1 | **canary** | `TestVerifyPR402_F1_ReferencesOnlyResolvePodScoped_Canary` | stays **GREEN** — it becomes the contract test for "references are PodScoped-only" | **FLIPS RED** once a RoleScoped fallback lands in `manager.go`. At that point invert the assertion (the RoleScoped row should then resolve and inject the port) or promote it to a contract test. Proven to flip: `results/harness-bites.txt` F1 block. |
| F2 | **canary** | `…_F2_RandomAllocatorNotUniqueAcrossCalls_Canary`, `…_F2_ReleaseIsNoOp_Canary` | stays **GREEN** — becomes the contract for "allocation is best-effort, not collision-free" | **FLIPS RED** once the allocator gains cross-call bookkeeping (the fix makes `AllocateBatch` return `requested 1 ports, but only 0 available in range` at exhaustion instead of colliding, and `Release(1)` rejects out-of-range). Then invert. Proven to flip: `harness-bites.txt` F2 block. |
| F3 | contract | `…_F3_ComponentAddressFQDNShape_Contract`, `…_F3_TruncatedDocRowsAreIndistinguishable_Contract` | stays **GREEN** (the doc gets corrected; these guard the FQDN template against future regression) | stays GREEN unless someone breaks the template |
| F4 | contract | `…_F4_ConfigMapYAMLKeysAreAlphabetical_Contract`, `…_F4_KeyOrderIsStableAcrossBuilds_Contract` | stays **GREEN** (guards against a serializer swap silently changing the documented order) | stays GREEN unless the serializer changes |
| F5 | contract | `…_F5_HeadlessServiceNameHasLegacyFallback_Contract`, `…_F5_ServiceNameTruncatedAt63_Contract` | stays **GREEN** (pins both the compat branch and the 63-char truncation) | goes RED if the legacy-compat branch is removed — which would be a **behaviour regression**, not a fix |
| F6 | n/a | none | n/a — already correct | n/a |

The three contract families were each proven non-vacuous by a **reverse perturbation** (see
below): break the code in the direction the doc describes and the test goes red.

## Harness-bites (does the harness actually bite?)

`results/harness-bites.txt` — recorded on the reviewed code with `go1.24.1 linux/amd64`. Each
block is (a) baseline, (b) same tests under a **temporary** production-code perturbation,
(c) restore + diff check.

| Finding | Perturbation applied | Result | Restore |
|---------|----------------------|--------|---------|
| F1 canary | add a RoleScoped fallback to `InjectPortsIntoPod`'s reference resolution in `manager.go` (**= the proposed fix**) | baseline `ok`; perturbed → `FAIL` — the RoleScoped row now resolves (`err=<nil>`, `LEADER_GRPC_PORT="30111"`), so the canary flips exactly as designed | `manager.go` restored, **0 diff lines** |
| F2 canaries | give `RandomAllocator` a persistent allocated-set plus a real `Release` (**= the proposed fix**) | baseline `ok`; perturbed → `FAIL` on the 33/32 and 2/1 pigeonhole rows (`requested 1 ports, but only 0 available in range`) **and** on `ReleaseIsNoOp` (`port 1 out of range [30000,30004)`) | `random.go` restored, **0 diff lines** |
| F3 contracts | break the FQDN template in `component_discovery.go` — drop the dot between pod name and service name, mimicking the doc's truncated string | baseline `ok`; perturbed → `FAIL`, injected value `cd-demo-prefill-0-worker-1s-cd-demo-prefill.…` no longer matches the FQDN regex, and all three router rows fail | `component_discovery.go` restored, **0 diff lines** |
| F4 contracts | replace `sigs.k8s.io/yaml` with an order-preserving hand-written emitter that puts `size` before `instances` — i.e. the order the doc prints | baseline `ok`; perturbed → `FAIL` with the diff `-instances,-size / +size,+instances` and `"22" is not less than "10"` for the `instances:`-before-`size:` textual check | `config_builder.go` restored, **0 diff lines** |
| F5 contracts | remove the legacy-name compatibility branch from `GetCompatibleHeadlessServiceName`, i.e. make the code match the doc's unconditional rule | baseline `ok`; perturbed → `FAIL` on the *legacy Service EXISTS* row: expected `pd-inference-prefill`, got `s-pd-inference-prefill` | `service_utils.go` restored, **0 diff lines** |

**Conclusion: the harness bites on all five findings.** Every canary flips under its proposed
fix, and every contract family goes red under a wrong-direction perturbation, so none of them
are vacuous assertions. The final section of `harness-bites.txt` re-verifies the restore:
`git diff --stat` over tracked files is empty (`OK: no modifications to tracked (production)
files.`) and the full harness is green again on the restored code.

---

## Environment limitations

Recorded verbatim; **no L3 evidence was fabricated.**

### 1. Controller image / CRD schema mismatch → **L3 Pod-level verification not completed**

The sandbox cluster runs `rbg-system/rbgs-controller-manager` with image
`rolebasedgroup/rbgs-controller:v0.8.0-cea2a47`, but the installed
`roleinstances.workloads.x-k8s.io` CRD declares `spec.restartPolicy` as an **object** while
that controller image writes a **string**. Every `RoleInstance` the controller tries to create
is rejected:

```
Warning FailedCreate roleinstanceset/pd-inference-decode
  create RoleInstance pd-inference-decode-0 ... failed error:
  RoleInstance.workloads.x-k8s.io "pd-inference-decode-0" is invalid:
  spec.restartPolicy: Invalid value: "string": spec.restartPolicy in body must be of type object: "string"
```

Consequently **zero Pods** were created (`pods in pr402-verify: 0`), and the Pod-level
observations — `references`-driven env injection (F1) and the injected `*_ADDR` FQDNs (F3) —
**could not be made on a live cluster**. Both are proven deterministically at L1 instead
(`pkg/port-allocator/zz_verify_pr402_doc_claims_test.go`,
`pkg/component-discovery/zz_verify_pr402_fqdn_test.go`).

What the cluster *did* produce, and what L2 therefore captured as real evidence:
`RoleInstanceSet` objects (`pd-inference-prefill`, `pd-inference-decode`, desired 2 each),
the headless Services (**F5**, no-legacy branch), and the discovery ConfigMap payload
(**F4**, key order). See `results/l2-live.txt` sections `[C]` and `[D]`.

### 2. `--enable-port-allocator` is not set → live port-allocation verification has no prerequisite

The deployed controller's args are exactly:

```
--metrics-bind-address=:8443 --leader-elect --health-probe-bind-address=:8081
--max-concurrent-reconciles=10 --kube-api-qps=20 --kube-api-burst=30
--scheduler-name=scheduler-plugins
```

No `--enable-port-allocator`, so `pkg/port-allocator` is **inert** on this cluster
(`AllocateBatch` returns `ErrPortAllocatorDisabled` when `!IsEnabled()`). F1 and F2 could not
have been exercised live here even with a matched controller/CRD pair. This is a *prerequisite*
gap, not a failed observation.

---

## Proposed documentation fixes (NOT applied — this branch changes no reviewed doc)

**F1 — constrain `references` to PodScoped, and say what happens otherwise.**
Replace the `references` description (zh:313-344 / en:316-347) with, e.g. (zh):

> `references` 只能引用**以 `PodScoped` 作用域分配**的端口。解析时控制器仅构造 PodScoped 注解键
> `<podName>.<portName>`（`pkg/port-allocator/manager.go:125-143`），**没有** RoleScoped 回退。
> 若被引用的端口是以 `RoleScoped` 分配的，注入会以
> `referenced port not found: <role>.<component>.<portName>` 硬失败，并阻塞该组件全部 Pod 的创建。
> 注意：`componentDiscovery` 的 `portRefs` 行为不同 —— 它会同时尝试 PodScoped 与 RoleScoped 两种键。

and fix the worked example so the referenced `leader-grpc` is allocated with
`scope: PodScoped` (or drop the `references` entry from it). English mirror at en:316-347.

**F2 — stop presenting dynamic allocation as a collision-free hostNetwork fix.**
At zh:222-245 / en:225-248 replace the "solution" framing with a caveat, e.g. (zh):

> **注意**：`random` 策略仅保证**单次 `AllocateBatch` 调用内**端口不重复，**不保证**跨调用或跨实例唯一
> （见 `pkg/port-allocator/random.go:28-31`）。`PodScoped` 对每个 Pod 单独调用一次分配，因此**不同 Pod
> 之间仍可能拿到相同端口**；`Release()` 目前是空实现，不做任何回收记账。在 `hostNetwork` 场景下请预留
> 足够大的端口区间（远大于同节点 Pod 数），并把端口冲突当作需要重试/告警处理的情况，而不是已被消除的问题。

and reword zh:278 from "每个 Pod 分配独立端口" to "为每个 Pod **单独分配**端口（端口值不保证互不相同）".

**F3 — print the FQDNs in full.**
At zh:597/606/614/616/618 and en:600/609/617/619/621 replace the ellipsis form with the real
template `<instanceName>-<component>-<index>.<svcName>.<ns>.svc.cluster.local`, e.g.:

```
LEADER_ADDR=pd-server-prefill-0-leader-0.s-pd-server-prefill.default.svc.cluster.local
WORKER_0_ADDR=pd-server-prefill-0-worker-0.s-pd-server-prefill.default.svc.cluster.local
WORKER_1_ADDR=pd-server-prefill-0-worker-1.s-pd-server-prefill.default.svc.cluster.local
```

(The router table in particular must show three *different* values.)

**F4 — print the ConfigMap in the serializer's actual (alphabetical) order.**
Rewrite the `/etc/rbg/config.yaml` listing at zh:183-208 / en:184-209 to match what
`sigs.k8s.io/yaml` emits — i.e. exactly the shape already printed at zh:228-239:

```yaml
group:
  name: pd-inference
  roles:
  - prefill
  - decode
  size: 2
roles:
  decode:            # decode before prefill: map keys are sorted
    instances:       # instances before size
    - address: pd-inference-decode-0.s-pd-inference-decode
      ports:
        http: 8000
    size: 2
  prefill:
    instances:
    - address: pd-inference-prefill-0.s-pd-inference-prefill
      ports:
        http: 8000
    size: 2
```

**F5 — qualify the Service naming rule.**
At zh:32-38 / en:33-39 append (zh):

> 命名规则为 `s-{rbgName}-{roleName}`，但有两个例外：
> 1. **兼容旧命名**：若集群中已存在按旧规则 `{rbgName}-{roleName}`（无 `s-` 前缀）命名的 Service，
>    控制器会继续沿用该旧名（`pkg/utils/service_utils.go:30-45`），因此从旧版本升级的集群里实际名字
>    可能没有 `s-` 前缀；
> 2. **长度截断**：名称超过 63 个字符时会被截断到 63 并去掉末尾的 `-`
>    （`api/workloads/v1alpha2/helper.go:106-116`）。
> 请始终以 `kubectl get svc` 的实际输出为准，不要在脚本里硬编码拼接出的名字。

**F6 — no change needed.** Keep the plain text until `doc/best-practice/en/04-*` exists.

---

## Continuing after the fix (possibly on another machine)

The harness lives on branch `verify/pr402-port-alloc-doc` of `https://github.com/cheyang/rbg.git`
and touches **no production code and no reviewed document**, so it grafts onto whatever the
fixed code is.

1. Graft it onto the updated PR head:
   ```bash
   git clone https://github.com/cheyang/rbg.git && cd rbg
   git remote add upstream https://github.com/sgl-project/rbg.git
   git fetch origin verify/pr402-port-alloc-doc
   git fetch upstream pull/402/head && git checkout FETCH_HEAD
   git checkout origin/verify/pr402-port-alloc-doc -- \
     docs/verification/pr402-port-alloc-doc \
     pkg/port-allocator/zz_verify_pr402_doc_claims_test.go \
     pkg/component-discovery/zz_verify_pr402_fqdn_test.go \
     pkg/discovery/zz_verify_pr402_configmap_keyorder_test.go \
     pkg/utils/zz_verify_pr402_svcname_test.go
   ```
   Or let the script do it: `docs/verification/pr402-port-alloc-doc/scripts/re-verify.sh --layers unit`
   (it resolves the PR head from `verify-manifest.json`'s `pr` field, so no local remote is needed).
2. Prereqs: **L1 = Go toolchain only** (no envtest, no cluster). **L2** = a reachable cluster via
   `$KUBECONFIG` plus permission to create/delete a namespace. **L3** = a cluster whose controller
   image and CRDs agree on `spec.restartPolicy` *and* which runs the controller with
   `--enable-port-allocator` (neither held on the sandbox used here).
3. Re-run L1, then optionally `scripts/l2-live-service-and-configmap.sh --live`.
4. Read the results through the **polarity table** above. Because #402 is a doc PR, the expected
   outcome of a correct fix is: **all 14 tests still green**, with the doc text changed. If a
   canary (F1/F2) flips red, the maintainers changed the *code* instead — invert that canary's
   assertion (or promote it to a contract test) and re-check the doc against the new behaviour.
   If an F5 contract goes red, the legacy-compat branch was removed — treat that as a behaviour
   regression, not a doc fix.
5. Re-run the harness-bites check (repeat the perturbations tabulated above) to confirm the
   harness still bites on the new code, and **verify `git status --short` is clean afterwards**.
6. Advance the marker: `echo <new-head-sha> > docs/verification/pr402-port-alloc-doc/.last-reviewed`.
   Current value: `b412ed6c`.

### Kickoff prompt for a fresh agent

```text
Continue a review-verification task for sgl-project/rbg PR #402
(https://github.com/sgl-project/rbg/pull/402), a documentation-only PR adding
doc/best-practice/{zh,en}/05-port-allocation-and-service-discovery{,-guide}.md.

The verification harness is already written and pushed. Get it with:
  git clone https://github.com/cheyang/rbg.git && cd rbg
  git remote add upstream https://github.com/sgl-project/rbg.git
  git fetch origin verify/pr402-port-alloc-doc
  git checkout verify/pr402-port-alloc-doc

Read docs/verification/pr402-port-alloc-doc/README.md in full — especially the
observed-vs-expected table, the polarity table, the harness-bites table, and
"Environment limitations". It documents six findings F1..F6 against PR head
b412ed6c (recorded in .last-reviewed): F1/F2 major, F3/F4/F5 minor, F6 verified
not-a-defect. F1 and F2 are CANARY tests (they pin current CODE behaviour the doc
contradicts); F3/F4/F5 are CONTRACT tests.

Your job:
1. Fetch the CURRENT PR #402 head and diff it against b412ed6c to see what the
   author changed this round.
2. Re-run Layer 1 (Go toolchain only, no cluster needed):
     go test ./pkg/port-allocator/... ./pkg/component-discovery/... \
             ./pkg/discovery/... ./pkg/utils/... -run 'PR402' -v -count=1
   or drive it via docs/verification/pr402-port-alloc-doc/scripts/re-verify.sh --layers unit
   (it auto-resolves the PR head from verify-manifest.json's "pr" field).
3. Since #402 is a DOC PR, the expected outcome of a correct fix is: all 14 tests
   still GREEN, with the prose corrected. Check each finding's doc lines against
   the README's "Proposed documentation fixes" section and report which findings
   the new revision actually addresses.
4. If a canary (F1/F2) flipped RED, the maintainers changed the CODE instead of
   the doc — say so, invert that canary's assertion (or promote it to a contract
   test), and re-read the doc against the new behaviour.
5. Optional Layer 2 (needs $KUBECONFIG and namespace create/delete rights):
     docs/verification/pr402-port-alloc-doc/scripts/l2-live-service-and-configmap.sh --live
   It creates ONE minimal RBG in a scratch namespace (default pr402-verify) and
   deletes the namespace on exit. Layer 3 (Pod env injection for F1/F3) needs a
   cluster whose controller image and CRDs agree on spec.restartPolicy AND which
   runs the controller with --enable-port-allocator; it was NOT achievable on the
   original sandbox and is documented as such — do not fabricate L3 evidence.
6. Re-run the harness-bites perturbations from the README table to confirm the
   harness still bites, then restore and prove `git status --short` shows only
   untracked harness files. NEVER modify production code or the reviewed docs.
7. Report an observed-vs-expected table, the polarity outcomes, and advance
   .last-reviewed to the new head.

Do not push to `upstream`. Do not open a PR or comment on any GitHub PR/issue
unless explicitly asked.
```

---

## Artifacts

| File | What it holds |
|------|---------------|
| `results/l1-unit-final.txt` | authoritative L1 re-run on the **final clean tree** (14 PASS / 0 FAIL, `EXIT=0`) — includes the raw allocated-port lists and duplicate maps behind F2, and the exact FQDN / key-order / Service-name values behind F3/F4/F5 |
| `results/l1-unit.txt`, `results/l1-unit.json` | the earlier verbose + machine-readable L1 run |
| `results/l1-full-packages.txt` | full test suites of the four touched packages — all `ok`, i.e. the harness files break nothing |
| `results/harness-bites.txt` | the five perturbation experiments plus the restore verification |
| `results/l2-live.txt` | live-cluster log: preflight limitations, YAML dry-run, generated Services (F5), ConfigMap payload (F4), and the L3 SKIP diagnosis |
| `scripts/l2-live-service-and-configmap.sh` | the L2/L3 driver |
| `scripts/re-verify.sh` | polarity-aware re-verification driver (from the `review-finding-verifier` skill, unmodified) |
| `verify-manifest.json` | machine-readable finding → tests → polarity → layer map, consumed by `re-verify.sh` |
| `.last-reviewed` | `b412ed6c` — the PR head this round reviewed |

---

# Round 2 addendum — the L3 gap is now closed

**Date:** 2026-08-05. **Reviewed head:** unchanged (`b412ed6c`) — this round adds
evidence, not a new review of new code.

The first round recorded two environment limits that blocked the live layer:
the cluster could not create Pods at all (controller `v0.8.0-cea2a47` wrote
`restartPolicy` as a bare string while the CRDs required an object — a
23-commit controller lag, not a CRD problem), and the controller had no
`--enable-port-allocator`. Both are resolved:

```bash
helm upgrade rbgs deploy/helm/rbgs -n rbg-system \
  --set controller.features.portAllocator.enabled=true
# chart 0.8.0-alpha.3, image rolebasedgroup/rbgs-controller:v0.8.0-47cfe17
```

The resulting controller args — reproduced verbatim in the new results file —
are themselves evidence for the doc's flag table:

```
"--enable-port-allocator=true" "--port-allocate-strategy=random"
"--start-port=30000" "--port-range=5000"
```

New script `scripts/l3-live-pod-level.sh`, output `results/l3-live-pod-level.txt`
(exit 0). It creates one `standalonePattern` RBG and one
`customComponentsPattern` RBG in a scoped namespace and deletes it on exit.

## What the live layer confirmed

| Doc claim | Location | Live result | Verdict |
|-----------|----------|-------------|---------|
| The `RBG_*` env vars and their Downward API sources | zh:91-118 / en:94-121 | **6** injected on a standalone role — exactly the base (2) + Stateful (1) + RoleInstanceSet (3) sub-tables; label paths match character for character | **Correct** |
| `RBG_LWP_*` are LeaderWorkerPattern-only | zh:114-117 | absent on the standalone role, as the doc's sub-table split implies | **Correct** |
| Pod identity via `hostname` + `subdomain` | zh:33-38 | `hostname=a-prefill-0`, `subdomain=s-a-prefill` | **Correct** |
| ConfigMap volume `rbg-cluster-config` mounted read-only at `/etc/rbg` | zh:200-210 | `[('rbg-cluster-config', '/etc/rbg', True)]` | **Correct** |
| Headless Service `s-{rbgName}-{roleName}`, `clusterIP: None`, `publishNotReadyAddresses: true` | zh:32-38 | `s-a-prefill None true`, `s-pd-prefill None true` | **Correct** |
| `group.size` is the number of **roles**, not Pods | zh:190-196 | `group.size: 1` with one role, while `roles.prefill.size: 2` for two replicas | **Correct** |
| Allocated ports fall in `30000`–`34999` | zh:249-253 | `LEADER_GRPC_PORT=31349` | **Correct** |
| Helm values path `controller.features.portAllocator.*` | zh:255-262 | the four flags above reached `/manager` | **Correct** |
| ConfigMap key order printed as `size`→`roles` / `size`→`instances` | guide zh:183-208 | actual order is `name`→`roles`→`size` and `instances`→`size` | **Wrong — F4 confirmed live** |
| `LEADER_ADDR` sample value | zh:597-618 | real value `pd-prefill-0-leader-0.s-pd-prefill.<ns>.svc.cluster.local` — the doc's ellipsis swallows `-leader-0` **and** its separating dot | **Wrong — F3 confirmed live** |

F3 and F4 were previously proven only at unit level; they are now confirmed
against real Pods and a real ConfigMap. F1, F2 and F5 remain unit-level findings
(see below).

## What is still unit-level only, and why that is the right layer

- **F1** (`references` resolves only PodScoped keys, so referencing a RoleScoped
  port hard-fails and blocks Pod creation) and **F2** (the `random` allocator does
  not guarantee uniqueness across calls, and `Release()` is a no-op) are proven by
  deterministic tests, not live runs. That is deliberate: F2 in particular relies
  on the pigeonhole principle (9 PodScoped ports out of a range of 8 → a duplicate
  is *certain*), which is exact and reproducible. Reproducing it live would mean
  provoking a probabilistic collision in a 5000-port range — flaky evidence for a
  claim the unit layer already settles.
- **F5** (the legacy Service-name fallback) needs a pre-existing Service under the
  old `{rbgName}-{roleName}` name. The live run exercises only the no-legacy
  branch (`s-` prefix), which it confirms; the fallback branch stays with the
  fake-client test.

## Caveat

The live run allocates a single PodScoped port on a single component, so it shows
that allocation and injection work — it does **not** exercise the collision
scenario F2 is about. Do not read `results/l3-live-pod-level.txt` as evidence
that port allocation is collision-free; it is not, and F2 stands.
