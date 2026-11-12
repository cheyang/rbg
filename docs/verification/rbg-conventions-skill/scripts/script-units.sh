#!/usr/bin/env bash
# script-units.sh: offline checks for the rbg-conventions scripts that selftest.sh does not
# cover. Runs against a fake kubectl; needs only bash and jq. Emits "PASS <id>" / "FAIL <id>".
#
# Check IDs are stable; verify-manifest.json maps findings to them. Polarity per check:
#   contract-*  asserts the intended behavior (red on the reviewed head = reproduction)
#   canary-*    asserts the current observed behavior (flips red once fixed)
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
skill=$(cd "$here/../../../../.agents/skills/rbg-conventions" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

cat >"$work/kubectl" <<'EOF'
#!/usr/bin/env bash
# state_file calls 'kubectl config view'; answer it deterministically, outside the numbered answers
case "$*" in
  "config view"*) cat "$FAKE/server" 2>/dev/null || echo 'https://fake:6443'; exit 0 ;;
esac
n=$(($(cat "$FAKE/count" 2>/dev/null || echo 0) + 1))
echo "$n" >"$FAKE/count"
echo "$*" >>"$FAKE/calls"
prev=''
for arg in "$@"; do
  if [ "$prev" = "-f" ] && [ -f "$arg" ]; then cat "$arg" >>"$FAKE/inputs"; fi
  prev=$arg
done
f=$FAKE/$n
[ -e "$f.rc" ] || f=$FAKE/default
[ ! -e "$f.out" ] || cat "$f.out"
[ ! -e "$f.err" ] || cat "$f.err" >&2
exit "$(cat "$f.rc" 2>/dev/null || echo 0)"
EOF
chmod +x "$work/kubectl"
export KUBECTL=$work/kubectl RBG_VERIFY_RETRY_DELAY=0 RBG_VERIFY_STATE_DIR=$work/state

# shellcheck source=/dev/null
. "$skill/scripts/common.sh"

failed=0
pass() { echo "PASS $1"; }
fail() { echo "FAIL $1: $2"; failed=1; }
scenario() { export FAKE=$work/fake.$1; mkdir -p "$FAKE"; }
answer() { echo "$2" >"$FAKE/$1.rc"; [ -z "${3:-}" ] || printf '%s\n' "$3" >"$FAKE/$1.out"; [ -z "${4:-}" ] || printf '%s\n' "$4" >"$FAKE/$1.err"; }
calls() { wc -l <"$FAKE/calls" | tr -d ' '; }
run() { rc=0; "$@" >"$work/stdout" 2>"$work/stderr" || rc=$?; out=$(cat "$work/stdout"); err=$(cat "$work/stderr"); }

refused='The connection to the server 192.0.2.1:6443 was refused - did you specify the right host or port?'
lost='Post "https://192.0.2.1:6443/api/v1/namespaces": http2: client connection lost'

echo "== kc: retry budget"
scenario exhaust
answer default 1 '' "$refused"
run kc get namespaces
[ "$rc" = 1 ] && [ "$(calls)" = 5 ] && pass contract-kc-retry-budget || fail contract-kc-retry-budget "rc=$rc calls=$(calls), want 1 and 5"

scenario waitread
answer 1 1 '' "$refused"
answer 2 0 'condition met'
run kc wait --for=condition=Ready x --timeout=1s
[ "$rc" = 0 ] && [ "$(calls)" = 2 ] && pass contract-kc-wait-is-read || fail contract-kc-wait-is-read "rc=$rc calls=$(calls)"

echo "== delete_with_uid: transient get after a lost delete"
scenario delunknown
answer 1 1 '' "$lost"
answer 2 1 '' "$refused"
rc=0; ( delete_with_uid /api/v1/namespaces/x u1 ) >/dev/null 2>&1 || rc=$?
ndeletes=$(grep -c '^delete' "$FAKE/calls")
[ "$rc" = 1 ] && [ "$ndeletes" = 1 ] && pass contract-delete-unknown-no-blind-retry || fail contract-delete-unknown-no-blind-retry "rc=$rc deletes=$ndeletes (want rc=1, exactly one delete)"

