#!/usr/bin/env python3
"""Verification harness for PR 577 (KEP-133: mark replaced by KEP-576).

Docs-only PR. The "product" is the text, so the harness verifies:
  T1 (contract)   diff-shape claim: apart from the tombstone block, the status
                  flip, and EOF blank-line normalization, keps/133-configmap-refine
                  content is byte-identical to the merge-base.
  T2 (contract)   kep.yaml remains structurally valid and `status: replaced` is a
                  value from the repo's own KEP-template enum.
  T3 (canary)     F1 dead-link: the tombstone's root-relative link target
                  /keps/576-compact-discovery-configmap is ABSENT from the PR head
                  tree (KEP-576 lives only in the open PR #578). Canary asserts the
                  current (wrong) state; it flips to fail once the successor KEP is
                  actually in the tree, and must then be inverted.
  T4 (contract)   factual claims in the tombstone: #134 (3ddf8af6) is contained in
                  v0.7.0 and v0.8.0; the shipped refine mode uses per-instance full
                  address enumeration (no `excludes` compact field); the RBG-level
                  shared ConfigMap is created in the controller.

Usage: check_kep133_replaced.py <repo-path> <base-sha> <head-sha>
Exit 0 iff every check is in its expected state.
"""
import re
import subprocess
import sys

RESULTS = []

PR134_MERGE = "3ddf8af6"  # "feat(kep): add new kep to refine configmap content and configuration (#134)"


