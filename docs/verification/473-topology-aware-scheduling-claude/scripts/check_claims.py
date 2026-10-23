#!/usr/bin/env python3 -I
"""L1 claim-check harness for the review of PR #473 (KEP-473: Topology-Aware Scheduling).

The PR under review is documentation-only (keps/473-topology-aware-scheduling/
{README.md,kep.yaml}); the only Go files in the GitHub diff are a cherry-pick of
the already-merged main commit #495 (byte-identical, see check C9). The
reviewable defects are therefore (a) defects inside the KEP document itself
(contradictions, unexplained renderings, stale claims) and (b) factual claims
about the codebase / upstream schedulers that are checkable deterministically.

Polarity (round 3):
  * LIVE findings are BUG-CANARIES on the KEP text.
      DEFECT-PRESENT  -> the finding still holds (harness green, finding live)
      DEFECT-ABSENT   -> flipped: the KEP was amended, finding is fixed
  * FIXED findings were inverted per the review-finding-verifier skill: each
    flipped canary is now a fixed-guard CONTRACT that asserts the fix is still
    present (it fails again if the author regresses the text). Their ids keep
    the historical F-numbers with polarity=contract.
  * LF* are fixed-guard contracts for the round-3 ledger findings (the frozen
    debate numbering) that were fixed by the head-99b991c5 revision.
  * P0/C*/G* are CONTRACT checks on facts about the world.

Ledger-id mapping (frozen debate numbering <-> checks in this file):
  ledger F1  (contract freeze / cache-backed policy read) -> LF1
  ledger F2  (resync as drift trigger)                   -> LF2
  ledger F3  (Koordinator cross-role-only example)       -> F11 (contract)
  ledger F4  (webhooks-disabled vs freeze)               -> LF4
  ledger F5  (Role declaration immutability)             -> F12 (contract)
  ledger F6  (KAI subGroup name length budget)           -> LF6
  ledger F7  (DNS-1123 rule for Role names unscoped)     -> F13 (canary, LIVE)
  ledger F8  (late-policy finalizer question)            -> no canary (question)
  ledger F9  (RBAC unaddressed)                          -> F8  (canary, LIVE)
  ledger F10 (minMember/minSubGroup zero-value wording)  -> LF10
  ledger F11 (PRR questionnaire missing)                 -> LF11
  ledger F12 (binding injection ordering)                -> LF12 (via reconcile flow)
  ledger F13 (Volcano example omits subGroupSize)        -> LF13
  ledger F14 (hasSubGroupPolicy symbol)                  -> F7  (contract)
  ledger F15 (typo 'upto')                               -> F10 (contract)
  ledger F16 (TopologyPolicyLate naming, nit)            -> no canary (nit)

Usage:
  python3 -I check_claims.py --repo <repo-root> --evidence <evidence-dir> [--kep <path>]
    --repo      checkout of the repo whose working tree holds the KEP (PR head)
    --evidence  the verification dir's results/ directory (holds pr-body.md and
                upstream/ snapshots; falls back to sibling-of-script layout)
Exit code 0 iff every live canary reports DEFECT-PRESENT and every contract passes.
"""
import argparse
import os
import re
import subprocess
import sys

CHECKS = []  # (id, kind, label, fn)


def check(cid, polarity, label):
    def deco(fn):
        CHECKS.append((cid, polarity, label, fn))
        return fn
    return deco


def read(path):
    with open(path, "r", encoding="utf-8") as f:
        return f.read()


def section(text, start, end):
    """Return the markdown section between two heading anchors."""
    i = text.find(start)
    j = text.find(end, i + 1) if i >= 0 else -1
    if i < 0 or j < 0:
        return ""
    return text[i:j]


# ---------------------------------------------------------------- canaries ---

KEP = {"text": "", "repo": "", "evidence": "", "body": ""}


# ------------------------------------- round-4 fixed guards (flipped r4) ----
# The round-3 canaries F8 (RBAC unaddressed, ledger F9) and F13 (unscoped
# DNS-1123 role rule, ledger F7) flipped to DEFECT-ABSENT at head 9f682b0.
# Per the verifier skill they are inverted into contracts asserting the fix.