echo "== require_owned_namespace"
# state_file runs 'kubectl config view' through the fake, and require_owned_namespace then
# gets the namespace; both answers come from the default slot here (harmless: the server string
# is only cksum'd, and the same default feeds both calls within a scenario).
mkdir -p "$RBG_VERIFY_STATE_DIR"
scenario own-missing
answer default 1 '' 'Error from server (NotFound): namespaces "nevercreated" not found'
rc=0; ( require_owned_namespace nevercreated ) >/dev/null 2>&1 || rc=$?
[ "$rc" = 2 ] && pass contract-owner-no-record || fail contract-owner-no-record "rc=$rc"

scenario own-tampered
# state file says aaaa.../u1, the cluster says bbbb.../u2 -> must refuse
printf 'aaaaaaaaaaaaaaaa u1\n' >"$(state_file tamperedns)"
answer default 0 'bbbbbbbbbbbbbbbb u2'
rc=0; ( require_owned_namespace tamperedns ) >/dev/null 2>&1 || rc=$?
[ "$rc" = 2 ] && pass contract-owner-tampered-refused || fail contract-owner-tampered-refused "rc=$rc"

scenario own-ok
answer default 0 'cccccccccccccccc u7'
printf 'cccccccccccccccc u7\n' >"$(state_file goodns)"
rc=0; ( require_owned_namespace goodns ) >/dev/null 2>&1 || rc=$?
[ "$rc" = 0 ] && pass contract-owner-match-accepted || fail contract-owner-match-accepted "rc=$rc"

echo "== state_file is per-cluster"
a=$(KUBECTL="$work/kubectl-aaa" ; printf x) # noop, keep bash happy
scenario sf-a
echo 'https://cluster-a:6443' >"$FAKE/server"
fa=$(state_file ns1)
scenario sf-b
echo 'https://example.com' >"$FAKE/server"
fb=$(state_file ns1)
[ "$fa" != "$fb" ] && pass contract-state-file-per-cluster || fail contract-state-file-per-cluster "$fa == $fb"

echo "== rbg-snapshot.sh: rendering"
scenario snap-rich
answer 1 1 '' 'Error from server (NotFound): the server could not find the requested resource'
answer 2 0 "$(cat <<'EOF'
{"kind": "List", "items": [
  {"kind": "RoleBasedGroup", "metadata": {"name": "rbg1", "uid": "r1", "generation": 2,
     "labels": {"app": "demo"}, "annotations": {"note": "a b&c"}}},
  {"kind": "RoleInstanceSet", "metadata": {"name": "rbg1-server", "uid": "s1", "generation": 3,
     "labels": {"rbg.workloads.x-k8s.io/role-revision-server": "abc123"}},
   "status": {"updateRevision": "rev-2", "currentRevision": "rev-1"}},
  {"kind": "Service", "metadata": {"name": "svc1", "uid": "v1"},
   "spec": {"type": "ClusterIP", "clusterIP": "10.0.0.9",
            "ports": [{"name": "http", "port": 80, "protocol": "TCP", "targetPort": 8080}],
            "selector": {"app": "demo"}}},
  {"kind": "Pod", "metadata": {"name": "p1", "uid": "p1",
     "ownerReferences": [{"kind": "RoleInstance", "name": "ri1", "controller": true}],
     "labels": {"controller-revision-hash": "rev-2"}},
   "spec": {"nodeName": "n1", "containers": [{"name": "main", "image": "img:1"}]},
   "status": {"containerStatuses": [{"name": "main", "restartCount": 2}]}}
]}
EOF
)"
answer 3 0 "$(cat <<'EOF'
{"items": [
  {"metadata": {"labels": {"kubernetes.io/service-name": "svc1"}},
   "endpoints": [{"targetRef": {"name": "p1"}, "conditions": {"ready": true}},
                 {"targetRef": {"name": "p2"}, "conditions": {"ready": false}},
                 {"targetRef": {"name": "p3"}}]}
]}
EOF
)"
run "$skill/scripts/rbg-snapshot.sh" -n demo
snap=$out
ok=true
printf '%s\n' "$out" | grep -q '^#rbg-snapshot v2 namespace=demo$' || ok=false
printf '%s\n' "$out" | grep -q '^ris/rbg1-server uid=s1 generation=3 updateRevision=rev-2 currentRevision=rev-1 roleHash=rbg.workloads.x-k8s.io/role-revision-server=abc123$' || ok=false
printf '%s\n' "$out" | grep -q '^svc/svc1 uid=v1 type=ClusterIP clusterIP=10.0.0.9 ports=http:80/TCP>8080 publishNotReady=false selector=app=demo$' || ok=false
printf '%s\n' "$out" | grep -q '^pod/p1 uid=p1 owner=RoleInstance/ri1 node=n1 images=main:img:1 initImages=- revision=rev-2 restarts=main:2 initRestarts=-$' || ok=false
printf '%s\n' "$out" | grep -q '^rbg/rbg1#annotation:note value=a%20b%26c$' || ok=false
printf '%s\n' "$out" | grep -q '^endpoints/svc1 ready=p1,p3$' || ok=false
$ok && pass contract-snapshot-rendering || { fail contract-snapshot-rendering "unexpected snapshot:"; printf '%s\n' "$snap" | sed 's/^/    /'; }