def run_git(repo, *args):
    out = subprocess.run(["git", "-C", repo] + list(args),
                         stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    return out.returncode, out.stdout.decode("utf-8", "replace")


def record(name, ok, detail):
    RESULTS.append((name, ok, detail))
    print("[%s] %s: %s" % ("PASS" if ok else "FAIL", name, detail))


def show_file(repo, sha, path):
    rc, out = run_git(repo, "show", "%s:%s" % (sha, path))
    return out if rc == 0 else None


def main():
    if len(sys.argv) != 4:
        print(__doc__)
        return 2
    repo, base, head = sys.argv[1], sys.argv[2], sys.argv[3]

    readme = "keps/133-configmap-refine/README.md"
    kepyaml = "keps/133-configmap-refine/kep.yaml"

    # ---------- T1: diff-shape claim (contract) ----------
    base_readme = show_file(repo, base, readme)
    head_readme = show_file(repo, head, readme)
    base_yaml = show_file(repo, base, kepyaml)
    head_yaml = show_file(repo, head, kepyaml)

    if None in (base_readme, head_readme, base_yaml, head_yaml):
        record("T1-diff-shape", False, "could not read keps/133 files at base/head")
        return finish()

    # Strip the tombstone block (blockquote lines between H1 and <!-- toc -->).
    m = re.search(r"^# .*\n\n((?:>.*\n)+)\n?<!-- toc -->", head_readme, re.M)
    if not m:
        record("T1-diff-shape", False, "tombstone blockquote not found before <!-- toc -->")
    else:
        # Reconstruct: title + (skip tombstone and its separator blank line) + toc marker + rest.
        # The whole match m ends right after the literal '<!-- toc -->' marker.
        stripped = head_readme[:m.start(1)] + "<!-- toc -->" + head_readme[m.end(0):]
        # Normalize EOF trailing blank lines on both sides before compare.
        norm_base = base_readme.rstrip("\n")
        norm_head = stripped.rstrip("\n")
        ok = norm_base == norm_head
        detail = ("README minus tombstone is byte-identical to base "
                  "(after EOF newline normalization)" if ok else
                  "README differs from base beyond the tombstone block")
        record("T1-diff-shape", ok, detail)

    # kep.yaml: only the status line may change (plus EOF blank normalization).
    yaml_ok, yaml_detail = True, "kep.yaml differs from base only in the status line"
    ly, hy = base_yaml.rstrip("\n").split("\n"), head_yaml.rstrip("\n").split("\n")
    if len(ly) != len(hy):
        yaml_ok, yaml_detail = False, "kep.yaml line count changed (%d -> %d)" % (len(ly), len(hy))
    else:
        for i in range(len(ly)):
            if ly[i] != hy[i]:
                if re.match(r"^status: provisional$", ly[i]) and re.match(r"^status: replaced$", hy[i]):
                    continue
                yaml_ok, yaml_detail = False, "kep.yaml line %d changed beyond status: %r -> %r" % (i + 1, ly[i], hy[i])
                break
    record("T1-yaml-shape", yaml_ok, yaml_detail)

    # ---------- T2: status value validity (contract) ----------
    status = None
    for line in head_yaml.split("\n"):
        m2 = re.match(r"^status: (.+)$", line)
        if m2:
            status = m2.group(1).strip()
            break
    tmpl = show_file(repo, head, "keps/NNNN-kep-template/kep.yaml") or ""
    enum = []
    for line in tmpl.split("\n"):
        m3 = re.match(r"^status: ([a-z|]+)$", line)
        if m3 and "|" in m3.group(1):
            enum = m3.group(1).split("|")
            break
    ok = status == "replaced" and "replaced" in enum
    record("T2-status-enum", ok, "status=%r, template enum=%s" % (status, enum))

    # ---------- T3: F1 dead-link canary ----------
    # Canary: asserts the CURRENT (wrong) state - successor KEP absent from the
    # PR head tree. Flips to FAIL when KEP-576 actually lands in the tree;
    # then invert it (promote to a resolvable-link contract test).
    rc, _ = run_git(repo, "cat-file", "-e", "%s:keps/576-compact-discovery-configmap/kep.yaml" % head)
    absent = rc != 0
    record("T3-successor-link-canary", absent,
           "link target /keps/576-compact-discovery-configmap %s PR head tree (canary: absent = dead link documented; "
           "KEP-576 ships only via open PR #578)" % ("ABSENT from" if absent else "PRESENT in"))

    # ---------- T4: tombstone factual claims (contract) ----------
    rc, _ = run_git(repo, "merge-base", "--is-ancestor", PR134_MERGE, "v0.7.0")
    rc2, _ = run_git(repo, "merge-base", "--is-ancestor", PR134_MERGE, "v0.8.0")
    record("T4-pr134-in-v070-v080", rc == 0 and rc2 == 0,
           "PR #134 merge %s is ancestor of v0.7.0=%s v0.8.0=%s" % (PR134_MERGE, rc == 0, rc2 == 0))

    builder = show_file(repo, head, "pkg/discovery/config_builder.go") or ""
    has_enum = ("Instances []Instance" in builder and "Address string" in builder)
    has_excludes = re.search(r"excludes", builder, re.I) is not None
    record("T4-full-enumeration-shipped", has_enum and not has_excludes,
           "ConfigBuilder uses per-instance full addresses=%s, compact excludes field=%s "
           "(expected: enumeration yes, excludes no)" % (has_enum, has_excludes))

    ctrl = show_file(repo, head, "internal/controller/workloads/rolebasedgroup_controller.go") or ""
    record("T4-rbg-level-configmap", "coreapplyv1.ConfigMap(rbg.Name, rbg.Namespace)" in ctrl,
           "controller creates one RBG-level ConfigMap named after the RBG")

    # best-effort genealogy check (non-gating): successor declares `replaces` back-pointer
    succ_yaml = show_file(repo, head, "keps/576-compact-discovery-configmap/kep.yaml")
    if succ_yaml is None:
        rc3, out3 = run_git(repo, "show", "origin/pr/578:keps/576-compact-discovery-configmap/kep.yaml")
        succ_yaml = out3 if rc3 == 0 else None
    if succ_yaml:
        record("T4-genealogy-backpointer (non-gating)", "/keps/133-configmap-refine" in succ_yaml,
               "KEP-576 kep.yaml declares replaces: /keps/133-configmap-refine")
    else:
        print("[SKIP] T4-genealogy-backpointer: KEP-576 not reachable (neither head tree nor origin/pr/578)")

    return finish()


def finish():
    failed = [r for r in RESULTS if not r[1]]
    print("\nSummary: %d/%d checks in expected state" % (len(RESULTS) - len(failed), len(RESULTS)))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