@check("LF9", "contract", "[fixed r4] the KEP enumerates the required RBAC matrix (ledger F9)")
def lf9():
    kep = KEP["text"]
    intro = "The required RBAC surface is listed below" in kep
    rows = {
        "clusternetworktopologies verbs+status+finalizers":
            "`topology.x-k8s.io/clusternetworktopologies` (cluster) | `get`, `list`, `watch`, `patch`, `update`" in kep
            and "`/status`: `get`, `patch`, `update`" in kep,
        "workload resources in managed namespaces":
            "`workloads.x-k8s.io/rolebasedgroups`, `roleinstances`, `coordinatedpolicies`" in kep,
        "scheduler PodGroups incl. finalizers":
            "Scheduler PodGroups: `scheduling.volcano.sh`" in kep and "`/finalizers`: `update`" in kep,
        "Managed native create/delete (hypernodes/topologies)":
            "Managed `kai.scheduler/topologies` or `topology.volcano.sh/hypernodes` (cluster) | `get`, `list`, `watch`, `create`, `patch`, `update`, `delete`" in kep,
        "Imported native read-only":
            "Imported `kai.scheduler/topologies` or `scheduling.koordinator.sh/clusternetworktopologies` (cluster) | `get`, `list`, `watch` only" in kep,
        "nodes + CRD read-only":
            "Core `nodes`; `apiextensions.k8s.io/customresourcedefinitions` (cluster) | `get`, `list`, `watch`" in kep,
        "cross-namespace retention visibility":
            "Cross-namespace topology retention requires visibility into every namespace" in kep,
    }
    ok = intro and all(rows.values())
    return ok, "intro=%s; " % intro + "; ".join("%s=%s" % (k, v) for k, v in rows.items())


@check("LF7", "contract", "[fixed r4] the DNS-1123 rule is scoped to topology-bearing compiler sources (ledger F7)")
def lf7():
    kep = KEP["text"]
    scoped = ("Topology-bearing policy-rule names and Role names used as sources by a topology-bearing compiled plan"
              " must be DNS-1123 labels" in kep)
    no_tightening = "adds no stricter naming admission to topology-free RBGs or gang-only policies" in kep
    unqualified_gone = "Role names and topology-bearing policy-rule names are validated at admission" not in kep
    # the codebase half of the round-3 finding: RoleSpec.Name still has no
    # API-wide pattern, so only the scoped reading is compatible with it.
    types_src = read(os.path.join(KEP["repo"], "api", "workloads", "v1alpha2", "rolebasedgroup_types.go"))
    role_block = re.search(r"type RoleSpec struct.*?Name string `json:\"name\"`", types_src, re.S)
    no_pattern = bool(role_block) and "Pattern" not in role_block.group(0)
    ok = scoped and no_tightening and unqualified_gone and no_pattern
    return ok, "scoped sentence=%s; no-tightening sentence=%s; unqualified sentence gone=%s; RoleSpec.Name pattern-free=%s" % (
        scoped, no_tightening, unqualified_gone, no_pattern)


# --------------------------------------- fixed guards (inverted canaries) ---
# Each of these was a round-1/round-2 canary that flipped to DEFECT-ABSENT at
# some reviewed head. Per the verifier skill they are inverted into contracts:
# they now assert the FIX is present and fail if the KEP regresses.

@check("F1", "contract", "[fixed r2] downgrade/rollback section documents the finalizer escape hatch")
def g_f1():
    downgrade = section(KEP["text"], "## Upgrade / Downgrade Strategy", "## Version Skew")
    ok = "finalizer" in downgrade and ("escape hatch" in KEP["text"].lower() or "escape hatch" in downgrade.lower())
    return ok, "downgrade section mentions finalizer=%s; escape hatch documented=%s" % ("finalizer" in downgrade, ok)


@check("F2", "contract", "[fixed r2] RBGSet guard stated once, self-contained")
def g_f2():
    kep = KEP["text"]
    selfcontained = "requires no cross-resource child lookup" in kep
    cross = "lists children from the informer cache" in kep
    return selfcontained and not cross, "self-contained guard=%s; contradictory informer-cache listing=%s" % (selfcontained, cross)


@check("F3", "contract", "[fixed r2] preferred<=required level ordering is validated (InvalidLevelOrder)")
def g_f3():
    kep = KEP["text"]
    return "InvalidLevelOrder" in kep, "InvalidLevelOrder validation documented=%s" % ("InvalidLevelOrder" in kep)


@check("F4", "contract", "[fixed r2] PR body does not claim an absent in-tree Chinese translation")
def g_f4():
    body = KEP["body"]
    claims = "Chinese translation" in body
    kep_dir = os.path.join(KEP["repo"], "keps", "473-topology-aware-scheduling")
    files = sorted(os.listdir(kep_dir)) if os.path.isdir(kep_dir) else []
    has_zh = any(("zh" in f.lower() or "cn" in f.lower()) for f in files)
    return (not claims) or has_zh, "body claims translation=%s; kep dir has zh file=%s" % (claims, has_zh)


@check("F5", "contract", "[fixed r2] Generated Group Names covers every dialect")
def g_f5():
    sec = section(KEP["text"], "#### Generated Group Names", "#### Translation Matrix")
    covers = ("Volcano" in sec and "Koordinator" in sec and "KAI" in sec)
    return covers, "naming rules cover Volcano/Koordinator/KAI=%s" % covers