# determinism: same answers in a different item order must produce the same bytes
scenario snap-order
answer 1 1 '' 'x'
answer 2 0 "$(printf '%s' "$snap" >/dev/null; cat <<'EOF'
{"kind": "List", "items": [
  {"kind": "Service", "metadata": {"name": "svc1", "uid": "v1"},
   "spec": {"type": "ClusterIP", "clusterIP": "10.0.0.9",
            "ports": [{"name": "http", "port": 80, "protocol": "TCP", "targetPort": 8080}],
            "selector": {"app": "demo"}}},
  {"kind": "RoleBasedGroup", "metadata": {"name": "rbg1", "uid": "r1", "generation": 2,
     "labels": {"app": "demo"}, "annotations": {"note": "a b&c"}}},
  {"kind": "Pod", "metadata": {"name": "p1", "uid": "p1",
     "ownerReferences": [{"kind": "RoleInstance", "name": "ri1", "controller": true}],
     "labels": {"controller-revision-hash": "rev-2"}},
   "spec": {"nodeName": "n1", "containers": [{"name": "main", "image": "img:1"}]},
   "status": {"containerStatuses": [{"name": "main", "restartCount": 2}]}},
  {"kind": "RoleInstanceSet", "metadata": {"name": "rbg1-server", "uid": "s1", "generation": 3,
     "labels": {"rbg.workloads.x-k8s.io/role-revision-server": "abc123"}},
   "status": {"updateRevision": "rev-2", "currentRevision": "rev-1"}}
]}
EOF
)"
answer 3 0 "$(cat <<'EOF'
{"items": [
  {"metadata": {"labels": {"kubernetes.io/service-name": "svc1"}},
   "endpoints": [{"targetRef": {"name": "p3"}},
                 {"targetRef": {"name": "p1"}, "conditions": {"ready": true}},
                 {"targetRef": {"name": "p2"}, "conditions": {"ready": false}}]}
]}
EOF
)"
run "$skill/scripts/rbg-snapshot.sh" -n demo
[ "$out" = "$snap" ] && pass contract-snapshot-deterministic || fail contract-snapshot-deterministic "order changed the snapshot"

