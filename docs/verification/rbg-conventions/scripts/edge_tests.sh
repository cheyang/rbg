#!/usr/bin/env bash
# Edge-case tests for the rbg-conventions verification scripts, beyond selftest.sh.
# Runs against fake-kubectl.sh, so no cluster is needed. Unit layer (L1) of the
# verification harness for PR #568.
#
# Usage: edge_tests.sh
# Exit status: 0 when every test passes, 1 otherwise.
set -uo pipefail
here=$(cd "$(dirname "$0")" && pwd)
scripts=$here/../../../../.agents/skills/rbg-conventions/scripts
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

export KUBECTL=$here/fake-kubectl.sh
export RBG_VERIFY_RETRY_DELAY=0
export RBG_VERIFY_STATE_DIR=$work/state
export PATH=$work/bin:$PATH
mkdir -p "$work/bin" "$RBG_VERIFY_STATE_DIR"
cp "$KUBECTL" "$work/bin/kubectl"
chmod +x "$work/bin/kubectl"
KUBECTL=$work/bin/kubectl
export KUBECTL

# shellcheck source-path=SCRIPTDIR source=../../../../.agents/skills/rbg-conventions/scripts/common.sh
. "$scripts/common.sh"

failed=0
check() { # $1 description, $2 actual, $3 expected
  if [ "$2" = "$3" ]; then
    echo "PASS $1"
  else
    echo "FAIL $1: got '$2', want '$3'"
    failed=1
  fi
}
scenario() { # $1 name; fresh FAKE dir
  FAKE=$work/fake.$1
  export FAKE
  rm -rf "$FAKE"
  mkdir -p "$FAKE"
  printf 'tok123' >"$FAKE/ns_token"
  printf 'uid-1' >"$FAKE/ns_uid"
}
calls() { wc -l <"$FAKE/calls" 2>/dev/null | tr -d ' '; echo -n ''; }
has_call() { grep -q "$1" "$FAKE/calls" 2>/dev/null; }
run() { # sets rc, out, err; runs in a subshell so die() cannot exit this script
  rc=0
  ( "$@" ) >"$work/stdout" 2>"$work/stderr" || rc=$?
  out=$(cat "$work/stdout")
  err=$(cat "$work/stderr")
}
refused='The connection to the server 192.0.2.1:6443 was refused - did you specify the right host or port?'

echo "== kc additional edges"
scenario read-exhaust
printf '%s' "$refused" >"$FAKE/stderr_for_all"
# make every kubectl call fail with a refused connection by overriding the fake
cat >"$work/bin/kubectl" <<EOF
#!/usr/bin/env bash
echo "\$*" >>"$FAKE/calls"
echo "$refused" >&2
exit 1
EOF
chmod +x "$work/bin/kubectl"
run kc get namespaces
check "a read gives up after 5 transient failures" "$rc $(calls)" "1 5"

cat >"$work/bin/kubectl" <<EOF
#!/usr/bin/env bash
exec "$here/fake-kubectl.sh" "\$@"
EOF
chmod +x "$work/bin/kubectl"

echo "== ownership guard (require_owned_namespace / rbg-fixtures.sh)"
scenario guard-ok
printf 'tok123 uid-1\n' >"$RBG_VERIFY_STATE_DIR/1234567.ns" # wrong checksum, must not be read
run require_owned_namespace ns
check "a namespace with no ownership record is refused" "$rc" "2"

scenario guard-tamper
server=$(printf 'https://fake.invalid:6443' | cksum | cut -d' ' -f1)
printf 'tok123 uid-1\n' >"$RBG_VERIFY_STATE_DIR/$server.ns"
printf 'othertoken' >"$FAKE/ns_token"
run require_owned_namespace ns
check "a namespace whose token was replaced is refused" "$rc" "2"

scenario guard-match
printf 'tok123' >"$FAKE/ns_token"
run require_owned_namespace ns
check "a matching token and UID is accepted" "$rc" "0"

echo "== rbg-fixtures.sh apply"
scenario apply-new
rm -f "$RBG_VERIFY_STATE_DIR"/*
run "$scripts/rbg-fixtures.sh" apply -n ns --image registry.example/img:v1
check "apply creates a namespace that does not exist" "$rc" "0"
check "the fixtures are applied with the image substituted (8 images)" \
  "$(grep -c 'registry.example/img:v1' "$FAKE/inputs")" "8"
check "no __IMAGE__ placeholder survives" "$(grep -c '__IMAGE__' "$FAKE/inputs")" "0"
check "the created namespace carries the owner labels" \
  "$(grep -c '"rbg-conventions/owner": "' "$FAKE/inputs")" "1"
server=$(printf 'https://fake.invalid:6443' | cksum | cut -d' ' -f1)
token=$(sed -n 's/.*"rbg-conventions\/owner": "\([0-9a-f]*\)".*/\1/p' "$FAKE/inputs" | head -1)
check "the ownership record is written for the created token" \
  "$(cat "$RBG_VERIFY_STATE_DIR/$server.ns" 2>/dev/null)" "$token uid-ns-created"

scenario apply-reuse
server=$(printf 'https://fake.invalid:6443' | cksum | cut -d' ' -f1)
printf 'tok123 uid-1\n' >"$RBG_VERIFY_STATE_DIR/$server.ns"
touch "$FAKE/ns_exists"
run "$scripts/rbg-fixtures.sh" apply -n ns
check "apply reuses a namespace it owns" "$rc" "0"
check "reusing does not create the namespace again" "$(grep -c '^create ' "$FAKE/calls")" "0"
check "reusing still applies the fixtures" "$(grep -c '^apply ' "$FAKE/calls")" "1"

