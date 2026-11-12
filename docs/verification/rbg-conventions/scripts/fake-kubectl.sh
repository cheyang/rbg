#!/usr/bin/env bash
# Dispatching fake kubectl for the offline edge tests (edge_tests.sh).
# Behavior is driven by files in $FAKE:
#   ns_token, ns_uid  what `get namespace NS -o jsonpath=...` reports
#   ns_exists         namespace exists at all
#   ns_gone           delete loop: namespace already gone
#   delete_rc         rc of `delete --raw ... -f -` (default 0)
#   label_removed     ssa-key-removal: label gone after the patch
# All arguments are logged to $FAKE/calls; every file passed with -f is
# appended to $FAKE/inputs; DeleteOptions bodies to $FAKE/deletes.
set -u
FAKE=${FAKE:?}
log() { printf '%s\n' "$*" >>"$FAKE/calls"; }
log "$*"
prev=''
for arg in "$@"; do
  if [ "$prev" = "-f" ] && [ -f "$arg" ]; then cat "$arg" >>"$FAKE/inputs"; fi
  prev=$arg
done
one() { [ -f "$FAKE/$1" ] && cat "$FAKE/$1"; }

case "$*" in
  'config view --minify -o jsonpath={.clusters[0].cluster.server}')
    echo 'https://fake.invalid:6443'; exit 0 ;;
esac

case "$1" in
  get)
    case "$*" in
      *'--ignore-not-found -o name'*)
        [ -f "$FAKE/ns_exists" ] && echo "namespace/ns" ; exit 0 ;;
      *'jsonpath={.metadata.labels.rbg-conventions/owner} {.metadata.uid}'*)
        echo "$(one ns_token) $(one ns_uid)"; exit 0 ;;
      *'--ignore-not-found -o jsonpath={.metadata.uid}'*)
        [ -f "$FAKE/ns_gone" ] || one ns_uid; exit 0 ;;
      *'jsonpath={.spec.conversion.strategy}'*)
        echo "${FAKE_CONVERSION:-Webhook}"; exit 0 ;;
      *roleinstancesets*'-o json'*)
        if [ -f "$FAKE/label_removed" ]; then echo '{}'; else echo '{"metadata":{"labels":{"rbg-conventions/remove-me":"true"}}}'; fi
        exit 0 ;;
      *'raw /api/v1/namespaces/ns'*)
        exit 1 ;;
      *) echo '{}'; exit 0 ;;
    esac ;;
  create)
    case "$*" in
      *'-o jsonpath={.metadata.uid} -f'*) echo 'uid-ns-created'; exit 0 ;;
      *) echo 'created'; exit 0 ;;
    esac ;;
  delete)
    case "$*" in
      *'--raw'*)
        prev=''
        for arg in "$@"; do
          if [ "$prev" = "-f" ] && [ -f "$arg" ]; then cat "$arg" >>"$FAKE/deletes"; fi
          prev=$arg
        done
        exit "$(one delete_rc 2>/dev/null || echo 0)" ;;
      *) exit 0 ;;
    esac ;;
  apply) echo 'fixture/x created'; exit "$(one apply_rc 2>/dev/null || echo 0)" ;;
  patch) touch "$FAKE/label_removed"; echo 'patched'; exit 0 ;;
  replace) echo 'replaced'; exit 0 ;;
  wait) exit 0 ;;
  *) exit 0 ;;
esac
