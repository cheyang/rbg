import re, subprocess, sys, os
SRC = sys.argv[1]; DST = sys.argv[2]
files = [
    "doc/best-practice/en/08-configuring-autoscaling.md",
    "doc/best-practice/en/08-configuring-autoscaling-guide.md",
    "doc/best-practice/zh/08-configuring-autoscaling.md",
    "doc/best-practice/zh/08-configuring-autoscaling-guide.md",
]
for rel in files:
    dst = os.path.join(DST, rel)
    os.makedirs(os.path.dirname(dst), exist_ok=True)
    t = open(os.path.join(SRC, rel), encoding="utf-8").read()
    # F1: correct org for repo links, chart install, profiler image
    t = t.replace("https://github.com/sgl-project/rbg-planner", "https://github.com/rolebasedgroup/rbg-planner")
    t = t.replace("helm install rbg-planner oci://ghcr.io/sgl-project/charts/rbg-planner",
                  "git clone https://github.com/rolebasedgroup/rbg-planner && helm install rbg-planner ./rbg-planner/charts/rbg-planner")
    t = t.replace("ghcr.io/sgl-project/rbg-profiler:latest", "ghcr.io/rolebasedgroup/rbg-profiler:latest")
    # F2: metricSource enum
    t = t.replace("sglang | vllm | dynamo", "sglang | vllm | patio")
    # F3: drop port: 8000 override after metricSource line; fix table descriptions
    t = re.sub(r"(\n +metricsEndpoint:\n +metricSource: [^\n]*\n) +port: 8000[^\n]*", r"\1", t)
    t = t.replace("Inference engine metrics port", "Planner's own Prometheus metrics port (default 9091)")
    t = t.replace("Metrics port", "Planner's own Prometheus metrics port (default 9091)")
    t = t.replace("Inference engine type", "Metric source type")
    # F4: planner pod selector
    t = t.replace("-l app=rbg-planner", "-l app.kubernetes.io/name=rbg-planner")
    # F5: Related Documents -> link existing siblings, drop stale TODO (en + zh)
    en_old = "<!-- TODO: The following documents have not been created yet; links will be added once they are complete -->\n\n+ Deploying Inference Services with RBG\n+ Using RoleTemplates to Reduce Configuration Duplication\n+ Configuring Rolling Update Strategies\n+ In-Place Update and In-Place Scheduling"
    en_new = "+ [Deploying Inference Services with RBG](./01-deploy-inference-service.md)\n+ [Simplifying Configuration with RoleTemplates](./02-using-role-templates.md)\n+ [Configuring Rolling Update Strategies](./03-configuring-rolling-updates.md)\n+ [Configuring In-Place Update and In-Place Scheduling Strategies](./04-configuring-inplace-update-and-scheduling-policies.md)"
    t = t.replace(en_old, en_new)
    zh_old = "<!-- TODO: 以下文档尚未创建，待文档完成后统一添加链接 -->\n\n+ 使用 RBG 部署推理服务\n+ 使用 RoleTemplates 减少配置重复\n+ 配置滚动更新策略\n+ 原地升级与原地调度"
    zh_new = "+ [使用 RBG 部署推理服务](./01-deploy-inference-service.md)\n+ [使用 RoleTemplates 减少配置重复](./02-using-role-templates.md)\n+ [配置滚动更新策略](./03-configuring-rolling-updates.md)\n+ [配置原地更新与原地调度策略](./04-configuring-inplace-update-and-scheduling-policies.md)"
    t = t.replace(zh_old, zh_new)
    # F7: trailing whitespace
    t = "\n".join(l.rstrip() for l in t.split("\n"))
    open(dst, "w", encoding="utf-8").write(t)
# F6: autocorrect fix mode on zh copies
for rel in files:
    if "/zh/" in rel:
        subprocess.run(["npx", "-y", "autocorrect-node", "--fix", os.path.join(DST, rel)], capture_output=True)
print("scratch fixed copy at", DST)