scenario apply-foreign
touch "$FAKE/ns_exists"
printf 'othertoken' >"$FAKE/ns_token"
run "$scripts/rbg-fixtures.sh" apply -n ns
check "apply refuses a namespace owned by someone else" "$rc" "2"
check "a refused apply writes nothing" "$(grep -c '^apply ' "$FAKE/calls")" "0"

scenario apply-no-record
rm -f "$RBG_VERIFY_STATE_DIR"/*
touch "$FAKE/ns_exists"
run "$scripts/rbg-fixtures.sh" apply -n ns
check "apply refuses an existing namespace with no ownership record" "$rc" "2"
check "no create happens without a record" "$(grep -c '^create ' "$FAKE/calls")" "0"

echo "== rbg-fixtures.sh delete"
scenario delete-ok
server=$(printf 'https://fake.invalid:6443' | cksum | cut -d' ' -f1)
printf 'tok123 uid-1\n' >"$RBG_VERIFY_STATE_DIR/$server.ns"
touch "$FAKE/ns_gone"
run "$scripts/rbg-fixtures.sh" delete -n ns
check "delete removes an owned namespace" "$rc" "0"
check "the delete carries the recorded UID as a precondition" \
  "$(jq -r .preconditions.uid "$FAKE/deletes")" "uid-1"
check "the ownership record is cleaned up" "$([ -f "$RBG_VERIFY_STATE_DIR/$server.ns" ] && echo kept || echo gone)" "gone"

scenario delete-foreign
printf 'othertoken' >"$FAKE/ns_token"
run "$scripts/rbg-fixtures.sh" delete -n ns
check "delete refuses a namespace owned by someone else" "$rc" "2"
check "a refused delete sends no delete request" "$(grep -c '^delete ' "$FAKE/calls")" "0"

echo "== v1alpha1-roundtrip.sh"
scenario roundtrip-pass
rm -f "$RBG_VERIFY_STATE_DIR"/*
server=$(printf 'https://fake.invalid:6443' | cksum | cut -d' ' -f1)
printf 'tok123 uid-1\n' >"$RBG_VERIFY_STATE_DIR/$server.ns"
FAKE_CONVERSION=Webhook
export FAKE_CONVERSION
run "$scripts/v1alpha1-roundtrip.sh" -n ns some-rbg
check "an unchanged round trip passes" "$rc" "0"
check "the object was written through v1alpha1" "$(grep -c '^replace ' "$FAKE/calls")" "1"

scenario roundtrip-none-conversion
FAKE_CONVERSION=None
export FAKE_CONVERSION
run "$scripts/v1alpha1-roundtrip.sh" -n ns some-rbg
check "a CRD without webhook conversion is refused" "$rc" "2"
check "no write happens without webhook conversion" "$(grep -c '^replace ' "$FAKE/calls")" "0"

scenario roundtrip-no-record
unset FAKE_CONVERSION
rm -f "$RBG_VERIFY_STATE_DIR"/*
run "$scripts/v1alpha1-roundtrip.sh" -n ns some-rbg
check "v1alpha1 round trip refuses a namespace without an ownership record" "$rc" "2"

echo "== ssa-key-removal.sh guard"
scenario ssa-guard
rm -f "$RBG_VERIFY_STATE_DIR"/*
run "$scripts/ssa-key-removal.sh" -n ns
check "ssa-key-removal refuses a namespace without an ownership record" "$rc" "2"
check "no patch is sent" "$(grep -c '^patch ' "$FAKE/calls")" "0"

echo "== crd-default-on-read.sh cleanup on unreachable server"
scenario crd-unreachable
rm -f "$RBG_VERIFY_STATE_DIR"/*
cat >"$work/bin/kubectl" <<EOF
#!/usr/bin/env bash
echo "\$*" >>"$FAKE/calls"
if [ "\$1" = create ] && printf '%s' "\$*" | grep -q 'jsonpath={.metadata.uid}'; then
  echo 'crd-uid-1'
  exit 0
fi
echo "$refused" >&2
exit 1
EOF
chmod +x "$work/bin/kubectl"
run "$scripts/crd-default-on-read.sh"
check "crd-default-on-read fails when the server is unreachable" "$rc" "2"

cat >"$work/bin/kubectl" <<EOF
#!/usr/bin/env bash
exec "$here/fake-kubectl.sh" "\$@"
EOF
chmod +x "$work/bin/kubectl"

echo "== rbg-compare.sh extra cases"
printf '#rbg-snapshot v2 namespace=ns\nrbg/r uid=r1\npod/p uid=p1 restarts=main:0\n' >"$work/before"
printf '#rbg-snapshot v2 namespace=ns\nrbg/r uid=r1\npod/p uid=p1 restarts=main:1\n' >"$work/after"
run "$scripts/rbg-compare.sh" "$work/before" "$work/after"
check "a container restart is reported" "$rc" "1"
check "it is explained" "$(printf '%s\n' "$out" | grep -c 'Containers restarted')" "1"

if [ "$failed" -eq 0 ]; then
  echo "all edge tests passed"
fi
exit "$failed"
