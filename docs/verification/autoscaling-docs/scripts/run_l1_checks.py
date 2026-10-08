#!/usr/bin/env python3
"""L1 static fact-check for PR #389 docs (doc/best-practice/*/08-configuring-autoscaling*).

Each check compares a factual claim made by the PR docs against ground truth
(in-repo CRDs/controllers for RBG, snapshotted sources under ../ground_truth/ for
the external rbg-planner / KEDA / sglang projects).

Polarity:
  - C*  contract checks: assert the doc's claim is CORRECT (PASS = doc accurate).
  - B*  bug-canaries:   assert the doc's claim is WRONG   (PASS = defect CONFIRMED;
                        flips to FAIL once the doc is fixed, then invert it).
  - N*  nit check:      informational mismatch, PASS = mismatch present.

Run from the repo root (or any dir): `python3 -I scripts/run_l1_checks.py`
Writes a machine-readable summary to stdout; exit code 0 iff no check errored
(canary PASSes do not fail the run — they are the point).
"""
import os
import re
import sys

try:
    import yaml
except ImportError:  # pragma: no cover
    print("FATAL: PyYAML required", file=sys.stderr)
    sys.exit(2)

HERE = os.path.dirname(os.path.abspath(__file__))
VDIR = os.path.join(HERE, "..", "ground_truth")
RESULTS = []

# repo root = 4 levels up from scripts/ (docs/verification/autoscaling-docs/scripts)
ROOT = os.path.abspath(os.path.join(HERE, "..", "..", "..", ".."))


def read(*p):
    path = os.path.join(*p)
    with open(path, "r", encoding="utf-8", errors="replace") as f:
        return f.read()


def doc(rel):
    return read(ROOT, "doc", "best-practice", rel)


DOCS = {
    "en-guide": doc("en/08-configuring-autoscaling-guide.md"),
    "en-concept": doc("en/08-configuring-autoscaling.md"),
    "zh-guide": doc("zh/08-configuring-autoscaling-guide.md"),
    "zh-concept": doc("zh/08-configuring-autoscaling.md"),
}


def check(cid, desc, ok, evidence, polarity):
    status = "PASS" if ok else "FAIL"
    RESULTS.append((cid, polarity, status, desc, evidence))
    print("[%s] %s (%s) %s\n        evidence: %s" % (status, cid, polarity, desc, evidence))


def crd_versions(path, version):
    docs = [d for d in yaml.safe_load_all(read(ROOT, *path)) if d]
    for d in docs:
        if d.get("kind") != "CustomResourceDefinition":
            continue
        for v in d["spec"]["versions"]:
            if v["name"] == version:
                return v
    raise KeyError(version)


# ---------------------------------------------------------------- contract checks
def c1_scaling_adapter_fields():
    v = crd_versions(["config", "crd", "bases", "workloads.x-k8s.io_rolebasedgroups.yaml"], "v1alpha2")
    roles = v["schema"]["openAPIV3Schema"]["properties"]["spec"]["properties"]["roles"]["items"]
    sa = roles["properties"]["scalingAdapter"]["properties"]
    ok = sa["enable"]["type"] == "boolean" and sa["enable"].get("default") is False and "labels" in sa
    check("C1", "doc: roles[].scalingAdapter.{enable,labels} exist on v1alpha2 RBG CRD",
          ok, "enable: %s (default %s), labels present: %s" % (sa["enable"]["type"], sa["enable"].get("default"), "labels" in sa), "contract")


def c2_standalone_pattern():
    v = crd_versions(["config", "crd", "bases", "workloads.x-k8s.io_rolebasedgroups.yaml"], "v1alpha2")
    roles = v["schema"]["openAPIV3Schema"]["properties"]["spec"]["properties"]["roles"]["items"]
    ok = "standalonePattern" in roles["properties"] and "template" in roles["properties"]["standalonePattern"]["properties"]
    check("C2", "doc: roles[].standalonePattern.template exists on v1alpha2 RBG CRD", ok,
          "standalonePattern properties: %s" % sorted(roles["properties"]["standalonePattern"]["properties"]), "contract")


