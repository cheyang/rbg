#!/usr/bin/env python3 -I
"""Static consistency checker: PR #390 docs vs the code they document.

Each check prints a line `CHECK <id>: PASS|FAIL ...`. Exit code is the number
of FAILs. Runs from the repo root of a checkout that holds both the docs
(working tree) and the code refs being compared.

Findings map:
  F1  - stage inventory in docs vs test/stress/templates/kwok-stage.yaml
        (at PR head AND at the merge target ref)
  F3  - guide's "expected controller startup args" vs helm-rendered args
  FX1 - every repo path referenced by the docs exists
  FX2 - every stress-client flag used by the docs is defined in test/stress/main.go
  FX3 - every helm --set key used by the docs resolves in values.yaml
"""
import json
import os
import re
import subprocess
import sys

REPO = os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))))
DOCS = [
    "doc/best-practice/en/09-stress-testing-and-tuning.md",
    "doc/best-practice/en/09-stress-testing-and-tuning-guide.md",
    "doc/best-practice/zh/09-stress-testing-and-tuning.md",
    "doc/best-practice/zh/09-stress-testing-and-tuning-guide.md",
]

failures = 0


def check(fid, ok, detail):
    global failures
    status = "PASS" if ok else "FAIL"
    if not ok:
        failures += 1
    print(f"CHECK {fid}: {status} - {detail}")


def read(path):
    with open(os.path.join(REPO, path), encoding="utf-8") as f:
        return f.read()


def git(*args):
    return subprocess.run(["git", "-C", REPO, *args], capture_output=True, text=True).stdout


doc_texts = {p: read(p) for p in DOCS if os.path.exists(os.path.join(REPO, p))}
if len(doc_texts) != len(DOCS):
    print(f"FATAL: expected docs not found in {REPO}")
    sys.exit(99)

# ---------------------------------------------------------------- F1: stages
def stage_names(ref):
    """Pod Stage names defined in kwok-stage.yaml at a git ref (or 'WORKTREE')."""
    if ref == "WORKTREE":
        content = read("test/stress/templates/kwok-stage.yaml")
    else:
        content = git("show", f"{ref}:test/stress/templates/kwok-stage.yaml")
    return set(re.findall(r"^  name: (pod-\S+)\s*$", content, re.M)), content.count("kind: Stage")


mentioned = set()
for text in doc_texts.values():
    mentioned |= set(re.findall(r"`(pod-[a-z-]+)`", text))

for ref, label in (("WORKTREE", "PR head"), ("origin/main", "merge target origin/main")):
    stages, total = stage_names(ref)
    undocumented = stages - mentioned
    check(f"F1/{label}", not undocumented,
          f"kwok-stage.yaml at {label} defines {sorted(stages)}; docs mention {sorted(mentioned & stages)}; "
          f"undocumented: {sorted(undocumented) or 'none'} ({total} Stage docs in file)")

# ------------------------------------------------- F3: rendered helm args
guide = doc_texts["doc/best-practice/en/09-stress-testing-and-tuning-guide.md"]
m = re.search(r"containers\[0\]\.args.*?\n(.*?)```", guide, re.S)
expected_args = set()
if m:
    expected_args = set(re.findall(r'"(--[a-z-]+(?:=[^"]*)?)"', m.group(1)))
expected_arg_names = {a.split("=")[0] for a in expected_args}

set_flags = [
    "controller.image.tag=v0-test", "controller.resources.limits.cpu=8",
    "controller.resources.limits.memory=16Gi", "controller.tuning.maxConcurrentReconciles=20",
    "controller.tuning.kubeApiQPS=100", "controller.tuning.kubeApiBurst=200",
    "controller.pprof.enabled=true", "controller.pprof.containerPort=6060",
]
rendered = subprocess.run(
    ["helm", "template", "rbgs", "deploy/helm/rbgs", "-n", "rbgs-system", "--no-hooks"] +
    [x for f in set_flags for x in ("--set", f)],
    capture_output=True, text=True, cwd=REPO)