@check("F6", "contract", "[fixed r2] examples do not partition unconstrained roles per instance")
def g_f6():
    kep = KEP["text"]
    kai_partition = bool(re.search(r"name: decode-0", kep)) and bool(re.search(r"name: decode-1", kep))
    koord_partition = "infer-0-decode-0" in kep and "infer-0-prefill-0" in kep
    role_level = "role-decode-" in kep and "not split into" in kep
    return (not kai_partition) and (not koord_partition) and role_level, \
        "KAI decode-0/1 subGroups=%s; Koordinator per-instance members=%s; role-level decode documented=%s" % (
            kai_partition, koord_partition, role_level)


@check("F7", "contract", "[fixed] KEP no longer cites the nonexistent hasSubGroupPolicy (ledger F14)")
def g_f7():
    kep = KEP["text"]
    return "hasSubGroupPolicy" not in kep, "hasSubGroupPolicy cited=%s" % ("hasSubGroupPolicy" in kep)


@check("F9", "contract", "[fixed r2] Koordinator PodGroup CRD dialect detection documented (ledger F25 mitigation)")
def g_f9():
    kep = KEP["text"]
    mitigated = ("detects the installed dialect at runtime" in kep
                 or "detects the installed PodGroup CRD dialect" in kep)
    sigs_unsupported = "scheduling.x-k8s.io" in kep and "minMember >= 1" in kep
    return mitigated and sigs_unsupported, "runtime dialect detection=%s; sigs CRD >=1 divergence documented=%s" % (
        mitigated, sigs_unsupported)


@check("F10", "contract", "[fixed r2] typo 'upto' absent (ledger F15)")
def g_f10():
    return "upto" not in KEP["text"], "doc comment contains 'upto'=%s" % ("upto" in KEP["text"])


@check("F11", "contract", "[fixed r3] Koordinator cross-role-only variant declared unsupported; Phase 2 members are role-level (ledger F3)")
def g_f11():
    kep = KEP["text"]
    unsupported = "cross-role-only variant" in kep and "unsupported on the inspected baseline" in kep
    per_instance = ("infer-0-prefill-0" in kep or "infer-0-prefill-1" in kep)
    role_level = "k-infer-0-prefill-<hash12>" in kep and "k-infer-0-decode-<hash12>" in kep
    return unsupported and (not per_instance) and role_level, \
        "cross-role-only unsupported=%s; stale per-instance members=%s; role-level Phase 2 members=%s" % (
            unsupported, per_instance, role_level)


@check("F12", "contract", "[fixed r3] immutability is scoped to topology declarations, not whole Role (ledger F5)")
def g_f12():
    kep = KEP["text"]
    whole_role = "Role declaration immutable from create onward" in kep
    scoped = ("topology declaration is immutable for the RBG lifecycle" in kep
              or "workload topology declaration is immutable" in kep)
    ordinary = "Ordinary changes remain allowed only when they preserve the accepted physical membership layout" in kep
    return (not whole_role) and scoped and ordinary, \
        "whole-Role immutability claim=%s; topology-scoped wording=%s; ordinary changes allowed=%s" % (
            whole_role, scoped, ordinary)


# ------------------------------------------- round-3 ledger fixed guards ---

@check("LF1", "contract", "[fixed r3] contract freeze specifies the uncached apiReader guard + explicit topologyPolicyRequired (ledger F1)")
def lf1():
    kep = KEP["text"]
    uncached = ("read the policy through the uncached `apiReader`" in kep
                and "any read error other than `NotFound` blocks acceptance" in kep)
    dependency = "topologyPolicyRequired" in kep and "TopologyPolicyPending" in kep
    downgraded = ("persist the complete policy before the RBG" in kep
                  and "normal launch ordering" not in kep)
    # codebase fact: the base controller already has an uncached reader
    ctrl = os.path.join(KEP["repo"], "internal", "controller", "workloads", "rolebasedgroup_controller.go")
    api_reader = False
    if os.path.exists(ctrl):
        src = read(ctrl)
        api_reader = "apiReader" in src and "mgr.GetAPIReader()" in src
    return uncached and dependency and downgraded and api_reader, \
        "uncached freeze read=%s; explicit dependency=%s; ordering claim downgraded=%s; base controller exposes apiReader=%s" % (
            uncached, dependency, downgraded, api_reader)


@check("LF2", "contract", "[fixed r3] no 'controller resync' drift trigger; event/retry wording only (ledger F2)")
def lf2():
    kep = KEP["text"]
    resync = "resync" in kep.lower()
    events = "enqueue referencing RBGs" in kep or "Policy create/update/delete events enqueue" in kep
    return (not resync) and events, "resync mentioned=%s; event-driven requeue documented=%s" % (resync, events)