def c3_rbgsa_scale_subresource():
    v = crd_versions(["config", "crd", "bases", "workloads.x-k8s.io_rolebasedgroupscalingadapters.yaml"], "v1alpha2")
    scale = v.get("subresources", {}).get("scale", {})
    ok = (v.get("served") and v.get("storage") and scale.get("specReplicasPath") == ".spec.replicas"
          and scale.get("statusReplicasPath") == ".status.replicas"
          and scale.get("labelSelectorPath") == ".status.selector"
          and "rbgsa" in crd_shortnames(["config", "crd", "bases", "workloads.x-k8s.io_rolebasedgroupscalingadapters.yaml"]))
    check("C3", "doc: RBGSA v1alpha2 is served+storage with /scale subresource and shortname 'rbgsa'", ok,
          "served=%s storage=%s scale=%s" % (v.get("served"), v.get("storage"), scale), "contract")


def crd_shortnames(path):
    docs = [d for d in yaml.safe_load_all(read(ROOT, *path)) if d]
    for d in docs:
        if d.get("kind") == "CustomResourceDefinition":
            return d["spec"]["names"].get("shortNames", [])
    return []


def c4_scale_target_ref():
    v = crd_versions(["config", "crd", "bases", "workloads.x-k8s.io_rolebasedgroupscalingadapters.yaml"], "v1alpha2")
    spec = v["schema"]["openAPIV3Schema"]["properties"]["spec"]["properties"]
    st = spec["scaleTargetRef"]["properties"]
    ok = set(st.keys()) >= {"name", "role"} and "phase" in v["schema"]["openAPIV3Schema"]["properties"]["status"]["properties"]
    check("C4", "doc: RBGSA spec.scaleTargetRef.{name,role} and status.phase exist", ok,
          "scaleTargetRef: %s" % sorted(st), "contract")


def c5_labels():
    src = read(ROOT, "api", "workloads", "constants", "label.go")
    ok = ('GroupNameLabelKey = RBGPrefix + "group-name"' in src and 'RoleNameLabelKey = RBGPrefix + "role-name"' in src)
    check("C5", "doc: pod labels rbg.workloads.x-k8s.io/{group-name,role-name} exist as constants", ok,
          "RBGPrefix label keys found: %s" % ok, "contract")


def c6_bound_phase():
    src = read(ROOT, "api", "workloads", "constants", "constants.go")
    ctrl = read(ROOT, "internal", "controller", "workloads", "rolebasedgroupscalingadapter_controller.go")
    ok = ('AdapterPhaseBound    AdapterPhase = "Bound"' in src and "AdapterPhaseBound" in ctrl)
    check("C6", "doc: RBGSA status.phase 'Bound' is a real phase set by the controller", ok,
          "constants.go defines AdapterPhaseBound='Bound'; controller references it", "contract")


def c7_scale_syncs_rbg():
    ctrl = read(ROOT, "internal", "controller", "workloads", "rolebasedgroupscalingadapter_controller.go")
    ok = "func (r *RoleBasedGroupScalingAdapterReconciler) updateRoleReplicas" in ctrl and "rbg.Spec.Roles[index] = role" in ctrl
    check("C7", "doc: kubectl scale rbgsa -> RBG spec.roles[].replicas sync (Operation 1 Step 3)", ok,
          "updateRoleReplicas() writes rbg.Spec.Roles[i].Replicas via r.client.Update", "contract")


def c8_adapter_lifecycle():
    ctrl = read(ROOT, "internal", "controller", "workloads", "rolebasedgroup_controller.go")
    ok = ctrl.count('"delete scalingAdapter"') >= 2 and "r.client.Delete(ctx, rbgScalingAdapter)" in ctrl
    check("C8", "doc: RBGSA auto-cleaned when role removed / adapter disabled", ok,
          "rolebasedgroup_controller.go deletes adapters in both cases", "contract")


