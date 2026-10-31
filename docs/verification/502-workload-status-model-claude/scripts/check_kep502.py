#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""check_kep502.py — deterministic verification harness for KEP-502 (PR #528).

PR under review: https://github.com/sgl-project/rbg/pull/528 (docs-only: adds
keps/502-workload-status-model/{README.md,kep.yaml}).

The deliverable under review is a *design document*, so the harness checks the
document's own contract: every factual claim the KEP makes about the current API
must hold, and its machine-readable reason taxonomy must be internally
consistent. Checks C1-C4 are CONTRACT tests for findings F1-F4 (fail == the
defect is present in the KEP). C5 verifies the KEP's motivation claims against
the base code (premise). C6 verifies the YAML examples parse.

Output: one line per check, "<check-id>\t<pass|fail>", on stdout (consumed by
re-verify.sh). Human-readable detail goes to stderr.

Usage:
    python3 -I check_kep502.py [--repo <repo-root>] [--kep <kep-readme-path>]
"""

from __future__ import print_function

import argparse
import os
import re
import sys

try:
    import yaml
except ImportError:  # pragma: no cover
    yaml = None


RESULTS = []


def emit(cid, ok, detail):
    """Record a check result. ok is True/False."""
    RESULTS.append((cid, bool(ok), detail))


def read(path):
    with open(path, "r") as fh:
        return fh.read()


def json_tags(text):
    """All JSON field names declared in a Go types file."""
    return set(re.findall(r'json:"([a-zA-Z0-9]+)', text))


def camel_tokens(text):
    """Backticked CamelCase tokens (reasons / phases / condition types)."""
    return re.findall(r"`([A-Z][A-Za-z0-9]+)`", text)


def yaml_reason_tokens(text):
    """reason: values inside YAML example blocks."""
    blocks = re.findall(r"```yaml\n(.*?)```", text, re.S)
    reasons = []
    for b in blocks:
        reasons += re.findall(r"reason:\s*([A-Za-z0-9]+)", b)
    return reasons


# ---------------------------------------------------------------- C1 (F1)


def _field_list(raw):
    fields = []
    for part in re.split(r",|\band\b", raw):
        f = part.strip()
        if f and re.match(r"^[a-z][A-Za-z0-9]*$", f):
            fields.append(f)
    return fields


def check_c1(kep_text, repo):
    """Field-inventory contract: every status field the KEP claims to retain
    or preserve exists in the current API.

    The KEP's own convention (Design Details preamble) says 'Retained' is
    relative to the current API, so each named field must be a real field of
    RoleInstanceSet (v1alpha2 / v1alpha1) or RoleInstance (v1alpha2).
    """
    problems = []

    m = re.search(r"RIS retains (.*?)\.", kep_text, re.S)
    if not m:
        problems.append("'RIS retains ...' sentence not found in KEP")
    else:
        fields = _field_list(m.group(1))
        api_fields = set()
        for rel in ("api/workloads/v1alpha2/roleinstanceset_types.go",
                    "api/workloads/v1alpha1/instanceset_types.go"):
            p = os.path.join(repo, rel)
            if os.path.exists(p):
                api_fields |= json_tags(read(p))
        missing = [f for f in fields if f not in api_fields]
        if missing:
            problems.append("RIS retained fields missing from current API: %s"
                            % ", ".join(missing))

    m2 = re.search(r"Preserve (restartCount.*?)\.", kep_text, re.S)
    if not m2:
        problems.append("'Preserve restartCount ...' sentence not found in KEP")
    else:
        fields = _field_list(m2.group(1))
        p = os.path.join(repo, "api/workloads/v1alpha2/roleinstance_types.go")
        api_fields = json_tags(read(p)) if os.path.exists(p) else set()
        missing = [f for f in fields if f not in api_fields]
        if missing:
            problems.append("RI preserved fields missing from current API: %s"
                            % ", ".join(missing))

    emit("C1-retained-fields-exist", not problems,
         "; ".join(problems) if problems
         else "RIS 'retains' and RI 'Preserve' inventories all exist in the "
              "current API")


# ---------------------------------------------------------------- C2 (F2)


def check_c2(kep_text):
    """F2: every reason in the RBG Progressing=True candidate row is defined
    or used elsewhere in the document (>= 2 distinct lines of occurrence)."""
    rows = re.findall(r"^\|\s*Progressing=True\s*\|(.*)\|\s*$", kep_text, re.M)
    if not rows:
        emit("C2-progressing-reasons-defined", False,
             "Progressing=True candidate-reason row not found")
        return
    reasons = re.findall(r"`([A-Za-z]+)`", rows[0])
    undefined = []
    for r in reasons:
        lines = [ln for ln in kep_text.splitlines() if r in ln]
        if len(lines) < 2:
            undefined.append("%s (%d line(s))" % (r, len(lines)))
    emit("C2-progressing-reasons-defined", not undefined,
         "candidate reasons: %s; not defined/used elsewhere: %s"
         % (", ".join(reasons), ", ".join(undefined) if undefined else "(none)"))


# ---------------------------------------------------------------- C3 (F3)


def check_c3(kep_text):
    """F3: no two distinct documented reasons differ only by one inserted 's'
    without an acknowledging line that mentions both spellings."""
    tokens = set(camel_tokens(kep_text)) | set(yaml_reason_tokens(kep_text))
    lines = kep_text.splitlines()
    collisions = []
    for a in sorted(tokens):
        for i in range(len(a) + 1):
            b = a[:i] + "s" + a[i:]
            if b in tokens and b != a:
                ack = any(a in ln and b in ln for ln in lines)
                if not ack:
                    collisions.append("%s vs %s" % (a, b))
    emit("C3-reason-collision-ack", not collisions,
         "unacknowledged near-colliding reason pairs: %s"
         % ("; ".join(collisions) if collisions else "(none)"))


# ---------------------------------------------------------------- C4 (F4)


def check_c4(kep_text, repo):
    """F4: the KEP's candidate reasons for aggregated RI readiness must match,
    case-sensitively, the reason strings the current RI controller writes for
    the RoleInstanceReady condition."""
    m = re.search(r"aggregated RI readiness are (.*?)\.", kep_text)
    if not m:
        emit("C4-ri-readiness-reasons-match-code", False,
             "'Candidate reasons for aggregated RI readiness' sentence not found")
        return
    kep_reasons = set()
    for part in re.split(r",|\band\b", m.group(1)):
        p = part.strip()
        if p:
            kep_reasons.add(p)
    p = os.path.join(repo, "pkg/reconciler/roleinstance/instance_status.go")
    code_reasons = set()
    if os.path.exists(p):
        code_reasons = set(
            re.findall(r'instanceReady\.Reason = "([^"]+)"', read(p)))
    diff = kep_reasons.symmetric_difference(code_reasons)
    emit("C4-ri-readiness-reasons-match-code", not diff,
         "KEP: {%s} vs code RoleInstanceReady reasons: {%s}; diff: %s"
         % (", ".join(sorted(kep_reasons)), ", ".join(sorted(code_reasons)),
            ", ".join(sorted(diff)) if diff else "(none)"))


# ---------------------------------------------------------------- C5 (P0)


def walk_go_files(root):
    for dirpath, _dirnames, filenames in os.walk(root):
        for fn in filenames:
            if fn.endswith(".go") and not fn.endswith("_test.go"):
                yield os.path.join(dirpath, fn)


def check_c5(repo):
    """Premise/motivation: the KEP's factual claims about the current API hold.
    - RBG / RIS / RI have no status.phase today;
    - controllers never write RBG Progressing / RollingUpdateInProgress;
    - RIS and RI do declare FailedScale / FailedUpdate conditions.
    """
    problems = []
    for rel in ("api/workloads/v1alpha2/rolebasedgroup_types.go",
                "api/workloads/v1alpha2/roleinstanceset_types.go",
                "api/workloads/v1alpha2/roleinstance_types.go"):
        p = os.path.join(repo, rel)
        if os.path.exists(p) and re.search(r'json:"phase', read(p)):
            problems.append("%s already declares status.phase" % rel)
    writers = []
    for base in ("internal", "pkg"):
        root = os.path.join(repo, base)
        if not os.path.isdir(root):
            continue
        for path in walk_go_files(root):
            text = read(path)
            if re.search(r"RoleBasedGroupProgressing\b", text):
                writers.append(path)
            if re.search(r"RoleBasedGroupRollingUpdateInProgress\b", text):
                writers.append(path)
            if re.search(r'"Progressing"|"RollingUpdateInProgress"', text):
                writers.append(path)
    if writers:
        problems.append("controllers write RBG Progressing/"
                        "RollingUpdateInProgress: %s" % ", ".join(writers[:5]))
    ris = os.path.join(repo, "api/workloads/v1alpha2/roleinstanceset_types.go")
    ri = os.path.join(repo, "api/workloads/v1alpha2/roleinstance_types.go")
    for p, name in ((ris, "RIS"), (ri, "RI")):
        if os.path.exists(p):
            t = read(p)
            if "FailedScale" not in t or "FailedUpdate" not in t:
                problems.append("%s does not declare FailedScale/FailedUpdate" % name)
    emit("C5-premise-motivation-claims", not problems,
         "KEP motivation claims vs base code: %s"
         % ("; ".join(problems) if problems else "all verified accurate"))


# ---------------------------------------------------------------- C6


def check_c6(kep_path, yaml_dir):
    if yaml is None:
        emit("C6-yaml-examples-parse", False, "PyYAML not available")
        return
    text = read(kep_path)
    blocks = re.findall(r"```yaml\n(.*?)```", text, re.S)
    bad = []
    for i, b in enumerate(blocks):
        try:
            yaml.safe_load(b)
        except Exception as exc:  # noqa: BLE001
            bad.append("block %d: %s" % (i, exc))
    kep_yaml = os.path.join(yaml_dir, "kep.yaml")
    try:
        data = yaml.safe_load(read(kep_yaml))
        num_ok = data.get("kep-number") == 502
    except Exception as exc:  # noqa: BLE001
        bad.append("kep.yaml: %s" % exc)
        num_ok = False
    if not num_ok:
        bad.append("kep.yaml kep-number != 502")
    emit("C6-yaml-examples-parse", not bad,
         "%d/%d YAML example blocks parse; kep.yaml: %s"
         % (len(blocks) - len([b for b in bad if b.startswith("block")]),
            len(blocks), "OK" if num_ok else "FAIL"))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--repo", default=".")
    ap.add_argument("--kep",
                    default="keps/502-workload-status-model/README.md")
    args = ap.parse_args()
    repo = os.path.abspath(args.repo)
    kep_path = os.path.abspath(args.kep)
    if not os.path.exists(kep_path):
        sys.stderr.write("kep not found: %s\n" % kep_path)
        print("C0-harness\tnot-run")
        return 1
    kep_text = read(kep_path)
    yaml_dir = os.path.dirname(kep_path)

    check_c1(kep_text, repo)
    check_c2(kep_text)
    check_c3(kep_text)
    check_c4(kep_text, repo)
    check_c5(repo)
    check_c6(kep_path, yaml_dir)

    for cid, ok, detail in RESULTS:
        print("%s\t%s" % (cid, "pass" if ok else "fail"))
        sys.stderr.write("  [%s] %s: %s\n" % (cid, "PASS" if ok else "FAIL",
                                              detail))
    return 0


if __name__ == "__main__":
    sys.exit(main())
