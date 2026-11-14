#!/usr/bin/env bash
# check-drain-manifests.sh — contract check for PR #587: the manager Deployment
# must carry an exec preStop `/bin/sleep <N>` (N>0) and a pod
# terminationGracePeriodSeconds >= N + 30 (controller-runtime default
# GracefulShutdownTimeout) in ALL THREE shipped install surfaces:
#   1. the Helm chart (rendered with default + variant values, if helm present)
#   2. config/manager/manager.yaml (the kustomize base)
#   3. deploy/kubectl/manifests.yaml (the generated installer)
# Run from the repo root of the ref under test. Exit 0 iff all hold.
set -euo pipefail
[ -d config/manager ] || { echo "run from repo root" >&2; exit 2; }

python3 - <<'PY'
import sys, subprocess, yaml

GRACE_FLOOR = 30  # controller-runtime defaultGracefulShutdownPeriod; cmd/rbgs does not override

def check(name, deploy, failures):
    spec = deploy['spec']['template']['spec']
    containers = spec['containers']
    if len(containers) != 1:
        failures.append(f"{name}: want 1 manager container, got {len(containers)}")
        return
    c = containers[0]
    cmd = (((c.get('lifecycle') or {}).get('preStop') or {}).get('exec') or {}).get('command')
    if not (isinstance(cmd, list) and len(cmd) == 2 and cmd[0] == '/bin/sleep'):
        failures.append(f"{name}: want exec preStop [/bin/sleep <N>], got {cmd!r}")
        return
    try:
        n = int(cmd[1])
        assert n > 0
    except (ValueError, AssertionError):
        failures.append(f"{name}: preStop sleep {cmd[1]!r} is not a positive int")
        return
    grace = spec.get('terminationGracePeriodSeconds')
    if grace is None or grace < n + GRACE_FLOOR:
        failures.append(f"{name}: terminationGracePeriodSeconds={grace}, want >= {n + GRACE_FLOOR}")
        return
    print(f"OK {name}: preStop sleep {n}s, grace {grace}s")

def manager_deploy(docstream, deploy_name):
    docs = [d for d in yaml.safe_load_all(docstream) if d]
    for d in docs:
        if d.get('kind') == 'Deployment' and d['metadata']['name'] == deploy_name:
            return d
    raise SystemExit(f"Deployment {deploy_name} not found")

failures = []

# 1. Helm chart (default + representative variants)
try:
    for args in ([], ['--set', 'controller.pprof.enabled=true'],
                 ['--set', 'controller.features.portAllocator.enabled=true'],
                 ['--set', 'controller.deprecatedWorkloadTypes.enabled=false']):
        out = subprocess.run(['helm', 'template', 'rbgs', 'deploy/helm/rbgs'] + args,
                             capture_output=True, text=True, check=True).stdout
        check('helm' + ('(' + ' '.join(args) + ')' if args else '(default)'),
              manager_deploy(out, 'rbgs-controller-manager'), failures)
except FileNotFoundError:
    print("SKIP helm not installed; chart render checks skipped")

# 2. kustomize base
check('config/manager/manager.yaml',
      manager_deploy(open('config/manager/manager.yaml').read(), 'controller-manager'), failures)

# 3. generated installer
check('deploy/kubectl/manifests.yaml',
      manager_deploy(open('deploy/kubectl/manifests.yaml').read(), 'rbgs-controller-manager'), failures)

if failures:
    print("DRAIN CONTRACT FAILURES:")
    for f in failures:
        print(" -", f)
    sys.exit(1)
print("drain contract holds on all shipped install surfaces")
PY