def c9_autoscaler_paths():
    crd = yaml.safe_load(read(VDIR, "..", "harness", "crds", "inference-extension.rolebasedgroup.io_autoscalers.yaml"))
    schema = crd["spec"]["versions"][0]["schema"]["openAPIV3Schema"]

    # extract the AutoScaler yaml block from the concept doc
    m = re.search(r"```yaml\n(apiVersion: inference-extension.*?)```", DOCS["en-concept"], re.S)
    if not m:
        check("C9", "doc: AutoScaler manifest field paths exist on real planner CRD", False, "no AutoScaler yaml block found", "contract")
        return
    manifest = yaml.safe_load(m.group(1))

    missing = []

    def walk(node, schema_node, path):
        if isinstance(node, dict):
            props = schema_node.get("properties", {})
            for k, val in node.items():
                if k in ("apiVersion", "kind", "metadata"):
                    continue
                if k not in props:
                    missing.append(path + "." + k)
                else:
                    walk(val, props[k], path + "." + k)

    walk(manifest.get("spec", {}), schema["properties"]["spec"], "spec")
    ok = not missing
    check("C9", "doc: every spec path in the concept-doc AutoScaler manifest exists on the real planner CRD", ok,
          "missing paths: %s" % (missing or "none"), "contract")


def c10_sglang_flags():
    src = read(VDIR, "sglang_server_args.py")
    ok = ("model_path: str" in src and "tp_size: int = 1" in src and "enable_metrics: bool = False" in src
          and 'disaggregation_mode: Literal["null", "prefill", "decode"]' in src)
    check("C10", "doc: sglang v0.5.9 flags --model-path/--tp-size/--enable-metrics/--disaggregation-mode exist", ok,
          "all four args present in v0.5.9 server_args.py", "contract")


def c11_keda_fields():
    crd = yaml.safe_load(read(VDIR, "..", "harness", "crds", "keda_scaledobjects.yaml"))
    spec = crd["spec"]["versions"][0]["schema"]["openAPIV3Schema"]["properties"]["spec"]["properties"]
    st = spec["scaleTargetRef"]["properties"]
    ok = {"apiVersion", "kind", "name"} <= set(st) and {"minReplicaCount", "maxReplicaCount", "pollingInterval", "cooldownPeriod", "triggers"} <= set(spec)
    check("C11", "doc: ScaledObject fields (scaleTargetRef, min/maxReplicaCount, pollingInterval, triggers) on real KEDA CRD", ok,
          "spec properties: %s" % sorted(spec), "contract")


def c12_misc_planner_claims():
    crd = yaml.safe_load(read(VDIR, "..", "harness", "crds", "inference-extension.rolebasedgroup.io_autoscalers.yaml"))
    si = crd["spec"]["versions"][0]["schema"]["openAPIV3Schema"]["properties"]["spec"]["properties"]["scalingInterval"]
    conn = read(VDIR, "rbg_connector.py")
    ok = si.get("default") == 180 and "patch_namespaced_custom_object_scale" in conn and "_patch_rbg_role_replicas" in conn
    check("C12", "doc: scalingInterval default 180; planner scales 'via RBGSA or direct RBG Patch'", ok,
          "CRD default=%s; connector has both RBGSA scale + RBG patch fallback" % si.get("default"), "contract")


# ---------------------------------------------------------------- bug canaries
def b1_planner_repo_links():
    all_docs = "\n".join(DOCS.values())
    n = all_docs.count("github.com/sgl-project/rbg-planner")
    # live verdict comes from run_l1_network_checks.sh; statically: upstream README
    # (this repo's README.md) links the planner at rolebasedgroup/rbg-planner.
    repo_readme = read(ROOT, "README.md")
    ok = n > 0 and "rolebasedgroup/rbg-planner" in repo_readme and "sgl-project/rbg-planner" not in repo_readme
    check("B1", "DEFECT: docs link github.com/sgl-project/rbg-planner (404) instead of rolebasedgroup/rbg-planner", ok,
          "%d occurrences in PR docs; this repo's own README links rolebasedgroup/rbg-planner" % n, "canary")


def b2_chart_ref():
    all_docs = "\n".join(DOCS.values())
    n = all_docs.count("oci://ghcr.io/sgl-project/charts/rbg-planner")
    readme = read(VDIR, "planner_readme.md")
    ok = n > 0 and "helm install rbg-planner ./charts/rbg-planner" in readme and "oci://ghcr.io/sgl-project" not in readme
    check("B2", "DEFECT: install cmd uses oci://ghcr.io/sgl-project/charts/rbg-planner; upstream installs from local chart path, no OCI chart documented", ok,
          "%d occurrences in PR docs; planner README quick start: helm install rbg-planner ./charts/rbg-planner" % n, "canary")


