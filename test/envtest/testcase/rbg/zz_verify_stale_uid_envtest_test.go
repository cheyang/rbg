/*
Verification harness for PR #500 review (Reviewer B / Codex).
Additive test file — production code untouched.

Integration layer (L2): runs the real RBGSet/RBG/RBGSA controllers against a
real API server (envtest). envtest runs no garbage collector, so deleting a
parent leaves its children behind with dangling owner references — exactly the
"recreated same-named parent coexists with the predecessor's children" window
this PR targets.

Polarity: CONTRACT tests. They assert the intended correct behavior:
- On the base branch they FAIL (the foreign child is deleted on scale-down, the
  stale adapter keeps the predecessor's owner reference).
- On the PR head they PASS.
*/

package rbg

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/scale"
	"sigs.k8s.io/rbgs/test/envtest/testutil"
	wrappersv2 "sigs.k8s.io/rbgs/test/wrappers/v1alpha2"
)

var _ = Describe("Verify PR500: stale-UID children of a recreated parent", func() {
	const (
		verifyTimeout  = time.Second * 30
		verifyInterval = time.Millisecond * 250
	)

	var testNs string

	BeforeEach(func() {
		testNs = fmt.Sprintf("verify-p500-%d", time.Now().UnixNano())
		testutil.CreateNamespace(testNs)
	})

	AfterEach(func() {
		testutil.DeleteNamespace(testNs)
	})

	It("P0/P1: a leftover child RBG of a same-named predecessor is not deleted on scale-down and not counted in status", func() {
		setName := "verify-set"
		childKey := types.NamespacedName{Name: setName + "-0", Namespace: testNs}
		setKey := types.NamespacedName{Name: setName, Namespace: testNs}

		role := wrappersv2.BuildStandaloneRole("worker").Obj()
		mkSet := func(replicas int32) *workloadsv1alpha2.RoleBasedGroupSet {
			return &workloadsv1alpha2.RoleBasedGroupSet{
				ObjectMeta: metav1.ObjectMeta{Name: setName, Namespace: testNs},
				Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
					Replicas: ptr.To(replicas),
					GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
						Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{role}},
					},
				},
			}
		}

		By("creating the first set and waiting for its child")
		Expect(testutil.K8sClient.Create(testutil.Ctx, mkSet(1))).To(Succeed())
		child := &workloadsv1alpha2.RoleBasedGroup{}
		Eventually(func() error {
			return testutil.K8sClient.Get(testutil.Ctx, childKey, child)
		}, verifyTimeout, verifyInterval).Should(Succeed())
		oldUID := metav1.GetControllerOfNoCopy(child).UID
		Expect(oldUID).NotTo(BeEmpty())

		By("deleting the set; envtest runs no GC, so the child lingers with a dangling owner reference")
		toDelete := &workloadsv1alpha2.RoleBasedGroupSet{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, setKey, toDelete)).To(Succeed())
		Expect(testutil.K8sClient.Delete(testutil.Ctx, toDelete)).To(Succeed())
		Eventually(func() bool {
			err := testutil.K8sClient.Get(testutil.Ctx, setKey, &workloadsv1alpha2.RoleBasedGroupSet{})
			return apierrors.IsNotFound(err)
		}, verifyTimeout, verifyInterval).Should(BeTrue())

		By("recreating a same-named set scaled to zero; the foreign child must survive")
		Expect(testutil.K8sClient.Create(testutil.Ctx, mkSet(0))).To(Succeed())
		Consistently(func() error {
			return testutil.K8sClient.Get(testutil.Ctx, childKey, &workloadsv1alpha2.RoleBasedGroup{})
		}, 8*time.Second, verifyInterval).Should(Succeed(),
			"contract: the new set must not delete a child controlled by the predecessor's UID")

		By("scaling to 1; the foreign child occupies the name, so it must not be adopted or counted")
		set := &workloadsv1alpha2.RoleBasedGroupSet{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, setKey, set)).To(Succeed())
		set.Spec.Replicas = ptr.To(int32(1))
		Expect(testutil.K8sClient.Update(testutil.Ctx, set)).To(Succeed())
		Consistently(func() int32 {
			fresh := &workloadsv1alpha2.RoleBasedGroupSet{}
			if err := testutil.K8sClient.Get(testutil.Ctx, setKey, fresh); err != nil {
				return -1
			}
			return fresh.Status.Replicas
		}, 8*time.Second, verifyInterval).Should(Equal(int32(0)),
			"contract: a foreign-UID child must not be counted in status.replicas")
		freshChild := &workloadsv1alpha2.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, childKey, freshChild)).To(Succeed())
		Expect(metav1.GetControllerOfNoCopy(freshChild).UID).To(Equal(oldUID),
			"contract: the foreign child must not be adopted by the new set")

		By("once the leftover is gone (GC in a real cluster), a replacement owned by the new set appears")
		Expect(testutil.K8sClient.Delete(testutil.Ctx, freshChild)).To(Succeed())
		Eventually(func() bool {
			repl := &workloadsv1alpha2.RoleBasedGroup{}
			if err := testutil.K8sClient.Get(testutil.Ctx, childKey, repl); err != nil {
				return false
			}
			ref := metav1.GetControllerOfNoCopy(repl)
			return ref != nil && ref.UID == set.UID
		}, verifyTimeout, verifyInterval).Should(BeTrue(),
			"contract: after the leftover is collected, the set must create a child it owns")
	})

	It("P2: an orphaned child RBG carrying the set label is adopted by the recreated set", func() {
		setName := "verify-set-orphan"
		childKey := types.NamespacedName{Name: setName + "-0", Namespace: testNs}
		setKey := types.NamespacedName{Name: setName, Namespace: testNs}

		role := wrappersv2.BuildStandaloneRole("worker").Obj()
		set := &workloadsv1alpha2.RoleBasedGroupSet{
			ObjectMeta: metav1.ObjectMeta{Name: setName, Namespace: testNs},
			Spec: workloadsv1alpha2.RoleBasedGroupSetSpec{
				Replicas: ptr.To(int32(1)),
				GroupTemplate: workloadsv1alpha2.RoleBasedGroupTemplateSpec{
					Spec: workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{role}},
				},
			},
		}
		Expect(testutil.K8sClient.Create(testutil.Ctx, set)).To(Succeed())

		child := &workloadsv1alpha2.RoleBasedGroup{}
		Eventually(func() error {
			return testutil.K8sClient.Get(testutil.Ctx, childKey, child)
		}, verifyTimeout, verifyInterval).Should(Succeed())

		By("orphaning the child (what --cascade=orphan + GC would leave behind)")
		child.OwnerReferences = nil
		Expect(testutil.K8sClient.Update(testutil.Ctx, child)).To(Succeed())

		By("nudging the set to force a reconcile (orphan events do not enqueue the set)")
		fresh := &workloadsv1alpha2.RoleBasedGroupSet{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, setKey, fresh)).To(Succeed())
		if fresh.Annotations == nil {
			fresh.Annotations = map[string]string{}
		}
		fresh.Annotations["verify.poke"] = "1"
		Expect(testutil.K8sClient.Update(testutil.Ctx, fresh)).To(Succeed())

		Eventually(func() bool {
			repl := &workloadsv1alpha2.RoleBasedGroup{}
			if err := testutil.K8sClient.Get(testutil.Ctx, childKey, repl); err != nil {
				return false
			}
			return metav1.IsControlledBy(repl, fresh)
		}, verifyTimeout, verifyInterval).Should(BeTrue(),
			"contract: an orphan carrying the set label must be adopted")
	})

	It("P0/P3: a leftover scaling adapter of a same-named predecessor is replaced, not honored", func() {
		rbgName := "verify-rbg"
		role := wrappersv2.BuildStandaloneRole("worker").Obj()
		role.ScalingAdapter = &workloadsv1alpha2.ScalingAdapter{Enable: true}
		mkRBG := func() *workloadsv1alpha2.RoleBasedGroup {
			return &workloadsv1alpha2.RoleBasedGroup{
				ObjectMeta: metav1.ObjectMeta{Name: rbgName, Namespace: testNs},
				Spec:       workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{role}},
			}
		}
		rbgKey := types.NamespacedName{Name: rbgName, Namespace: testNs}
		adapterKey := types.NamespacedName{Name: scale.GenerateScalingAdapterName(rbgName, "worker"), Namespace: testNs}

		By("creating the first RBG and waiting for its adapter")
		Expect(testutil.K8sClient.Create(testutil.Ctx, mkRBG())).To(Succeed())
		adapter := &workloadsv1alpha2.RoleBasedGroupScalingAdapter{}
		Eventually(func() error {
			return testutil.K8sClient.Get(testutil.Ctx, adapterKey, adapter)
		}, verifyTimeout, verifyInterval).Should(Succeed())
		var oldUID types.UID
		for _, ref := range adapter.OwnerReferences {
			if ref.Name == rbgName {
				oldUID = ref.UID
			}
		}
		Expect(oldUID).NotTo(BeEmpty())

		By("deleting the RBG; without GC the adapter lingers with the old UID")
		rbg := &workloadsv1alpha2.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, rbgKey, rbg)).To(Succeed())
		Expect(testutil.K8sClient.Delete(testutil.Ctx, rbg)).To(Succeed())
		Eventually(func() bool {
			return apierrors.IsNotFound(testutil.K8sClient.Get(testutil.Ctx, rbgKey, &workloadsv1alpha2.RoleBasedGroup{}))
		}, verifyTimeout, verifyInterval).Should(BeTrue())

		By("recreating the same-named RBG; the stale adapter must be replaced with one owned by the new UID")
		newRBG := mkRBG()
		Expect(testutil.K8sClient.Create(testutil.Ctx, newRBG)).To(Succeed())

		// The predecessor's RoleInstanceSet also lingers (envtest has no GC), and the
		// RBG controller (#487) refuses to claim a foreign-UID workload, which blocks
		// the role reconcile — and with it ReconcileScalingAdapter — until the
		// leftover is gone. Simulate the garbage collector removing it.
		By("simulating GC of the predecessor's leftover workload")
		risList := &workloadsv1alpha2.RoleInstanceSetList{}
		Expect(testutil.K8sClient.List(testutil.Ctx, risList, client.InNamespace(testNs))).To(Succeed())
		for i := range risList.Items {
			if ref := metav1.GetControllerOfNoCopy(&risList.Items[i]); ref != nil && ref.UID == oldUID {
				Expect(testutil.K8sClient.Delete(testutil.Ctx, &risList.Items[i])).To(Succeed())
			}
		}

		// ControllerRevisions are filtered by owner UID in getCurrentRevision, so the
		// leftover revision-1 with the same deterministic name is invisible to the new
		// RBG's list but collides on Create (AlreadyExists) until GC removes it.
		// Simulate that too.
		By("simulating GC of the predecessor's leftover controller revisions")
		revList := &appsv1.ControllerRevisionList{}
		Expect(testutil.K8sClient.List(testutil.Ctx, revList, client.InNamespace(testNs))).To(Succeed())
		for i := range revList.Items {
			if ref := metav1.GetControllerOfNoCopy(&revList.Items[i]); ref != nil && ref.UID == oldUID {
				Expect(testutil.K8sClient.Delete(testutil.Ctx, &revList.Items[i])).To(Succeed())
			}
		}
		Eventually(func() bool {
			fresh := &workloadsv1alpha2.RoleBasedGroup{}
			if err := testutil.K8sClient.Get(testutil.Ctx, rbgKey, fresh); err != nil {
				return false
			}
			a := &workloadsv1alpha2.RoleBasedGroupScalingAdapter{}
			if err := testutil.K8sClient.Get(testutil.Ctx, adapterKey, a); err != nil {
				return false
			}
			for _, ref := range a.OwnerReferences {
				if ref.Name == rbgName {
					return ref.UID == fresh.UID && ref.UID != oldUID
				}
			}
			return false
		}, verifyTimeout, verifyInterval).Should(BeTrue(),
			"contract: the recreated RBG must replace the predecessor's adapter with one carrying its own UID")
	})

	It("F8: the API server enforces the UID delete precondition used by the adapter replace path", func() {
		// The fake client cannot enforce UID preconditions; this spec proves the
		// real API server does, using the exact client.DeleteOptions shape the
		// controller uses at rolebasedgroup_controller.go (replace path).
		// Combined with the unit layer
		// (TestVerifyF8AdapterReplaceDeletePreconditionReachesWire, which proves the
		// controller puts the observed UID into the wire body), this closes the
		// loop: controller sends precondition -> it reaches the wire -> the API
		// server rejects a stale-UID delete instead of wiping the live object.
		rbgName := "verify-f8-rbg"
		role := wrappersv2.BuildStandaloneRole("worker").Obj()
		role.ScalingAdapter = &workloadsv1alpha2.ScalingAdapter{Enable: true}
		rbg := &workloadsv1alpha2.RoleBasedGroup{
			ObjectMeta: metav1.ObjectMeta{Name: rbgName, Namespace: testNs},
			Spec:       workloadsv1alpha2.RoleBasedGroupSpec{Roles: []workloadsv1alpha2.RoleSpec{role}},
		}
		adapterKey := types.NamespacedName{Name: scale.GenerateScalingAdapterName(rbgName, "worker"), Namespace: testNs}

		By("creating the RBG and waiting for its adapter")
		Expect(testutil.K8sClient.Create(testutil.Ctx, rbg)).To(Succeed())
		adapter := &workloadsv1alpha2.RoleBasedGroupScalingAdapter{}
		Eventually(func() error {
			return testutil.K8sClient.Get(testutil.Ctx, adapterKey, adapter)
		}, verifyTimeout, verifyInterval).Should(Succeed())
		observedUID := adapter.UID

		By("deleting with a WRONG UID precondition: the API server must reject and the adapter must survive")
		wrongUID := types.UID("00000000-0000-0000-0000-000000000000")
		err := testutil.K8sClient.Delete(testutil.Ctx, adapter, &client.DeleteOptions{
			Preconditions: &metav1.Preconditions{UID: &wrongUID},
		})
		Expect(err).To(HaveOccurred(), "a UID-mismatched preconditioned delete must be rejected")
		Expect(apierrors.IsConflict(err)).To(BeTrue(), "expected a 409 Conflict, got: %v", err)
		survived := &workloadsv1alpha2.RoleBasedGroupScalingAdapter{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, adapterKey, survived)).To(Succeed())

		By("stale-cache simulation: the observed object is replaced under the same name (fresh UID)")
		Expect(testutil.K8sClient.Delete(testutil.Ctx, survived)).To(Succeed())
		liveRBG := &workloadsv1alpha2.RoleBasedGroup{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, types.NamespacedName{Name: rbgName, Namespace: testNs}, liveRBG)).To(Succeed())
		var liveUID types.UID
		Eventually(func() bool {
			cur := &workloadsv1alpha2.RoleBasedGroupScalingAdapter{}
			err := testutil.K8sClient.Get(testutil.Ctx, adapterKey, cur)
			if apierrors.IsNotFound(err) {
				// Recreate it ourselves (owned by the live RBG, so the controller
				// treats it as managed and leaves it alone) if the controller has
				// not recreated it yet; either way a fresh UID appears.
				repl := &workloadsv1alpha2.RoleBasedGroupScalingAdapter{
					ObjectMeta: metav1.ObjectMeta{
						Name:      adapterKey.Name,
						Namespace: testNs,
						OwnerReferences: []metav1.OwnerReference{
							*metav1.NewControllerRef(liveRBG, workloadsv1alpha2.GroupVersion.WithKind("RoleBasedGroup")),
						},
					},
					Spec: workloadsv1alpha2.RoleBasedGroupScalingAdapterSpec{
						ScaleTargetRef: &workloadsv1alpha2.AdapterScaleTargetRef{Name: rbgName, Role: "worker"},
					},
				}
				_ = testutil.K8sClient.Create(testutil.Ctx, repl)
				return false
			}
			if err != nil || cur.UID == observedUID {
				return false
			}
			liveUID = cur.UID
			return true
		}, verifyTimeout, verifyInterval).Should(BeTrue(), "a replacement adapter with a fresh UID must exist")

		By("deleting with the STALE observed UID: must be rejected, and the live replacement must survive")
		stale := observedUID
		replacement := &workloadsv1alpha2.RoleBasedGroupScalingAdapter{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, adapterKey, replacement)).To(Succeed())
		err = testutil.K8sClient.Delete(testutil.Ctx, replacement, &client.DeleteOptions{
			Preconditions: &metav1.Preconditions{UID: &stale},
		})
		Expect(err).To(HaveOccurred(), "a stale-UID preconditioned delete must be rejected")
		Expect(apierrors.IsConflict(err)).To(BeTrue(), "expected a 409 Conflict, got: %v", err)
		check := &workloadsv1alpha2.RoleBasedGroupScalingAdapter{}
		Expect(testutil.K8sClient.Get(testutil.Ctx, adapterKey, check)).To(Succeed(),
			"the live replacement adapter must survive the rejected stale delete")
		Expect(check.UID).To(Equal(liveUID))

		By("deleting with the CURRENT UID precondition: must succeed")
		Expect(testutil.K8sClient.Delete(testutil.Ctx, check, &client.DeleteOptions{
			Preconditions: &metav1.Preconditions{UID: &check.UID},
		})).To(Succeed())
	})

})