@check("LF4", "contract", "[fixed r3] webhooks-disabled semantics reconciled with the contract freeze (ledger F4)")
def lf4():
    kep = KEP["text"]
    must_enable = "Clusters using topology must enable v1alpha2 validating webhooks" in kep
    reconciled = "retains the previous bindings" in kep and "rejects a topology/layout mismatch" in kep
    stale_render = "renders the latest desired" in kep
    return must_enable and reconciled and (not stale_render), \
        "webhooks required=%s; controller-rejects-mismatch wording=%s; stale 'renders latest desired'=%s" % (
            must_enable, reconciled, stale_render)


@check("LF6", "contract", "[fixed r3] KAI subGroup names carry an explicit 63-char budget (ledger F6)")
def lf6():
    kep = KEP["text"]
    kinds = ("policy-<source[0:20]>" in kep and "instance-<role[0:20]>" in kep and "role-<source[0:20]>" in kep)
    budget = "names stay below 63 characters" in kep or "below the 63-character limit" in kep
    test63 = "63-character RBG/role/rule names" in kep or "63 characters" in kep
    return kinds and budget and test63, "kind-prefixed bounded forms=%s; budget stated=%s; 63-char tests promised=%s" % (
        kinds, budget, test63)


@check("LF10", "contract", "[fixed r3] zero-value threshold rendering is stated consistently (ledger F10)")
def lf10():
    kep = KEP["text"]
    stale = "render only where gang is configured" in kep
    zero = "Nonzero thresholds come only from gang configuration" in kep
    volcano_zero = "render `minMember: 0`" in kep or "render minMember: 0" in kep
    kai_zero = "minSubGroup: 0" in kep and "minMember: 0" in kep
    return (not stale) and zero and volcano_zero and kai_zero, \
        "stale 'only where gang configured'=%s; nonzero-only-from-gang=%s; Volcano zero=%s; KAI zero=%s" % (
            stale, zero, volcano_zero, kai_zero)


@check("LF11", "contract", "[fixed r3] Production Readiness Review questionnaire present (ledger F11)")
def lf11():
    kep = KEP["text"]
    section_present = "## Production Readiness Review Questionnaire" in kep
    gate = "RBGTopologyAwareScheduling" in kep
    kep_yaml = read(os.path.join(KEP["repo"], "keps", "473-topology-aware-scheduling", "kep.yaml"))
    yaml_gate = "RBGTopologyAwareScheduling" in kep_yaml and "metrics:" in kep_yaml
    return section_present and gate and yaml_gate, \
        "PRR section=%s; feature gate named=%s; kep.yaml gates+metrics=%s" % (section_present, gate, yaml_gate)


@check("LF12", "contract", "[fixed r3] reconcile flow injects bindings before Role create/update (ledger F12)")
def lf12():
    kep = KEP["text"]
    flow = section(kep, "#### Reconcile Flow and Failure Gating", "#### Instance Binding Lifecycle")
    step11 = re.search(r"11\. Inject static pod bindings.*?\n", flow)
    step12 = re.search(r"12\. Create/update Roles", flow)
    ordered = bool(step11 and step12) and step11.start() < step12.start()
    handshake = "Before each Pod creation" in kep and "PlacementBound" in kep
    return ordered and handshake, "injection (step 11) precedes Role writes (step 12)=%s; per-Pod handshake=%s" % (
        ordered, handshake)


@check("LF13", "contract", "[fixed r3] Volcano instance subgroup example sets subGroupSize (ledger F13)")
def lf13():
    kep = KEP["text"]
    volcano_example = section(kep, "```yaml\napiVersion: scheduling.volcano.sh", "```")
    size = "subGroupSize: 4" in volcano_example
    compute = "ComputeSubGroupSize" in kep
    threshold = "instance-local dispatch threshold" in kep or "instance-local threshold" in kep
    return size and compute and threshold, "example subGroupSize=4=%s; ComputeSubGroupSize=%s; threshold semantics=%s" % (
        size, compute, threshold)


# ------------------------------------- round-4 fixed guards (new fixes) -----
# Head 9f682b0 closed the remaining round-3 open findings. Each guard below
# pins the sentence(s) that constitute the fix so a later rewrite cannot
# silently regress them.