def b3_profiler_image():
    all_docs = "\n".join(DOCS.values())
    n = all_docs.count("ghcr.io/sgl-project/rbg-profiler")
    values = read(VDIR, "values.yaml")
    ok = n > 0 and "ghcr.io/rolebasedgroup/rbg-profiler" in values
    check("B3", "DEFECT: profiling.image ghcr.io/sgl-project/rbg-profiler contradicts upstream default ghcr.io/rolebasedgroup/rbg-profiler", ok,
          "%d occurrences in PR docs; upstream chart values: %s" % (n, [l for l in values.splitlines() if "rbg-profiler" in l]), "canary")


def b4_metrics_port_semantics():
    crd = yaml.safe_load(read(VDIR, "..", "harness", "crds", "inference-extension.rolebasedgroup.io_autoscalers.yaml"))
    port = (crd["spec"]["versions"][0]["schema"]["openAPIV3Schema"]["properties"]["spec"]
            ["properties"]["implementation"]["properties"]["DynamoPlanner"]["properties"]
            ["metricsEndpoint"]["properties"]["port"])
    ctrl = read(VDIR, "roleautoscaler_controller.go")
    pyplanner = read(VDIR, "planner.py")
    crd_desc = port.get("description", "")
    semantics_own = "planner's own" in crd_desc and "exposition" in crd_desc
    env_mapped = 'PLANNER_PROMETHEUS_PORT' in ctrl and 'Value: strconv.Itoa(spec.metricsPort)' in ctrl
    self_server = "start_http_server(config.planner_prometheus_port)" in pyplanner
    doc_engine_port = "Inference engine metrics port" in DOCS["en-concept"] and "推理引擎指标端口" in DOCS["zh-concept"]
    ok = semantics_own and env_mapped and self_server and doc_engine_port
    check("B4", "DEFECT: doc says metricsEndpoint.port = 'Inference engine metrics port'; CRD says planner's OWN metrics exposition port (default 9091)", ok,
          "CRD description: %r; controller maps it to PLANNER_PROMETHEUS_PORT; planner.py start_http_server(self)=%s; doc mislabels=%s" % (crd_desc, self_server, doc_engine_port), "canary")


def b5_prometheus_wiring():
    guide = DOCS["en-guide"]
    op4 = guide.split("## Operation 4")[1]
    helm_lines = [l for l in guide.splitlines() if "helm install rbg-planner" in l]
    values = read(VDIR, "values.yaml")
    default_ep = [l.strip() for l in values.splitlines() if "endpoint" in l]
    # the planner's Prometheus URL comes ONLY from the helm value prometheus.endpoint
    # (env PROMETHEUS_ENDPOINT on the planner Deployment); the AutoScaler CR has no
    # Prometheus address field (C9 proved the CR schema: metricSource + port only).
    no_endpoint_set = bool(helm_lines) and all("prometheus.endpoint" not in l for l in helm_lines)
    no_scrape_ann = "prometheus.io/scrape" not in op4
    ok = no_endpoint_set and no_scrape_ann
    check("B5", "DEFECT: Operation 4 never sets prometheus.endpoint and pods lack prometheus.io/scrape annotations -> planner has no metrics source", ok,
          "helm install line(s) set prometheus.endpoint: %s; op4 manifest has scrape annotations: %s; chart default endpoint: %s" % (not no_endpoint_set, not no_scrape_ann, default_ep), "canary")


def b6_pod_selector():
    guide = "\n".join([DOCS["en-guide"], DOCS["zh-guide"]])
    ctrl = read(VDIR, "roleautoscaler_controller.go")
    has_plain_app = '"app":' in ctrl
    has_k8s_name = '"app.kubernetes.io/name":     "rbg-planner"' in ctrl
    doc_uses_app = "-l app=rbg-planner" in guide
    ok = has_k8s_name and not has_plain_app and doc_uses_app
    check("B6", "DEFECT: 'kubectl get pods -l app=rbg-planner' matches nothing; planner pods only carry app.kubernetes.io/name + app.kubernetes.io/instance", ok,
          "operator deployment labels contain app.kubernetes.io/name=%s, plain 'app' label=%s; doc uses '-l app=rbg-planner': %s" % (has_k8s_name, has_plain_app, doc_uses_app), "canary")


