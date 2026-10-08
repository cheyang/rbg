#!/bin/bash
# F4 (contract): The guide shows `kubectl get deploy ... -o jsonpath='{...args}' | tr ',' '\n'`
# producing one arg per line. kubectl jsonpath renders lists space-separated inside
# brackets, so tr ',' '\n' is a no-op and the shown expected output is not reproducible.
# Verified empirically with kubectl dry-run (no cluster needed).
set -u
cd "$(git rev-parse --show-toplevel)"

cat > /tmp/f4-pod.yaml <<'YAML'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: f4-demo
spec:
  selector:
    matchLabels: {app: f4}
  template:
    metadata:
      labels: {app: f4}
    spec:
      containers:
        - name: c
          image: nginx
          args: ["--a=1", "--b=2", "--c=3"]
YAML

raw=$(kubectl apply -f /tmp/f4-pod.yaml --dry-run=client --validate=false -o jsonpath='{.spec.template.spec.containers[0].args}' 2>/dev/null)
echo "raw jsonpath output: ${raw}"
piped=$(printf '%s' "$raw" | tr ',' '\n')
lines=$(printf '%s\n' "$piped" | wc -l)
echo "lines after tr ',' '\\n': ${lines}"

if [ "$lines" -le 1 ] && printf '%s' "$raw" | grep -q ' '; then
  echo "F4 REPRODUCED: jsonpath list output is space-separated; 'tr ,' ''\\n''' cannot"
  echo "produce the one-arg-per-line expected output shown in the guide."
  exit 1
fi
echo "F4 OK"