@check("LF8", "contract", "[fixed r4] rejected unselected late policies acquire no finalizer; deleting them resumes (ledger F8)")
def lf8():
    kep = KEP["text"]
    no_finalizer = ("A rejected late policy that was never selected for acceptance acquires no "
                    "`topology.x-k8s.io/policy` finalizer or topology consumer reservation" in kep)
    resume = ("Removing that policy restores the previously accepted role-only or `none` declarations" in kep
              and "does not require recreating the existing RBG" in kep)
    new_lifecycle = "Applying the rejected topology does require a new RBG lifecycle" in kep
    test = "Late-policy recovery: an unselected rejected policy acquires no topology finalizer" in kep
    return no_finalizer and resume and new_lifecycle and test, \
        "no finalizer/reservation=%s; delete-resumes=%s; new lifecycle for applying=%s; test item 34=%s" % (
            no_finalizer, resume, new_lifecycle, test)


@check("LF16", "contract", "[fixed r4] TopologyPolicyLate's message identifies which mismatch occurred (ledger F16)")
def lf16():
    kep = KEP["text"]
    documented = ("`TopologyPolicyLate` also covers changed or removed accepted declarations, "
                  "and its message must identify which mismatch occurred" in kep)
    return documented, "reason semantics + message identification documented=%s" % documented


@check("LF26", "contract", "[fixed r4] PR body describes the ClusterNetworkTopology design; stale legs gone (ledger F26)")
def lf26():
    body = KEP["body"]
    kep = KEP["text"]
    mentions = body.count("ClusterNetworkTopology") >= 1
    stale = {
        "TopologyResourceUnresolved": "TopologyResourceUnresolved" in body,
        "selects the KAI Topology CR": "selects the KAI Topology CR" in body,
        "controller default can be overridden": "default can be overridden" in body,
    }
    # the body's replacement bullets agree with the KEP's head statements
    agrees = ("topologyName` references this RBG CR" in body
              and "It is not a KAI/Volcano/Koordinator resource name" in kep
              and "No default topology CR is created" in kep)
    ok = mentions and not any(stale.values()) and agrees
    return ok, "body mentions ClusterNetworkTopology=%s; stale legs present=%s; replacement bullets agree with KEP=%s" % (
        mentions, [k for k, v in stale.items() if v], agrees)


