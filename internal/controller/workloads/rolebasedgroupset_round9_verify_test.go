package workloads

// Round 9 (head 8f07c37e) contract for the single delta commit
// 8f07c37e "fix(rbgs): compare role templates semantically".
//
//  - The commit swaps needsUpdate's RoleTemplates comparison from
//    reflect.DeepEqual to apiequality.Semantic.DeepEqual, closing R8TPLCHURN
//    (an explicitly-empty collection in a stored role template churned the
//    strategy-less path forever, because JSON round trips drop empty
//    map/slice values and reflect.DeepEqual treats nil != empty).
//  - The author's own regression table
//    (TestRoleBasedGroupSetReconciler_NeedsUpdateEmptyRoleTemplates) is the
//    primary repro: all 5 subtests are RED when the production one-liner is
//    reverted to reflect.DeepEqual and GREEN at head
//    (results/harness-bites-r9.log).
//  - The contract below re-pins the same behavior through the actual child
//    creation path (newRBGForSet serialization) so the carried
//    TestVerifyPR474 suite keeps guarding it after this round.

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

// TestVerifyPR474_NeedsUpdate_SemanticRoleTemplates is the R8TPLCHURN
// contract: a child RBG created from the set's template (whose explicit empty
// collections were dropped by JSON serialization) must NOT be reported as
// needing an update, while a genuine template change must.
func TestVerifyPR474_NeedsUpdate_SemanticRoleTemplates(t *testing.T) {
	set := &workloadsv1alpha2.RoleBasedGroupSet{}
	set.Spec.GroupTemplate.Spec.RoleTemplates = []workloadsv1alpha2.RoleTemplate{{
		Name: "worker",
		Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "worker", Image: "test:v1"}},
		}},
	}}
	// Simulate a declaratively-written explicit empty map/slice the API
	// server keeps, which the child creation round trip drops (omitempty).
	raw := `{"metadata":{"labels":{}},"spec":{"containers":[{"name":"worker","image":"test:v1","env":[]}]}}`
	require.NoError(t, json.Unmarshal([]byte(raw), &set.Spec.GroupTemplate.Spec.RoleTemplates[0].Template))

	original := set.DeepCopy()
	data, err := json.Marshal(newRBGForSet(set, 0))
	require.NoError(t, err)
	child := &workloadsv1alpha2.RoleBasedGroup{}
	require.NoError(t, json.Unmarshal(data, child))
	require.NotEqual(t, set.Spec.GroupTemplate.Spec.RoleTemplates, child.Spec.RoleTemplates,
		"the JSON round trip must drop the explicit empty collections (premise of R8TPLCHURN)")

	r := &RoleBasedGroupSetReconciler{}
	assert.False(t, r.needsUpdate(set, child),
		"an explicitly-empty collection dropped by serialization must not mark the child outdated")
	assert.Equal(t, original, set, "needsUpdate must not mutate the set template")

	child.Spec.RoleTemplates[0].Template.Spec.Containers[0].Image = "test:v2"
	assert.True(t, r.needsUpdate(set, child),
		"a real template change must still require an update")
}
