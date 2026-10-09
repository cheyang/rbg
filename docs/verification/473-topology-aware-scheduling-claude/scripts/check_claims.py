#!/usr/bin/env python3 -I
"""L1 claim-check harness for the review of PR #473 (KEP-473: Topology-Aware Scheduling).

The PR under review is documentation-only: it adds keps/473-topology-aware-scheduling/
{README.md,kep.yaml} and no runtime code. The reviewable defects are therefore (a)
defects inside the KEP document itself (contradictions, unexplained renderings,
stale claims) and (b) factual claims about the codebase / upstream schedulers that
are checkable deterministically.

Polarity: every F-numbered check below is a BUG-CANARY on the KEP text.
  DEFECT-PRESENT  -> the finding still holds (harness green, review finding live)
  DEFECT-ABSENT   -> the KEP was amended and the finding is fixed; a canary that
                     flips to absent must then be inverted or promoted to a
                     contract check (see README.md).
The P0 premise check and the C-numbered checks are CONTRACT checks: they assert
facts that must STAY true (they describe the world, not a defect).

Usage:
  python3 -I check_claims.py --repo <repo-root> --evidence <evidence-dir> [--kep <path>]
    --repo      checkout of the repo whose working tree holds the KEP (PR head)
    --evidence  the verification dir's results/ directory (holds pr-body.md and
                upstream/ snapshots; falls back to sibling-of-script layout)
Exit code 0 iff every canary reports DEFECT-PRESENT and every contract check passes.
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


@check("F1", "canary", "downgrade section does not address the topology finalizer")
def f1():
    kep = KEP["text"]
    finalizer_claimed = "finalizer" in kep and "delete-and-recreate" in kep.lower().replace("delete and recreate", "delete-and-recreate")
    downgrade = section(kep, "## Upgrade / Downgrade Strategy", "## Version Skew")
    mentions_finalizer_in_downgrade = "finalizer" in downgrade
    present = finalizer_claimed and not mentions_finalizer_in_downgrade
    detail = "finalizer design present=%s, downgrade section mentions finalizer=%s" % (
        finalizer_claimed, mentions_finalizer_in_downgrade)
    return present, detail


@check("F2", "canary", "RBGSet admission guard described two contradictory ways")
def f2():
    kep = KEP["text"]
    cross = "lists children from the informer cache" in kep
    selfcontained = "requires no cross-resource child lookup" in kep
    present = cross and selfcontained
    return present, "cross-resource listing=%s; self-contained/no-lookup=%s (both asserted)" % (cross, selfcontained)


@check("F3", "canary", "PR body promises preferred<=required validation the KEP declines")
def f3():
    body = KEP["body"]
    kep = KEP["text"]
    body_promises = "preferred level not broader than required" in body
    kep_declines = "Required/preferred relative order" in kep and "Not validated by RBG" in kep
    present = body_promises and kep_declines
    return present, "body promises=%s; KEP table row 'Not validated by RBG'=%s" % (body_promises, kep_declines)


@check("F4", "canary", "PR body claims an in-tree Chinese translation that is absent")
def f4():
    body = KEP["body"]
    claims = "Chinese translation" in body
    kep_dir = os.path.join(KEP["repo"], "keps", "473-topology-aware-scheduling")
    files = sorted(os.listdir(kep_dir)) if os.path.isdir(kep_dir) else []
    has_zh = any(("zh" in f.lower() or "cn" in f.lower()) for f in files)
    present = claims and not has_zh
    return present, "body claims translation=%s; kep dir contents=%s" % (claims, files)


@check("F5", "canary", "Generated Group Names section defines only the Volcano naming rule")
def f5():
    sec = section(KEP["text"], "#### Generated Group Names", "#### Translation Matrix")
    only_volcano = "Volcano" in sec and "Koordinator" not in sec and "KAI" not in sec
    present = bool(sec.strip()) and only_volcano
    return present, "section covers Volcano only (Koordinator/KAI naming undefined): %s" % only_volcano


@check("F6", "canary", "KAI/Koordinator examples partition unconstrained roles per instance")
def f6():
    kep = KEP["text"]
    kai_example = section(kep, "```yaml\napiVersion: scheduling.run.ai", "```")
    kai_partition = bool(re.search(r"name: decode-0", kep)) and bool(re.search(r"name: decode-1", kep))
    koord_partition = "infer-0-decode-0" in kep and "infer-0-prefill-0" in kep
    rule = "uncovered roles only appear as sibling subGroups when they declare their own instance-level constraints" in kep
    present = (kai_partition or koord_partition) and rule
    return present, "KAI decode-0/1 subGroups=%s, Koordinator per-instance members=%s, stated rule present=%s" % (
        kai_partition, koord_partition, rule)


@check("F7", "canary", "KEP cites hasSubGroupPolicy; codebase function is supportsSubGroupPolicy")
def f7():
    kep = KEP["text"]
    repo = KEP["repo"]
    grep = subprocess.run(
        ["grep", "-rn", "hasSubGroupPolicy", "--include=*.go", "."],
        cwd=repo, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    code_has = grep.returncode == 0 and b"hasSubGroupPolicy" in grep.stdout
    supp = subprocess.run(
        ["grep", "-rn", "supportsSubGroupPolicy", "--include=*.go", "pkg/scheduler/volcano/scheduler.go"],
        cwd=repo, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    real_fn = supp.returncode == 0 and b"func (m *GangScheduler) supportsSubGroupPolicy" in supp.stdout
    present = "hasSubGroupPolicy" in kep and not code_has and real_fn
    return present, "KEP cites hasSubGroupPolicy=%s; code has it=%s; real symbol supportsSubGroupPolicy=%s" % (
        "hasSubGroupPolicy" in kep, code_has, real_fn)


@check("F8", "canary", "reconcile-level validation needs new cluster-scoped reads; RBAC unaddressed")
def f8():
    kep = KEP["text"]
    needs_objects = ("HyperNode" in kep and "ClusterNetworkTopology" in kep and "reconcile" in kep.lower())
    mentions_rbac = ("RBAC" in kep or "ClusterRole" in kep or "role-based access" in kep.lower())
    present = needs_objects and not mentions_rbac
    return present, "topology objects validated at reconcile=%s; RBAC mentioned=%s" % (needs_objects, mentions_rbac)


@check("F9", "canary", "minMember: 0 on scheduling.sigs.k8s.io PodGroup diverges from upstream CRD")
def f9():
    kep = KEP["text"]
    repo = KEP["repo"]
    koord_example = section(kep, "```yaml\napiVersion: scheduling.sigs.k8s.io", "```")
    renders_zero = "minMember: 0" in koord_example
    types_go = os.path.join(repo, "vendor", "sigs.k8s.io", "scheduler-plugins", "apis", "scheduling", "v1alpha1", "types.go")
    upstream_min1 = False
    if os.path.exists(types_go):
        src = read(types_go)
        block = re.search(r"MinMember defines.*?MinMember int32", src, re.S)
        upstream_min1 = bool(block) and "Minimum=1" in block.group(0)
    koord_crd = os.path.join(KEP["evidence"], "upstream", "koordinator_podgroups_crd.yaml")
    koord_no_min = False
    if os.path.exists(koord_crd):
        crd = read(koord_crd)
        koord_no_min = "minimum:" not in crd
    present = renders_zero and upstream_min1 and koord_no_min
    return present, "KEP renders minMember: 0 on sigs PodGroup=%s; upstream scheduler-plugins CRD requires >=1=%s; koordinator's deployed CRD has no minimum=%s" % (
        renders_zero, upstream_min1, koord_no_min)


@check("F10", "canary", "typo 'upto' in generated API doc comment")
def f10():
    return "upto" in KEP["text"], "doc comment contains 'upto'"


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
    return ok, "base(origin/main) first-party topology code files=%s; PR head first-party topology .go files=%s (docs-only PR; vendor hits are the Volcano API dependency, unrelated)" % (base_hits, hits)


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
    # structural checks (explicit regex, anchored on struct bodies):
    sub = re.search(r"type SubGroupPolicySpec struct.*?\n}", t, re.S)
    ok_sub = bool(sub) and "NetworkTopology *NetworkTopologySpec" in sub.group(0)
    nts = re.search(r"type NetworkTopologySpec struct.*?\n}", t, re.S)
    ok_nts = bool(nts) and "Mode NetworkTopologyMode" in nts.group(0) and "HighestTierAllowed *int" in nts.group(0) and "HighestTierName string" in nts.group(0)
    minm = re.search(r"MinMember defines.*?MinMember int32", t, re.S)
    ok_min = bool(minm) and "Minimum=0" in minm.group(0)
    return (ok_sub and ok_nts and ok_min), "subGroupPolicy.networkTopology=%s; networkTopology{mode,highestTierAllowed,highestTierName}=%s; PodGroup minMember Minimum=0=%s" % (ok_sub, ok_nts, ok_min)


@check("C3", "contract", "upstream snapshots back the KEP's external API claims")
def c3():
    ev = KEP["evidence"]
    up = os.path.join(ev, "upstream")
    ok = True
    detail = []
    # Volcano HyperNode: spec.tier (int) + spec.tierName (string)
    h = read(os.path.join(up, "volcano_hypernode_types.go"))
    ok_h = ("Tier int" in h) and ("TierName string" in h)
    ok &= ok_h
    detail.append("volcano HyperNode tier+tierName=%s" % ok_h)
    # KAI PodGroup v2alpha2: topologyConstraint{required,preferred,topology}, subGroups w/ parent, minSubGroup
    k = read(os.path.join(up, "kai_podgroup_types.go"))
    ok_k = ("RequiredTopologyLevel" in k and "PreferredTopologyLevel" in k and "Topology string" in k
            and "Parent *string" in k and "MinSubGroup *int32" in k)
    ok &= ok_k
    detail.append("kai PodGroup topologyConstraint/subGroups=%s" % ok_k)
    # KAI Topology CRD: spec.levels[].nodeLabel
    t = read(os.path.join(up, "kai_topology_types.go"))
    ok_t = "NodeLabel string" in t and "Levels []TopologyLevel" in t
    ok &= ok_t
    detail.append("kai Topology levels[].nodeLabel=%s" % ok_t)
    # Koordinator annotations
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


def main():
    ap = argparse.ArgumentParser()
    here = os.path.dirname(os.path.abspath(__file__))
    ap.add_argument("--repo", required=True)
    ap.add_argument("--evidence", default=os.path.join(here, "..", "results"))
    ap.add_argument("--kep", default=os.path.join("keps", "473-topology-aware-scheduling", "README.md"))
    args = ap.parse_args()

    KEP["repo"] = os.path.abspath(args.repo)
    KEP["evidence"] = os.path.abspath(args.evidence)
    KEP["text"] = read(os.path.join(KEP["repo"], args.kep))
    body_path = os.path.join(KEP["evidence"], "pr-body.md")
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
    print("RESULT: all canaries DEFECT-PRESENT and all contracts PASS")
    return 0


if __name__ == "__main__":
    sys.exit(main())
