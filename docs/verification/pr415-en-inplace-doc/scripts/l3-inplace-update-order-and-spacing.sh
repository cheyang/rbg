#!/bin/bash
# Measure the rollout ORDER and SPACING of an in-place update, using the
# guide's own configuration (maxUnavailable: 1, gracePeriodSeconds: 30).
set -u
NS=l3o
kubectl create ns $NS --dry-run=client -o yaml | kubectl apply -f - >/dev/null 2>&1

cat > /tmp/l3o.yaml <<EOF
apiVersion: workloads.x-k8s.io/v1alpha2
kind: RoleBasedGroup
metadata:
  name: o
  namespace: $NS
spec:
  roles:
  - name: backend
    replicas: 4
    rolloutStrategy:
      type: RollingUpdate
      rollingUpdate:
        maxUnavailable: 1
        type: InPlaceIfPossible
        inPlaceUpdateStrategy:
          gracePeriodSeconds: 30
    standalonePattern:
      template:
        spec:
          containers:
          - name: c
            image: registry-cn-hongkong.ack.aliyuncs.com/acs/busybox:v1.29.2
            command: ["sh","-c","sleep 100000"]
EOF
kubectl apply -f /tmp/l3o.yaml >/dev/null 2>&1

echo "waiting for 4 pods to be Running..."
for i in $(seq 1 60); do
  r=$(kubectl -n $NS get pods --no-headers 2>/dev/null | awk '$3=="Running"' | wc -l)
  [ "$r" -ge 4 ] && break
  sleep 3
done
echo "baseline:"
kubectl -n $NS get pods -o custom-columns=NAME:.metadata.name,START:.status.startTime,RESTARTS:.status.containerStatuses[0].restartCount --no-headers | sort

echo
echo "=== patching image; polling every 2s, recording the instant each pod's restartCount increments ==="
T0=$(date +%s)
kubectl -n $NS patch rbg o --type=json \
  -p '[{"op":"replace","path":"/spec/roles/0/standalonePattern/template/spec/containers/0/image","value":"registry-cn-hongkong.ack.aliyuncs.com/acs/busybox:1.30"}]' >/dev/null
echo "patch applied at t=0"

declare -A seen
done_count=0
for i in $(seq 1 150); do   # up to 300s
  now=$(( $(date +%s) - T0 ))
  while read -r name rc; do
    [ -z "${name:-}" ] && continue
    if [ "${rc:-0}" != "0" ] && [ -z "${seen[$name]:-}" ]; then
      seen[$name]=$now
      echo "  t=+${now}s  $name  restartCount -> $rc"
      done_count=$((done_count+1))
    fi
  done < <(kubectl -n $NS get pods --no-headers \
             -o custom-columns=NAME:.metadata.name,RC:.status.containerStatuses[0].restartCount 2>/dev/null)
  [ "$done_count" -ge 4 ] && break
  sleep 2
done

echo
echo "=== ORDER (by the moment each pod restarted) ==="
for k in "${!seen[@]}"; do echo "${seen[$k]} $k"; done | sort -n | awk '{printf "  +%-5ss  %s\n", $1, $2}'

echo
echo "=== spacing between consecutive updates ==="
prev=""; prevname=""
for k in "${!seen[@]}"; do echo "${seen[$k]} $k"; done | sort -n | while read -r t n; do
  if [ -n "$prev" ]; then echo "  $prevname -> $n : $((t - prev))s"; fi
  prev=$t; prevname=$n
done

echo
echo "=== final state (startTime must be unchanged = AGE not reset) ==="
kubectl -n $NS get pods -o custom-columns=NAME:.metadata.name,START:.status.startTime,RESTARTS:.status.containerStatuses[0].restartCount,IMAGE:.status.containerStatuses[0].image --no-headers | sort
