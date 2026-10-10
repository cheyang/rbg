/*
Copyright 2026 The RBG Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package workloads

// Integration-layer verification (envtest) for PR #478 premise problem 3:
// the #466 item-scoped CEL rule ("customized action container image must not be
// empty") freezes status writes of already-stored objects on Kubernetes 1.31
// (this repo's CI target), which is the stated reason for dropping the rule.
//
// The A/B reproduces the upgrade path a user of #466 hits:
//   1. install the PR CRD (no CEL rule on containers),
//   2. store a Warmup whose customizedAction container image is blank (accepted),
//   3. upgrade the CRD to the #466 version (rule added),
//   4. write the object's status  -> per the claim this is REJECTED on 1.31,
//   5. downgrade the CRD back to the PR version,
//   6. write the same status      -> accepted.
//
// Runs against every envtest assets directory listed in VERIFY_478_ASSETS
// (default: 1.31.0 only). Skipped when KUBEBUILDER_ASSETS is not set.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"

	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
)

func verify478AssetsDirs(t *testing.T) []string {
	t.Helper()
	kb := os.Getenv("KUBEBUILDER_ASSETS")
	if kb == "" {
		t.Skip("KUBEBUILDER_ASSETS not set; skipping envtest verification")
	}
	if custom := os.Getenv("VERIFY_478_ASSETS"); custom != "" {
		var dirs []string
		for _, d := range filepath.SplitList(custom) {
			dirs = append(dirs, d)
		}
		return dirs
	}
	return []string{kb}
}

func verify478ReadCRD(t *testing.T, path string) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read CRD %s: %v", path, err)
	}
	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.UnmarshalStrict(raw, crd); err != nil {
		t.Fatalf("decode CRD %s: %v", path, err)
	}
	return crd
}

func verify478WarmupCRDName() string { return "rolebasedgroupwarmups.workloads.x-k8s.io" }

// swapCRDSchema updates the live CRD's openAPIV3Schema to the given CRD's schema,
// preserving name/metadata, and waits for the CRD to be re-established.
func verify478SwapCRDSchema(ctx context.Context, c client.Client, target *apiextensionsv1.CustomResourceDefinition) error {
	// The CRD's status (Established condition) is written concurrently by the API
	// server, so a plain get-then-update races; retry on conflict.
	var lastErr error
	for attempt := 0; attempt < 20; attempt++ {
		live := &apiextensionsv1.CustomResourceDefinition{}
		if err := c.Get(ctx, types.NamespacedName{Name: target.Name}, live); err != nil {
			return err
		}
		live.Spec.Versions = target.Spec.Versions
		if err := c.Update(ctx, live); err != nil {
			if apierrors.IsConflict(err) {
				lastErr = err
				time.Sleep(500 * time.Millisecond)
				continue
			}
			return fmt.Errorf("update CRD: %w", err)
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		return fmt.Errorf("update CRD (conflicts): %w", lastErr)
	}
	// Wait for the CRD controller to reprocess (Established again).
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		check := &apiextensionsv1.CustomResourceDefinition{}
		if err := c.Get(ctx, types.NamespacedName{Name: target.Name}, check); err != nil {
			return err
		}
		for _, cond := range check.Status.Conditions {
			if cond.Type == apiextensionsv1.Established && cond.Status == apiextensionsv1.ConditionTrue {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("CRD %s never re-established", target.Name)
}

// statusWrite attempts exactly what the warmup controller does when it records
// phase/conditions: a subresource write on the Warmup.
func verify478StatusWrite(ctx context.Context, c client.Client, name string) error {
	obj := &workloadsv1alpha2.RoleBasedGroupWarmup{}
	if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, obj); err != nil {
		return err
	}
	orig := obj.DeepCopy()
	// Build the status exactly like the controller does (SetStatusCondition fills
	// lastTransitionTime; the counter fields have no omitempty and always serialize).
	obj.Status.Phase = workloadsv1alpha2.WarmupJobPhaseRunning
	apimeta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
		Type:               "TargetReady",
		Status:             metav1.ConditionFalse,
		Reason:             "RoleBasedGroupNotFound",
		Message:            "verify478",
		ObservedGeneration: obj.Generation,
	})
	_ = orig // full status Update, exactly like the controller (no omitempty on counters)
	return c.Status().Update(ctx, obj)
}

// verify478MainPathUpdate touches only metadata via the main resource path while
// the stored spec is invalid, to probe whether validation ratcheting exempts the
// unchanged field (claimed: yes on the main path in every version).
func verify478MainPathUpdate(ctx context.Context, c client.Client, name string) error {
	obj := &workloadsv1alpha2.RoleBasedGroupWarmup{}
	if err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, obj); err != nil {
		return err
	}
	if obj.Annotations == nil {
		obj.Annotations = map[string]string{}
	}
	obj.Annotations["verify478"] = "main-path"
	return c.Update(ctx, obj)
}

// ruleActive probes whether the CEL rule is enforced for new objects by trying
// to create a blank-image Warmup.
func verify478RuleActive(ctx context.Context, c client.Client) (bool, error) {
	obj := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "verify478-probe-", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupWarmupSpec{
			TargetNodes: &workloadsv1alpha2.TargetNodes{
				NodeNames: []string{"node-1"},
				WarmupActions: workloadsv1alpha2.WarmupActions{
					CustomizedAction: &workloadsv1alpha2.CustomizedAction{
						Containers: []corev1.Container{{Name: "c1", Image: ""}},
					},
				},
			},
		},
	}
	err := c.Create(ctx, obj)
	if err == nil {
		_ = c.Delete(ctx, obj)
		return false, nil
	}
	if apierrors.IsInvalid(err) {
		return true, nil
	}
	return false, err
}

func TestVerifyP0cCRDRuleFreezesStatusOn131(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	base := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))) // repo root

	prCRD := verify478ReadCRD(t, filepath.Join(base, "config", "crd", "bases", "workloads.x-k8s.io_rolebasedgroupwarmups.yaml"))
	withRuleCRD := verify478ReadCRD(t, filepath.Join(base, "docs", "verification", "warmup-target-wait-claude", "fixtures", "crd-with-cel-rule-466.yaml"))
	if prCRD.Name != withRuleCRD.Name || prCRD.Name != verify478WarmupCRDName() {
		t.Fatalf("CRD fixture mismatch: %q vs %q", prCRD.Name, withRuleCRD.Name)
	}

	scheme := k8sruntime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = workloadsv1alpha2.AddToScheme(scheme)
	_ = apiextensionsv1.AddToScheme(scheme)

	for _, assetsDir := range verify478AssetsDirs(t) {
		assetsDir := assetsDir
		t.Run(filepath.Base(assetsDir), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			env := &envtest.Environment{
				BinaryAssetsDirectory: assetsDir,
			}
			cfg, err := env.Start()
			if err != nil {
				t.Fatalf("start envtest (%s): %v", assetsDir, err)
			}
			defer func() { _ = env.Stop() }()

			c, err := client.New(cfg, client.Options{Scheme: scheme})
			if err != nil {
				t.Fatalf("client: %v", err)
			}

			// 1. Install the PR CRD (no container-image CEL rule).
			if err := c.Create(ctx, prCRD.DeepCopy()); err != nil {
				t.Fatalf("install PR CRD: %v", err)
			}
			if err := verify478SwapCRDSchema(ctx, c, prCRD); err != nil {
				t.Fatalf("PR CRD never established: %v", err)
			}

			// 2. Store a violating object (accepted without the rule).
			warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
				ObjectMeta: metav1.ObjectMeta{Name: "verify478-frozen", Namespace: "default"},
				Spec: workloadsv1alpha2.RoleBasedGroupWarmupSpec{
					TargetNodes: &workloadsv1alpha2.TargetNodes{
						NodeNames: []string{"node-1"},
						WarmupActions: workloadsv1alpha2.WarmupActions{
							CustomizedAction: &workloadsv1alpha2.CustomizedAction{
								Containers: []corev1.Container{{Name: "c1", Image: ""}},
							},
						},
					},
				},
			}
			if err := c.Create(ctx, warmup); err != nil {
				t.Fatalf("create blank-image Warmup under PR CRD (should be accepted): %v", err)
			}

			// 3. Upgrade the CRD to the #466 version (adds the rule).
			if err := verify478SwapCRDSchema(ctx, c, withRuleCRD); err != nil {
				t.Fatalf("upgrade CRD to #466 rule: %v", err)
			}
			// Wait until the rule is actually enforced for new objects.
			deadline := time.Now().Add(90 * time.Second)
			active := false
			for time.Now().Before(deadline) {
				active, err = verify478RuleActive(ctx, c)
				if err != nil {
					t.Fatalf("probe rule activity: %v", err)
				}
				if active {
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
			if !active {
				t.Fatal("#466 CEL rule never became active after CRD upgrade; cannot verify the freeze claim")
			}

			// 4a. Control: main-resource-path write with unchanged (invalid) spec —
			// validation ratcheting (1.27+) should exempt the unchanged field.
			errMainPath := verify478MainPathUpdate(ctx, c, warmup.Name)

			// 4b. Write status on the pre-existing violating object.
			errWithRule := verify478StatusWrite(ctx, c, warmup.Name)

			// 5. Swap the CRD back to the PR version.
			if err := verify478SwapCRDSchema(ctx, c, prCRD); err != nil {
				t.Fatalf("restore PR CRD: %v", err)
			}
			deadline = time.Now().Add(90 * time.Second)
			for time.Now().Before(deadline) {
				active, err = verify478RuleActive(ctx, c)
				if err != nil {
					t.Fatalf("probe rule activity: %v", err)
				}
				if !active {
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
			if active {
				t.Fatal("CEL rule still active after restoring the PR CRD")
			}

			// 6. Same status write must now succeed.
			errWithoutRule := verify478StatusWrite(ctx, c, warmup.Name)

			t.Logf("A/B result: main-path update with #466 rule (ratchet control): %v", errMainPath)
			t.Logf("A/B result: status write with #466 rule: %v", errWithRule)
			t.Logf("A/B result: status write with PR CRD:  %v", errWithoutRule)

			if errWithoutRule != nil {
				t.Errorf("with the PR CRD the controller must be able to write status, got: %v", errWithoutRule)
			}
			version := filepath.Base(filepath.Dir(assetsDir))
			if errWithRule == nil {
				t.Logf("NOTE: on %s the #466 rule did NOT block the status write (ratcheting may apply here)", version)
				return
			}
			if !apierrors.IsInvalid(errWithRule) {
				t.Errorf("with the #466 rule the status write should fail as Invalid (the freeze), got: %v", errWithRule)
			}
		})
	}
}

// TestVerifyP0dImagesMinLengthRatchetClaim checks the PR-body claim that the kept
// `minLength: 1` marker on imagePreload.images items is "ratcheting-safe (a
// pre-existing object with images: [\"\"] keeps a writable status)". Same A/B
// shape as the CEL test: store the violating object under a pre-#466-style CRD
// (no minLength on images), upgrade to the PR CRD (minLength present), then
// write status.
func TestVerifyP0dImagesMinLengthRatchetClaim(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	base := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))) // repo root

	prCRD := verify478ReadCRD(t, filepath.Join(base, "config", "crd", "bases", "workloads.x-k8s.io_rolebasedgroupwarmups.yaml"))
	noMinLenCRD := verify478ReadCRD(t, filepath.Join(base, "docs", "verification", "warmup-target-wait-claude", "fixtures", "crd-pre466-no-minlength.yaml"))

	scheme := k8sruntime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = workloadsv1alpha2.AddToScheme(scheme)
	_ = apiextensionsv1.AddToScheme(scheme)

	for _, assetsDir := range verify478AssetsDirs(t) {
		assetsDir := assetsDir
		t.Run(filepath.Base(assetsDir), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			env := &envtest.Environment{BinaryAssetsDirectory: assetsDir}
			cfg, err := env.Start()
			if err != nil {
				t.Fatalf("start envtest: %v", err)
			}
			defer func() { _ = env.Stop() }()

			c, err := client.New(cfg, client.Options{Scheme: scheme})
			if err != nil {
				t.Fatalf("client: %v", err)
			}

			if err := c.Create(ctx, noMinLenCRD.DeepCopy()); err != nil {
				t.Fatalf("install no-minLength CRD: %v", err)
			}
			if err := verify478SwapCRDSchema(ctx, c, noMinLenCRD); err != nil {
				t.Fatalf("no-minLength CRD never established: %v", err)
			}

			// Store the violating object (accepted pre-#466).
			warmup := &workloadsv1alpha2.RoleBasedGroupWarmup{
				ObjectMeta: metav1.ObjectMeta{Name: "verify478-blankimage", Namespace: "default"},
				Spec: workloadsv1alpha2.RoleBasedGroupWarmupSpec{
					TargetNodes: &workloadsv1alpha2.TargetNodes{
						NodeNames: []string{"node-1"},
						WarmupActions: workloadsv1alpha2.WarmupActions{
							ImagePreload: &workloadsv1alpha2.ImagePreloadAction{Images: []string{""}},
						},
					},
				},
			}
			if err := c.Create(ctx, warmup); err != nil {
				t.Fatalf("create images:[\"\"] Warmup under pre-#466 CRD: %v", err)
			}

			// Upgrade to the PR CRD (minLength on images items present).
			if err := verify478SwapCRDSchema(ctx, c, prCRD); err != nil {
				t.Fatalf("upgrade CRD to PR version: %v", err)
			}
			deadline := time.Now().Add(90 * time.Second)
			for time.Now().Before(deadline) {
				active, err := verify478BlankImageCreateRejected(ctx, c)
				if err != nil {
					t.Fatalf("probe minLength activity: %v", err)
				}
				if active {
					break
				}
				time.Sleep(500 * time.Millisecond)
			}

			errMinLenStatus := verify478StatusWrite(ctx, c, warmup.Name)
			t.Logf("A/B result: images minLength status write: %v", errMinLenStatus)
			if errMinLenStatus != nil {
				t.Logf("NOTE: the kept images minLength ALSO blocks status writes of pre-existing "+
					"objects on %s — the PR's 'ratcheting-safe' claim does not hold on this version",
					filepath.Base(assetsDir))
			}
		})
	}
}

// verify478BlankImageCreateRejected probes whether creating a Warmup with a blank
// imagePreload image is rejected (i.e. the minLength rule is enforced).
func verify478BlankImageCreateRejected(ctx context.Context, c client.Client) (bool, error) {
	obj := &workloadsv1alpha2.RoleBasedGroupWarmup{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "verify478-probe-blank-", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupWarmupSpec{
			TargetNodes: &workloadsv1alpha2.TargetNodes{
				NodeNames: []string{"node-1"},
				WarmupActions: workloadsv1alpha2.WarmupActions{
					ImagePreload: &workloadsv1alpha2.ImagePreloadAction{Images: []string{""}},
				},
			},
		},
	}
	err := c.Create(ctx, obj)
	if err == nil {
		_ = c.Delete(ctx, obj)
		return false, nil
	}
	if apierrors.IsInvalid(err) {
		return true, nil
	}
	return false, err
}