@check("LF27", "contract", "[fixed r4] the published branch's net diff is the two KEP files only (ledger F27)")
def lf27():
    repo = KEP["repo"]
    mb = subprocess.run(["git", "merge-base", "origin/main", "HEAD"],
                        cwd=repo, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    if mb.returncode != 0:
        return False, "merge-base unavailable"
    mb_sha = mb.stdout.decode().strip()
    ls = subprocess.run(["git", "diff", "--name-only", mb_sha, "HEAD"],
                        cwd=repo, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    if ls.returncode != 0:
        return False, "git diff failed"
    # the grafted reviewer harness (docs/verification/**) is not part of the
    # published PR tree; everything else must be the two KEP files
    changed = [f for f in ls.stdout.decode().split()
               if not f.startswith("docs/verification/")]
    allowed = {"keps/473-topology-aware-scheduling/README.md",
               "keps/473-topology-aware-scheduling/kep.yaml"}
    ok = set(changed) <= allowed
    return ok, "net changed files outside the harness: %s" % (changed or "(none)")


@check("LF28", "contract", "[fixed r4] topology create rejects known-but-unsupported schedulers incl. native kube-scheduler (ledger F28)")
def lf28():
    kep = KEP["text"]
    rule = ("reject creation if the active scheduler is unknown or is not one of `KAI`, `Koordinator`, and `Volcano`, "
            "including a recognized native kube-scheduler configuration" in kep)
    no_enum = "do not add an enum value merely to persist a failed topology CR" in kep
    test = ("unknown and known-but-unsupported schedulers (including native kube-scheduler) reject create "
            "without expanding the enum" in kep)
    return rule and no_enum and test, \
        "create-rejection rule=%s; no-enum-persistence=%s; admission test item 1=%s" % (rule, no_enum, test)


@check("LF29", "contract", "[fixed r4] HyperNode readable prefixes sanitize dots before truncation (ledger F29)")
def lf29():
    kep = KEP["text"]
    sanitize = ("Compute `topology-prefix` by replacing every `.` in the DNS-subdomain topology name with `-`, "
                "then taking the first 16 characters" in kep)
    order = "do not truncate before sanitizing" in kep
    hash_inputs = "sanitization/truncation never changes those hash inputs" in kep
    boundary = "must not leave a dot at the truncation boundary" in kep
    test = "dotted names (including `abcdefghijklmno.example.com` with a dot at the 16-character boundary)" in kep
    return sanitize and order and hash_inputs and boundary and test, \
        "sanitize-then-truncate=%s; order stated=%s; hash inputs stable=%s; boundary example=%s; test item 12 dotted=%s" % (
            sanitize, order, hash_inputs, boundary, test)


@check("LF30", "contract", "[fixed r4] legacy recordless RBGs bootstrap to none; pre-arrival policy is late (ledger F30)")
def lf30():
    kep = KEP["text"]
    marker = "topology.x-k8s.io/contract-version: v1:<handover-uuid>" in kep
    legacy_none = "persist a legacy `none` record for that UID" in kep
    late = ("A topology-bearing policy already present before the new controller's first reconcile is late "
            "relative to this legacy contract and reports `TopologyPolicyLate`" in kep)
    blocked = "TopologyBootstrapBlocked" in kep
    tests = ("Upgrade bootstrap: CREATE stamps the configured `v1:<handover-uuid>` marker" in kep
             and "Legacy upgrade: seed old-controller workloads without an accepted record" in kep
             and "Upgrade handover: run an old controller with existing RBGs" in kep)
    return marker and legacy_none and late and blocked and tests, \
        "CREATE marker=%s; legacy none bootstrap=%s; pre-arrival policy late=%s; TopologyBootstrapBlocked=%s; test items 33/24/19=%s" % (
            marker, legacy_none, late, blocked, tests)


# --------------------------------------------------------------- contracts ---

@check("P0", "contract", "premise: base branch has no topology-aware scheduling")
def p0():
    repo = KEP["repo"]

    def non_vendor(lines):
        # vendor/ = upstream API types (a dependency, not RBG code);
        # docs/verification/ = this harness' own snapshot files.
        return [l for l in lines
                if not l.split(":", 1)[-1].startswith("vendor/")
                and not l.split(":", 1)[-1].startswith("docs/verification/")]

    out = subprocess.run(
        ["git", "grep", "-l", "-E", "topologyConstraint|networkTopology|InstanceTopologyConstraint", "--", "*.go"],
        cwd=repo, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    hits = non_vendor(out.stdout.decode().splitlines())
    # the KEP branch is checked out; the base tree is queried via git grep on the base ref
    base = subprocess.run(
        ["git", "grep", "-l", "-E", "topologyConstraint|networkTopology|InstanceTopologyConstraint", "origin/main", "--", "*.go"],
        cwd=repo, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    base_hits = non_vendor(base.stdout.decode().splitlines())
    ok = (not base_hits) and (len(hits) == 0)  # KEP branch adds only docs, no .go
    return ok, "base(origin/main) first-party topology code files=%s; PR head first-party topology .go files=%s (vendor hits are the Volcano API dependency, unrelated)" % (base_hits, hits)


@check("C1", "contract", "codebase symbols the KEP builds on exist (ResolveGangStrategy, labels, conditions)")
def c1():
    repo = KEP["repo"]
    def has(pattern, path):
        r = subprocess.run(["grep", "-rn", pattern, path], cwd=repo,
                           stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        return r.returncode == 0
    ok = (has("func ResolveGangStrategy", "pkg/scheduler/common/gang_strategy.go")
          and has("GroupNameLabelKey", "api/workloads/constants/label.go")
          and has("RoleInstanceNameLabelKey", "api/workloads/constants/label.go")
          and has("IncompatibleGangConfig", "pkg/scheduler/common/gang_error.go")
          and has("GangConfigured", "internal/controller/workloads/rolebasedgroup_controller.go")
          and has("SchedulingCoordinationStrategy", "api/workloads/v1alpha2/coordinatedpolicy_types.go")
          and has("LeaderWorkerPattern", "api/workloads/v1alpha2/rolebasedgroup_types.go"))
    return ok, "ResolveGangStrategy / labels / IncompatibleGangConfig / GangConfigured / strategy+pattern types all present"


@check("C2", "contract", "vendored Volcano API matches the KEP dialect claims")
def c2():
    repo = KEP["repo"]
    t = read(os.path.join(repo, "vendor", "volcano.sh", "apis", "pkg", "apis", "scheduling", "v1beta1", "types.go"))
    sub = re.search(r"type SubGroupPolicySpec struct.*?\n}", t, re.S)
    ok_sub = bool(sub) and "NetworkTopology *NetworkTopologySpec" in sub.group(0) and "SubGroupSize *int32" in sub.group(0)
    nts = re.search(r"type NetworkTopologySpec struct.*?\n}", t, re.S)
    ok_nts = bool(nts) and "Mode NetworkTopologyMode" in nts.group(0) and "HighestTierAllowed *int" in nts.group(0) and "HighestTierName string" in nts.group(0)
    minm = re.search(r"MinMember defines.*?MinMember int32", t, re.S)
    ok_min = bool(minm) and "Minimum=0" in minm.group(0)
    return (ok_sub and ok_nts and ok_min), "subGroupPolicy{networkTopology,subGroupSize}=%s; networkTopology{mode,highestTierAllowed,highestTierName}=%s; PodGroup minMember Minimum=0=%s" % (ok_sub, ok_nts, ok_min)


@check("C3", "contract", "upstream snapshots back the KEP's external API claims")
def c3():
    ev = KEP["evidence"]
    up = os.path.join(ev, "upstream")
    ok = True
    detail = []
    h = read(os.path.join(up, "volcano_hypernode_types.go"))
    ok_h = ("Tier int" in h) and ("TierName string" in h)
    ok &= ok_h
    detail.append("volcano HyperNode tier+tierName=%s" % ok_h)
    k = read(os.path.join(up, "kai_podgroup_types.go"))
    ok_k = ("RequiredTopologyLevel" in k and "PreferredTopologyLevel" in k and "Topology string" in k
            and "Parent *string" in k and "MinSubGroup *int32" in k)
    ok &= ok_k
    detail.append("kai PodGroup topologyConstraint/subGroups=%s" % ok_k)
    t = read(os.path.join(up, "kai_topology_types.go"))
    ok_t = "NodeLabel string" in t and "Levels []TopologyLevel" in t
    ok &= ok_t
    detail.append("kai Topology levels[].nodeLabel=%s" % ok_t)
    n = read(os.path.join(up, "koordinator_network_topology.go"))
    ok_n = ('AnnotationGangNetworkTopologySpec = AnnotationGangPrefix + "/network-topology-spec"' in n
            and "MustGather" in n and "PreferGather" in n and "GatherStrategy" in n)
    ok &= ok_n
    detail.append("koordinator network-topology-spec+gatherStrategy=%s" % ok_n)
    g = read(os.path.join(up, "koordinator_coscheduling.go"))
    ok_g = 'AnnotationGangGroups = AnnotationGangPrefix + "/groups"' in g
    ok &= ok_g
    detail.append("koordinator groups annotation=%s" % ok_g)
    return ok, "; ".join(detail)


@check("C4", "contract", "r3: Volcano v1.14.4 subjob mechanics back the KEP claims")
def c4():
    up = os.path.join(KEP["evidence"], "upstream", "round3")
    ji = read(os.path.join(up, "volcano_job_info.go"))
    sji = read(os.path.join(up, "volcano_sub_job_info.go"))
    # first policy with non-empty match values wins; selector-only policies fall through
    first_match = ("for _, policy := range ji.PodGroup.Spec.SubGroupPolicy" in ji
                   and "if matchValues := getSubJobMatchValues(policy, ti.Pod); len(matchValues) > 0" in ji)
    # default subjob clones the PodGroup's networkTopology (only the default one inherits)
    default_inherits = ("func (ji *JobInfo) getOrCreateDefaultSubJob" in ji
                        and "policy.NetworkTopology = cloneNetworkTopology(ji.NetworkTopology)" in ji)
    # subGroupSize populates SubJobInfo.MinAvailable; omitted size defaults to 1
    size = ("if policy.SubGroupSize != nil" in sji and "sji.MinAvailable = *policy.SubGroupSize" in sji
            and "MinAvailable:    1" in sji)
    return first_match and default_inherits and size, \
        "first-non-empty-match policy selection=%s; default subjob inherits PodGroup topology=%s; subGroupSize->MinAvailable=%s" % (
            first_match, default_inherits, size)


@check("C5", "contract", "r3: Volcano v1.14.4 restart domain recovery skips topology-less subjobs")
def c5():
    s = read(os.path.join(KEP["evidence"], "upstream", "round3", "volcano_session.go"))
    skip = ("if !subJob.WithNetworkTopology() || subJob.AllocatedHyperNode != \"\"" in s
            and "func (ssn *Session) recoverAllocatedHyperNode" in s)
    lca = "hyperNodes.GetLCAHyperNode(lca, subJob.AllocatedHyperNode)" in s
    return skip and lca, "subjobs without own topology skipped=%s; parent domain = LCA of recovered subjobs=%s" % (skip, lca)


@check("C6", "contract", "r3: Volcano v1.14.4 allocate skips BestEffort; backfill direct-allocates from the full node list")
def c6():
    a = read(os.path.join(KEP["evidence"], "upstream", "round3", "volcano_allocate.go"))
    b = read(os.path.join(KEP["evidence"], "upstream", "round3", "volcano_backfill.go"))
    skip_be = "if task.Resreq.IsEmpty()" in a and "BestEffort task, skip" in a
    bf = ("predicateNodes, fitErrors := ph.PredicateNodes(task, ssn.NodeList" in b
          and "ssn.Allocate(task, node)" in b)
    return skip_be and bf, "allocate skips empty-request tasks=%s; backfill allocates directly from ssn.NodeList=%s" % (
        skip_be, bf)


@check("C7", "contract", "r3: KAI v0.18.6 domain-ID encoding is non-injective; level/alias validation as cited")
def c7():
    up = os.path.join(KEP["evidence"], "upstream", "round3")
    ts = read(os.path.join(up, "kai_topology_structs.go"))
    tp = read(os.path.join(up, "kai_topology_plugin.go"))
    tt = read(os.path.join(up, "kai_topology_types.go"))
    pg = read(os.path.join(up, "kai_podgroup_types.go"))
    join = "return DomainID(strings.Join(domainsNames, \".\"))" in ts
    merge = ("domainInfo, foundDomain := domainsForLevel[domainId]" in tp
             and "domainInfo.AddNode(nodeInfo)" in tp)
    max16 = "MaxItems=16" in tt
    immutable = "nodeLabel structure is immutable; only aliases may be edited" in tt
    podgroup = "RequiredTopologyLevel" in pg and "MinSubGroup *int32" in pg
    return join and merge and max16 and immutable and podgroup, \
        "unescaped '.' join=%s; same-ID domain merge=%s; MaxItems=16=%s; nodeLabel immutable (alias edits only)=%s; v2alpha2 PodGroup fields=%s" % (
            join, merge, max16, immutable, podgroup)


@check("C8", "contract", "r3: Koordinator pinned baseline limits topology to no-Bound-member jobs")
def c8():
    up = os.path.join(KEP["evidence"], "upstream", "round3")
    w = read(os.path.join(up, "koord_network_topology_workflow.go"))
    c = read(os.path.join(up, "koord_core.go"))
    fixme = ("there are no Bound member Pods" in w
             and "total number of Job member Pods is equal to the minimum number" in w)
    once = ("GangMatchPolicyOnceSatisfied" in c and "isGangOnceResourceSatisfied" in c)
    return fixme and once, "FIXME no-Bound-members/total==minimum=%s; OnceSatisfied early-return path=%s" % (fixme, once)


@check("C9", "contract", "r3: the PR's only Go delta is byte-identical to main's merged #495 (no net runtime change)")
def c9():
    repo = KEP["repo"]
    f = "pkg/reconciler/roleinstance/utils/instance_utils.go"
    diff = subprocess.run(["git", "diff", "origin/main", "HEAD", "--", f],
                          cwd=repo, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    clean = diff.returncode == 0 and not diff.stdout.strip()
    blob = subprocess.run(["git", "rev-parse", "HEAD:" + f, "origin/main:" + f],
                          cwd=repo, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    same_blob = blob.returncode == 0 and len(blob.stdout.decode().split()) == 2 \
        and blob.stdout.decode().split()[0] == blob.stdout.decode().split()[1]
    return clean and same_blob, "diff vs origin/main empty=%s; blob identical=%s" % (clean, same_blob)


def main():
    ap = argparse.ArgumentParser()
    here = os.path.dirname(os.path.abspath(__file__))
    ap.add_argument("--repo", required=True)
    ap.add_argument("--evidence", default=os.path.join(here, "..", "results"))
    ap.add_argument("--kep", default=os.path.join("keps", "473-topology-aware-scheduling", "README.md"))
    ap.add_argument("--body", default=None,
                    help="override the PR-body fixture path (bites-check against an archived body)")
    args = ap.parse_args()

    KEP["repo"] = os.path.abspath(args.repo)
    KEP["evidence"] = os.path.abspath(args.evidence)
    KEP["text"] = read(os.path.join(KEP["repo"], args.kep))
    body_path = args.body or os.path.join(KEP["evidence"], "pr-body.md")
    KEP["body"] = read(body_path) if os.path.exists(body_path) else ""

    failures = []
    print("id   polarity  verdict         finding")
    print("---- -------- --------------- -------------------------------------------")
    for cid, polarity, label, fn in CHECKS:
        try:
            ok, detail = fn()
        except Exception as exc:  # noqa: BLE001 - report, don't crash the sweep
            ok, detail = False, "harness error: %r" % exc
        if polarity == "canary":
            verdict = "DEFECT-PRESENT" if ok else "DEFECT-ABSENT"
            good = ok
        else:
            verdict = "PASS" if ok else "FAIL"
            good = ok
        if not good:
            failures.append(cid)
        print("%-4s %-8s %-15s %s" % (cid, polarity, verdict, label))
        print("     detail: %s" % detail)
    print("----")
    if failures:
        print("RESULT: %d check(s) not in the expected state: %s" % (len(failures), ", ".join(failures)))
        return 1
    print("RESULT: all live canaries DEFECT-PRESENT and all contracts PASS")
    return 0


if __name__ == "__main__":
    sys.exit(main())