def b7_phase_enum():
    types = read(VDIR, "roleautoscaler_types.go")
    m = re.search(r"Enum=([^\"]*?)\"", types + '"')
    enum = re.findall(r"Enum=([\w;]+)", types)
    ctrl_readme = read(VDIR, "planner_readme.md")
    doc_says_profiling = "`Ready` or `Profiling`" in DOCS["en-guide"] and "`Profiling`" in DOCS["zh-guide"]
    ok = enum and "Profiling" not in enum[0] and doc_says_profiling
    check("B7", "DEFECT: guide says AutoScaler phase is 'Ready or Profiling'; real phase enum has no Profiling", ok,
          "planner phase enum: %s; doc claims Profiling phase: %s" % (enum, doc_says_profiling), "canary")


def b8_metric_source_enum():
    crd = yaml.safe_load(read(VDIR, "..", "harness", "crds", "inference-extension.rolebasedgroup.io_autoscalers.yaml"))
    enum = (crd["spec"]["versions"][0]["schema"]["openAPIV3Schema"]["properties"]["spec"]
            ["properties"]["implementation"]["properties"]["DynamoPlanner"]["properties"]
            ["metricsEndpoint"]["properties"]["metricSource"].get("enum"))
    all_docs = "\n".join(DOCS.values())
    doc_says_dynamo = bool(re.search(r"sglang \| vllm \| dynamo", all_docs))
    ok = enum and "dynamo" not in enum and "patio" in enum and doc_says_dynamo
    check("B8", "DEFECT: doc comment says metricSource: 'sglang | vllm | dynamo'; CRD enum is sglang|vllm|patio (dynamo invalid, patio missing)", ok,
          "real enum: %s; doc claims dynamo: %s" % (enum, doc_says_dynamo), "canary")


def n1_metric_name():
    collector = read(VDIR, "sglang_metrics_collector.py")
    real = re.search(r'name="(sglang:num_queue_reqs)"', collector)
    doc_query = "sglang_num_queue_requests" in DOCS["en-guide"]
    ok = bool(real) and doc_query
    check("N1", "NIT: KEDA demo queries sglang_num_queue_requests; real sglang v0.5.9 gauge is sglang:num_queue_reqs (sim-only name, self-consistent within the demo)", ok,
          "real gauge: %s; doc query uses simulated name: %s" % (real and real.group(1), doc_query), "nit")


def main():
    for fn in [c1_scaling_adapter_fields, c2_standalone_pattern, c3_rbgsa_scale_subresource,
               c4_scale_target_ref, c5_labels, c6_bound_phase, c7_scale_syncs_rbg,
               c8_adapter_lifecycle, c9_autoscaler_paths, c10_sglang_flags, c11_keda_fields,
               c12_misc_planner_claims,
               b1_planner_repo_links, b2_chart_ref, b3_profiler_image, b4_metrics_port_semantics,
               b5_prometheus_wiring, b6_pod_selector, b7_phase_enum, b8_metric_source_enum,
               n1_metric_name]:
        try:
            fn()
        except Exception as e:  # noqa: BLE001
            RESULTS.append(("ERR", "-", "ERROR", fn.__name__, str(e)))
            print("[ERROR] %s: %s" % (fn.__name__, e))

    contracts = [r for r in RESULTS if r[1] == "contract"]
    canaries = [r for r in RESULTS if r[1] == "canary"]
    nits = [r for r in RESULTS if r[1] == "nit"]
    print("\n==== SUMMARY ====")
    print("contract (doc correct)      : %d/%d PASS" % (sum(1 for r in contracts if r[2] == "PASS"), len(contracts)))
    print("canary (defect confirmed)   : %d/%d PASS" % (sum(1 for r in canaries if r[2] == "PASS"), len(canaries)))
    print("nit   (mismatch present)    : %d/%d PASS" % (sum(1 for r in nits if r[2] == "PASS"), len(nits)))
    for r in RESULTS:
        if r[2] != "PASS":
            print("  !! %s %s: %s (%s)" % (r[2], r[0], r[3], r[4]))
    sys.exit(1 if any(r[2] == "ERROR" for r in RESULTS) else 0)


if __name__ == "__main__":
    main()