echo "== rbg-snapshot.sh: settle timeout (F1)"
scenario settle
answer 1 1 '' 'x'
# alternating answers: every objects-get bumps the restart count so nothing ever settles
for n in 2 4 6 8 10 12; do
  i=$((n / 2))
  answer "$n" 0 "{\"kind\": \"List\", \"items\": [
    {\"kind\": \"RoleBasedGroup\", \"metadata\": {\"name\": \"rbg1\", \"uid\": \"r1\", \"generation\": 1}},
    {\"kind\": \"Pod\", \"metadata\": {\"name\": \"p1\", \"uid\": \"p1\"},
     \"spec\": {\"containers\": [{\"name\": \"main\", \"image\": \"img:1\"}]},
     \"status\": {\"containerStatuses\": [{\"name\": \"main\", \"restartCount\": $i}]}}]}"
  answer $((n + 1)) 0 '{"items": []}'
done
answer default 0 '{"items": []}'
rc=0
"$skill/scripts/rbg-snapshot.sh" -n demo --settle 1 --timeout 3 >"$work/stdout" 2>"$work/stderr" || rc=$?
# contract: on timeout the script must not emit an authoritative-looking snapshot on stdout
if [ "$rc" = 3 ] && ! grep -q '^#rbg-snapshot' "$work/stdout"; then
  pass contract-settle-timeout-quiet
else
  fail contract-settle-timeout-quiet "rc=$rc, stdout $(grep -c '^#rbg-snapshot' "$work/stdout") header line(s) (want rc=3 and no snapshot on stdout)"
fi
# canary documenting current behavior: the unsettled snapshot IS printed today
if grep -q '^#rbg-snapshot v2 namespace=demo' "$work/stdout"; then
  pass canary-settle-timeout-prints-snapshot
else
  fail canary-settle-timeout-prints-snapshot "behavior changed; re-check F1"
fi

echo "== rbg-compare.sh"
mk() { printf '#rbg-snapshot v2 namespace=ns\n%s\n' "$1" >"$2"; }
run_compare() { rc=0; "$skill/scripts/rbg-compare.sh" "$1" "$2" >"$work/cout" 2>"$work/cerr" || rc=$?; cout=$(cat "$work/cout"); }

# label removal + pod revision + svc selector + uid change + added CR all explained
mk 'rbg/x uid=u1 generation=1
rbg/x#label:old value=gone
pod/p uid=p1 owner=- node=n images=a:1 initImages=- revision=r1 restarts=a:0 initRestarts=-
svc/s uid=s1 type=ClusterIP clusterIP=1.2.3.4 ports=- publishNotReady=false selector=a=b' "$work/b"
mk 'rbg/x uid=p9 generation=1
pod/p uid=p1 owner=- node=n images=a:1 initImages=- revision=r2 restarts=a:0 initRestarts=-
svc/s uid=s1 type=ClusterIP clusterIP=1.2.3.4 ports=- publishNotReady=false selector=a=c
cr/rbg1-abc owner=RoleBasedGroup/rbg1 revision=7' "$work/a"
run_compare "$work/b" "$work/a"
cok=true
[ "$rc" = 1 ] || cok=false
printf '%s\n' "$cout" | grep -q 'Labels or annotations were removed' || cok=false
printf '%s\n' "$cout" | grep -q 'Pods or RoleInstances moved to a new revision' || cok=false
printf '%s\n' "$cout" | grep -q 'A Service was rewritten' || cok=false
printf '%s\n' "$cout" | grep -q 'deleted and recreated under the same name' || cok=false
printf '%s\n' "$cout" | grep -q 'New ControllerRevisions' || cok=false
$cok && pass contract-compare-hints || { fail contract-compare-hints "missing hint:"; printf '%s\n' "$cout" | sed 's/^/    /'; }

# F3 canary: a removed ControllerRevision produces a diff line but no "What it means" hint
mk 'rbg/x uid=u1 generation=1
cr/old owner=RoleBasedGroup/x revision=3' "$work/b2"
mk 'rbg/x uid=u1 generation=1' "$work/a2"
run_compare "$work/b2" "$work/a2"
if printf '%s\n' "$cout" | grep -q 'removed  cr/old' && ! printf '%s\n' "$cout" | grep -q '^  - '; then
  pass canary-removed-cr-unexplained
else
  fail canary-removed-cr-unexplained "behavior changed; re-check F3: rc=$rc"; printf '%s\n' "$cout" | sed 's/^/    /'
fi

# refuses an empty file and a no-RBG snapshot
: >"$work/empty"
run_compare "$work/empty" "$work/a2"
[ "$rc" = 2 ] && pass contract-compare-refuses-empty || fail contract-compare-refuses-empty "rc=$rc"
mk 'pod/p uid=p1 owner=- node=n images=a:1 initImages=- revision=r1 restarts=a:0 initRestarts=-' "$work/norbg"
run_compare "$work/norbg" "$work/norbg"
[ "$rc" = 2 ] && pass contract-compare-refuses-no-rbg || fail contract-compare-refuses-no-rbg "rc=$rc"

echo "== rbg-fixtures.sh apply: lost create response (F4)"
scenario fx-lost
answer 1 0 ''                     # get namespace -> not found (empty stdout)
answer 2 1 '' "$lost"             # create namespace: request sent, response lost
echo '(state_file config view is answered out of band)' >/dev/null
run "$skill/scripts/rbg-fixtures.sh" apply -n orphan
# contract: the error must tell the user the namespace may exist anyway and needs manual cleanup
if [ "$rc" = 2 ] && printf '%s' "$err" | grep -qi 'may exist\|delete it by hand\|might exist'; then
  pass contract-apply-lost-create-guides-cleanup
else
  fail contract-apply-lost-create-guides-cleanup "rc=$rc, stderr='$err'"
fi
# canary documenting current behavior: the message is a bare "cannot create namespace"
if [ "$rc" = 2 ] && printf '%s' "$err" | grep -q 'cannot create namespace orphan$'; then
  pass canary-apply-lost-create-bare-message
else
  fail canary-apply-lost-create-bare-message "behavior changed; re-check F4: rc=$rc err='$err'"
fi

echo "== v1alpha1-roundtrip.sh: guards"
scenario rt-none
answer default 0 'None'
run "$skill/scripts/v1alpha1-roundtrip.sh" -n ns some-rbg
# namespace is not owned -> must refuse before touching the CRD
[ "$rc" = 2 ] && printf '%s' "$err" | grep -q 'no ownership record' && pass contract-roundtrip-requires-ownership || fail contract-roundtrip-requires-ownership "rc=$rc err=$err"

scenario rt-strategy
mkdir -p "$RBG_VERIFY_STATE_DIR"
answer default 0 'dddddddddddddddd u3'
printf 'dddddddddddddddd u3\n' >"$(state_file ns)"
answer 1 0 'dddddddddddddddd u3'   # get namespace (require_owned_namespace)
answer 2 0 'None'                 # get crd ... conversion.strategy
run "$skill/scripts/v1alpha1-roundtrip.sh" -n ns some-rbg
if [ "$rc" = 2 ] && printf '%s' "$err" | grep -q "not Webhook; refusing"; then
  pass contract-roundtrip-refuses-none
else
  fail contract-roundtrip-refuses-none "rc=$rc err=$err"
fi

scenario rt-conflict
answer default 0 'dddddddddddddddd u3'
printf 'dddddddddddddddd u3\n' >"$(state_file ns)"
answer 1 0 'dddddddddddddddd u3'  # get namespace (require_owned_namespace)
answer 2 0 'Webhook'              # get crd conversion.strategy
answer 3 0 '{"metadata":{"generation":5},"spec":{"roles":[{"name":"a"}]}}'
answer 4 0 '{"metadata":{"generation":5},"spec":{"roles":[{"name":"a"}]}}'
answer 5 1 '' 'Error from server (Conflict): Operation cannot be fulfilled on rolebasedgroups.workloads.x-k8s.io "some-rbg": the object has been modified; please apply your changes to the latest version and try again'
run "$skill/scripts/v1alpha1-roundtrip.sh" -n ns some-rbg
# a resourceVersion conflict is an error (exit 2), never silently retried
if [ "$rc" = 2 ] && [ "$(calls)" = 5 ]; then
  pass contract-roundtrip-conflict-not-retried
else
  fail contract-roundtrip-conflict-not-retried "rc=$rc calls=$(calls)"
fi

echo "== fixtures image substitution"
scenario fx-image
answer 1 0 ''    # get namespace -> not found
answer 2 0 'u42' # create namespace -> uid
answer 3 0 ''    # apply
run "$skill/scripts/rbg-fixtures.sh" apply -n imgtest --image 'registry.example.com/team/img:v1.2.3'
grep -q 'image: registry.example.com/team/img:v1.2.3' "$FAKE/inputs" && ! grep -q '__IMAGE__' "$FAKE/inputs" \
  && pass contract-fixture-image-substitution || fail contract-fixture-image-substitution "inputs lack the image"

echo
if [ "$failed" -eq 0 ]; then echo "ALL PASS"; else echo "SOME CHECKS FAILED"; fi
exit "$failed"