if rendered.returncode != 0:
    check("F3/helm-render", False, f"helm template failed: {rendered.stderr.strip()[:200]}")
else:
    args_block = re.search(r"args:\n((?:\s+- --.*\n)+)", rendered.stdout)
    rendered_args = [ln.strip().lstrip("- ").strip() for ln in args_block.group(1).strip().splitlines()] if args_block else []
    rendered_args = [("--" + a) for a in rendered_args]
    rendered_names = {a.split("=")[0] for a in rendered_args}
    missing = rendered_names - expected_arg_names
    extra = expected_arg_names - rendered_names
    check("F3/args", not missing and not extra,
          f"rendered={sorted(rendered_args)}; doc-expected-missing={sorted(missing) or 'none'}; "
          f"doc-expected-extra={sorted(extra) or 'none'}")

    # resources claim: {"limits":{"cpu":"8","memory":"16Gi"},"requests":{"cpu":"100m","memory":"256Mi"}}
    res_ok = ('cpu: "8"' in rendered.stdout or "cpu: 8" in rendered.stdout) and "16Gi" in rendered.stdout \
        and "100m" in rendered.stdout and "256Mi" in rendered.stdout
    check("F3/resources", res_ok, "rendered resources contain 8/16Gi limits and 100m/256Mi requests")

# ------------------------------------------------- FX1: referenced paths
ref_paths = set()
for text in doc_texts.values():
    ref_paths |= set(re.findall(r"(test/stress/[A-Za-z0-9_./-]+)", text))
    ref_paths |= {"deploy/helm/rbgs"}  # helm install/upgrade path used everywhere
missing_paths = [p for p in sorted(ref_paths) if not os.path.exists(os.path.join(REPO, p.rstrip("./")))]
check("FX1/paths", not missing_paths, f"referenced paths missing: {missing_paths or 'none'}")

# ------------------------------------------------- FX2: stress client flags
main_go = read("test/stress/main.go")
defined = set(re.findall(r'flag\.\w+Var\([^,]+,\s*"([a-z-]+)"', main_go))
used = set()
for text in doc_texts.values():
    # only flags passed to the stress client itself (go run ./test/stress/ blocks),
    # not the controller startup args quoted elsewhere in the docs
    for seg in re.findall(r"go run \./test/stress/(?:[^\n]*\\\n)*[^\n]*", text):
        used |= set(re.findall(r"--([a-z-]+)=", seg))
unknown = used - defined
check("FX2/flags", not unknown, f"flags used in docs but not defined: {sorted(unknown) or 'none'}")

# ------------------------------------------------- FX3: helm --set keys
values = read("deploy/helm/rbgs/values.yaml")
vtree = {}
def insert(path):
    node = vtree
    for part in path.split("."):
        node = node.setdefault(part, {})
used_keys = set()
for text in doc_texts.values():
    for key in re.findall(r"--set\s+(controller\.[A-Za-z0-9_.]+)=", text):
        used_keys.add(key)
        insert(key)
bad_keys = []
for key in sorted(used_keys):
    node = {}
    cur = values
    segs = key.split(".")
    ok = True
    for seg in segs:
        mm = re.search(rf"^\s*{re.escape(seg)}:\s*$", cur, re.M)
        mm2 = re.search(rf"^\s*{re.escape(seg)}:\s*\S", cur, re.M)
        if mm:
            start = cur[:mm.start()]
            nxt = re.search(r"^\S", cur[mm.end():], re.M)
            block = cur[mm.end():nxt.start() + mm.end()] if nxt else cur[mm.end():]
            cur = block
        elif mm2:
            break  # leaf with inline value
        else:
            ok = False
            break
    if not ok:
        bad_keys.append(key)
check("FX3/helm-keys", not bad_keys, f"--set keys not resolving in values.yaml: {bad_keys or 'none'} "
      f"(checked {len(used_keys)} keys)")

print(f"\nSUMMARY: {failures} failure(s)")
sys.exit(failures)
