/*
Verification harness for PR #571 (KEP-531 engine runtime profile injection).
Reviewer: codex. Additive test file — no production code changed.

F1 (contract): KEP-531 Compatibility Guarantee #1 — "Pre-existing references
are byte-for-byte stable": a reference that sets only profileName (plus the
legacy containers merge) renders identically to main. The oracle below is
main's pkg/discovery/sidecar_builder.go copied verbatim (renamed) from the
merge-base ea5e82ae, driven through a fake client exactly as main's
InjectSidecar drove it. The new path is ResolveEngineRuntimes +
InjectEngineRuntimeFragments + both patch passes, per position.
*/

package discovery

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	workloadsv1alpha2 "sigs.k8s.io/rbgs/api/workloads/v1alpha2"
	"sigs.k8s.io/rbgs/pkg/utils"
)

// ---- oracle: main's sidecar_builder.go verbatim (base ea5e82ae) ----

type oracleSidecarBuilder struct {
	rbg    *workloadsv1alpha2.RoleBasedGroup
	role   *workloadsv1alpha2.RoleSpec
	client client.Client
}

func (b *oracleSidecarBuilder) Build(ctx context.Context, podSpec *v1.PodTemplateSpec) error {
	logger := log.FromContext(ctx)

	curRole, err := b.rbg.GetRole(b.role.Name)
	if err != nil || curRole == nil {
		return err
	}

	if len(curRole.EngineRuntimes) == 0 {
		logger.V(1).Info("runtime is nil, skip inject sidecar")
		return nil
	}

	for _, runtime := range curRole.EngineRuntimes {
		if err := b.oracleInjectRuntime(ctx, podSpec, runtime); err != nil {
			return fmt.Errorf("reconcile engine runtime %s, error: %s", runtime.ProfileName, err.Error())
		}
	}

	return nil
}

func (b *oracleSidecarBuilder) oracleInjectRuntime(
	ctx context.Context, podSpec *v1.PodTemplateSpec,
	runtime workloadsv1alpha2.EngineRuntime,
) error {
	logger := log.FromContext(ctx)

	engineRuntime := &workloadsv1alpha2.ClusterEngineRuntimeProfile{}
	if err := b.client.Get(
		ctx, types.NamespacedName{
			Name: runtime.ProfileName,
		}, engineRuntime,
	); err != nil {
		return fmt.Errorf("get engine runtime %s, error: %s", runtime.ProfileName, err.Error())
	}

	engineRuntimeContainerNames := make([]string, 0)

	// inject initContainers
	for _, initContainer := range engineRuntime.Spec.InitContainers {
		engineRuntimeContainerNames = append(engineRuntimeContainerNames, initContainer.Name)
		found := false
		for _, ic := range podSpec.Spec.InitContainers {
			if ic.Name == initContainer.Name {
				found = true
				break
			}
		}
		if !found {
			podSpec.Spec.InitContainers = append(podSpec.Spec.InitContainers, initContainer)
		}
	}

	// inject containers
	for _, container := range engineRuntime.Spec.Containers {
		engineRuntimeContainerNames = append(engineRuntimeContainerNames, container.Name)
		found := false
		for _, c := range podSpec.Spec.Containers {
			if c.Name == container.Name {
				found = true
				break
			}
		}
		if !found {
			podSpec.Spec.Containers = append(podSpec.Spec.Containers, container)
		}
	}

	// inject volumes
	for _, vol := range engineRuntime.Spec.Volumes {
		found := false
		for _, oldV := range podSpec.Spec.Volumes {
			if oldV.Name == vol.Name {
				found = true
				break
			}
		}
		if !found {
			podSpec.Spec.Volumes = append(podSpec.Spec.Volumes, vol)
		}
	}

	// override container based on rbg cr
	for _, container := range runtime.Containers {

		if !utils.ContainsString(engineRuntimeContainerNames, container.Name) {
			logger.V(1).Info(
				fmt.Sprintf(
					"rbg runtime has container %s but not in clusterEngineRuntime, "+
						"skip override", container.Name,
				),
			)
			continue
		}

		for i, c := range podSpec.Spec.Containers {
			if c.Name == container.Name {
				if len(container.Env) > 0 {
					podSpec.Spec.Containers[i].Env = append(podSpec.Spec.Containers[i].Env, container.Env...)
				}
				if len(container.Args) > 0 {
					podSpec.Spec.Containers[i].Args = append(podSpec.Spec.Containers[i].Args, container.Args...)
				}
				break
			}
		}
	}

	return nil
}

// ---- new-path driver ----

// renderNewPosition runs the PR-head pipeline for one position the way
// pod_reconciler.ConstructPodTemplateSpecApplyConfiguration does: snapshot,
// fragments, profile-side patches, role-side patches.
func renderNewPosition(
	t *testing.T,
	plan *EngineRuntimePlan,
	position string,
	template v1.PodTemplateSpec,
) *v1.PodTemplateSpec {
	t.Helper()
	spec := template.DeepCopy()
	roleContainerNames := make(map[string]struct{}, len(spec.Spec.Containers))
	for _, c := range spec.Spec.Containers {
		roleContainerNames[c.Name] = struct{}{}
	}
	roleInitContainerNames := make(map[string]struct{}, len(spec.Spec.InitContainers))
	for _, c := range spec.Spec.InitContainers {
		roleInitContainerNames[c.Name] = struct{}{}
	}
	ctx := context.Background()
	if err := InjectEngineRuntimeFragments(
		ctx, spec, plan.FragmentApplications[position], plan.RoleName, nil, nil); err != nil {
		t.Fatalf("InjectEngineRuntimeFragments: %v", err)
	}
	profilePositionalInserts, err := ApplyEngineRuntimePatches(
		ctx, spec, roleContainerNames, roleInitContainerNames, plan.RoleName,
		plan.PatchApplications[position], plan, nil, nil)
	if err != nil {
		t.Fatalf("ApplyEngineRuntimePatches: %v", err)
	}
	if _, err := ApplyRoleEngineRuntimePatches(
		ctx, spec, roleContainerNames, roleInitContainerNames,
		plan.RolePatchApplications[position], plan.RoleName, profilePositionalInserts,
		nil, nil,
	); err != nil {
		t.Fatalf("ApplyRoleEngineRuntimePatches: %v", err)
	}
	return spec
}

// ---- fixtures ----

func verifyProfile(name string) *workloadsv1alpha2.ClusterEngineRuntimeProfile {
	return &workloadsv1alpha2.ClusterEngineRuntimeProfile{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: workloadsv1alpha2.ClusterEngineRuntimeProfileSpec{
			InitContainers: []v1.Container{{Name: name + "-init", Image: name + "-init:1"}},
			Containers:     []v1.Container{{Name: name + "-sidecar", Image: name + "-sidecar:1"}},
			Volumes: []v1.Volume{{
				Name:         name + "-vol",
				VolumeSource: v1.VolumeSource{EmptyDir: &v1.EmptyDirVolumeSource{}},
			}},
		},
	}
}

func verifyBaseTemplate() v1.PodTemplateSpec {
	return v1.PodTemplateSpec{Spec: v1.PodSpec{
		Containers: []v1.Container{{
			Name: "engine", Image: "sglang:1",
			Command: []string{"python"}, Args: []string{"-m", "sglang.launch_server"},
			Env: []v1.EnvVar{{Name: "PYTHONPATH", Value: "/usr/lib/python"}},
		}},
	}}
}

func verifyRBG(role *workloadsv1alpha2.RoleSpec) *workloadsv1alpha2.RoleBasedGroup {
	return &workloadsv1alpha2.RoleBasedGroup{
		ObjectMeta: metav1.ObjectMeta{Name: "verify", Namespace: "default"},
		Spec: workloadsv1alpha2.RoleBasedGroupSpec{
			Roles: []workloadsv1alpha2.RoleSpec{*role},
		},
	}
}

func verifyStandaloneRole(name string, runtimes ...workloadsv1alpha2.EngineRuntime) *workloadsv1alpha2.RoleSpec {
	tpl := verifyBaseTemplate()
	return &workloadsv1alpha2.RoleSpec{
		Name: name,
		Pattern: workloadsv1alpha2.Pattern{
			StandalonePattern: &workloadsv1alpha2.StandalonePattern{
				TemplateSource: workloadsv1alpha2.TemplateSource{Template: &tpl},
			},
		},
		EngineRuntimes: runtimes,
	}
}

func ccpRole(name string, components []string, runtimes ...workloadsv1alpha2.EngineRuntime) *workloadsv1alpha2.RoleSpec {
	comps := make([]workloadsv1alpha2.InstanceComponent, len(components))
	for i, c := range components {
		tpl := verifyBaseTemplate()
		comps[i] = workloadsv1alpha2.InstanceComponent{Name: c, Template: tpl}
	}
	return &workloadsv1alpha2.RoleSpec{
		Name: name,
		Pattern: workloadsv1alpha2.Pattern{
			CustomComponentsPattern: &workloadsv1alpha2.CustomComponentsPattern{Components: comps},
		},
		EngineRuntimes: runtimes,
	}
}

func lwpRole(name string, runtimes ...workloadsv1alpha2.EngineRuntime) *workloadsv1alpha2.RoleSpec {
	tpl := verifyBaseTemplate()
	return &workloadsv1alpha2.RoleSpec{
		Name: name,
		Pattern: workloadsv1alpha2.Pattern{
			LeaderWorkerPattern: &workloadsv1alpha2.LeaderWorkerPattern{
				TemplateSource: workloadsv1alpha2.TemplateSource{Template: &tpl},
			},
		},
		EngineRuntimes: runtimes,
	}
}

func TestVerifyPR571_Scenario1ByteForByte(t *testing.T) {
	scheme := testScheme(t)

	type expectation struct {
		name     string
		role     *workloadsv1alpha2.RoleSpec
		profiles []client.Object
		// positions to compare: position name -> whether main's chain ran the
		// sidecar injector at that position (false = main rendered untouched)
		positions map[string]bool
	}

	cases := []expectation{
		{
			name: "lwp leader default scope, profile + legacy env/args merge",
			role: lwpRole("lg", workloadsv1alpha2.EngineRuntime{
				ProfileName: "patio",
				Containers: []v1.Container{{
					Name: "patio-sidecar",
					Env:  []v1.EnvVar{{Name: "EXTRA", Value: "1"}},
					Args: []string{"--extra"},
				}},
			}),
			profiles:  []client.Object{verifyProfile("patio")},
			positions: map[string]bool{"leader": true, "worker": false},
		},
		{
			name: "lwp two profiles, cross-profile container name collision",
			role: lwpRole("lg",
				workloadsv1alpha2.EngineRuntime{ProfileName: "a"},
				workloadsv1alpha2.EngineRuntime{ProfileName: "b"}),
			profiles: []client.Object{
				func() *workloadsv1alpha2.ClusterEngineRuntimeProfile {
					p := verifyProfile("a")
					p.Spec.Containers = append(p.Spec.Containers, v1.Container{Name: "shared", Image: "a-shared:1"})
					return p
				}(),
				func() *workloadsv1alpha2.ClusterEngineRuntimeProfile {
					p := verifyProfile("b")
					p.Spec.Containers = append(p.Spec.Containers, v1.Container{Name: "shared", Image: "b-shared:1"})
					return p
				}(),
			},
			positions: map[string]bool{"leader": true, "worker": false},
		},
		{
			name: "lwp duplicate same-profile references (compatibility pair), merge applies twice",
			role: lwpRole("lg",
				workloadsv1alpha2.EngineRuntime{
					ProfileName: "patio",
					Containers:  []v1.Container{{Name: "patio-sidecar", Env: []v1.EnvVar{{Name: "E", Value: "1"}}}},
				},
				workloadsv1alpha2.EngineRuntime{
					ProfileName: "patio",
					Containers:  []v1.Container{{Name: "patio-sidecar", Env: []v1.EnvVar{{Name: "E", Value: "2"}}}},
				}),
			profiles:  []client.Object{verifyProfile("patio")},
			positions: map[string]bool{"leader": true, "worker": false},
		},
		{
			name: "lwp legacy override naming a profile init container is a silent no-op",
			role: lwpRole("lg", workloadsv1alpha2.EngineRuntime{
				ProfileName: "patio",
				Containers: []v1.Container{{
					Name: "patio-init",
					Env:  []v1.EnvVar{{Name: "IGNORED", Value: "1"}},
				}},
			}),
			profiles:  []client.Object{verifyProfile("patio")},
			positions: map[string]bool{"leader": true, "worker": false},
		},
		{
			name: "lwp legacy override naming a container the profile does not declare is skipped",
			role: lwpRole("lg", workloadsv1alpha2.EngineRuntime{
				ProfileName: "patio",
				Containers:  []v1.Container{{Name: "ghost", Env: []v1.EnvVar{{Name: "X", Value: "1"}}}},
			}),
			profiles:  []client.Object{verifyProfile("patio")},
			positions: map[string]bool{"leader": true, "worker": false},
		},
		{
			name: "standalone role, default scope keys by role name",
			role: verifyStandaloneRole("solo", workloadsv1alpha2.EngineRuntime{
				ProfileName: "patio",
				Containers:  []v1.Container{{Name: "patio-sidecar", Args: []string{"--x"}}},
			}),
			profiles:  []client.Object{verifyProfile("patio")},
			positions: map[string]bool{"solo": true},
		},
		{
			name: "customComponentsPattern: absent component injects every component (main default)",
			role: ccpRole("multi", []string{"prefill", "decode"},
				workloadsv1alpha2.EngineRuntime{ProfileName: "patio"}),
			profiles:  []client.Object{verifyProfile("patio")},
			positions: map[string]bool{"prefill": true, "decode": true},
		},
		{
			name: "customComponentsPattern: duplicate same-profile references (compatibility pair)",
			role: ccpRole("multi", []string{"prefill", "decode"},
				workloadsv1alpha2.EngineRuntime{ProfileName: "patio"},
				workloadsv1alpha2.EngineRuntime{ProfileName: "patio"}),
			profiles:  []client.Object{verifyProfile("patio")},
			positions: map[string]bool{"prefill": true, "decode": true},
		},
		{
			name:      "no engine runtimes at all renders the template untouched",
			role:      lwpRole("lg"),
			profiles:  nil,
			positions: map[string]bool{"leader": false, "worker": false},
		},
		{
			name: "role template pre-declares the profile's container and volume (role wins)",
			role: func() *workloadsv1alpha2.RoleSpec {
				r := lwpRole("lg", workloadsv1alpha2.EngineRuntime{ProfileName: "patio"})
				tpl := verifyBaseTemplate()
				tpl.Spec.Containers = append(tpl.Spec.Containers,
					v1.Container{Name: "patio-sidecar", Image: "role-own:1"})
				tpl.Spec.Volumes = append(tpl.Spec.Volumes, v1.Volume{
					Name:         "patio-vol",
					VolumeSource: v1.VolumeSource{HostPath: &v1.HostPathVolumeSource{Path: "/role"}},
				})
				r.LeaderWorkerPattern.Template = &tpl
				return r
			}(),
			profiles:  []client.Object{verifyProfile("patio")},
			positions: map[string]bool{"leader": true, "worker": false},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rbg := verifyRBG(tc.role)
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.profiles...).Build()
			ctx := context.Background()

			plan, err := ResolveEngineRuntimes(ctx, cl, nil, rbg, tc.role)
			if err != nil {
				t.Fatalf("ResolveEngineRuntimes: %v", err)
			}

			for position, mainInjected := range tc.positions {
				// oracle: main behavior at this position
				oracleSpec := verifyBaseTemplate()
				if mainInjected {
					oracle := &oracleSidecarBuilder{rbg: rbg, role: tc.role, client: cl}
					if err := oracle.Build(ctx, &oracleSpec); err != nil {
						t.Fatalf("oracle build: %v", err)
					}
				}
				// new path
				newSpec := renderNewPosition(t, plan, position, verifyBaseTemplate())

				if !reflect.DeepEqual(oracleSpec.Spec, newSpec.Spec) {
					t.Errorf("position %q: rendered pod spec differs from main\noracle: %+v\nnew:    %+v",
						position, oracleSpec.Spec, newSpec.Spec)
				}
			}
		})
	}
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := workloadsv1alpha2.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return scheme
}

// TestVerifyPR571_ByteForByteNegativeControl proves the differential
// comparison in TestVerifyPR571_Scenario1ByteForByte is not vacuous: when the
// fragment pass is dropped from the new path, the rendered spec MUST differ
// from the main oracle for a role with engine runtimes. If this ever passes
// while the positive cases pass, the comparison is broken.
func TestVerifyPR571_ByteForByteNegativeControl(t *testing.T) {
	scheme := testScheme(t)
	role := lwpRole("lg", workloadsv1alpha2.EngineRuntime{ProfileName: "patio"})
	rbg := verifyRBG(role)
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(verifyProfile("patio")).Build()
	ctx := context.Background()

	oracleSpec := verifyBaseTemplate()
	oracle := &oracleSidecarBuilder{rbg: rbg, role: role, client: cl}
	if err := oracle.Build(ctx, &oracleSpec); err != nil {
		t.Fatalf("oracle build: %v", err)
	}
	plan, err := ResolveEngineRuntimes(ctx, cl, nil, rbg, role)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// sabotaged new path: no fragments at all
	broken := &EngineRuntimePlan{
		FragmentApplications:  map[string][]EngineRuntimeFragment{},
		PatchApplications:     plan.PatchApplications,
		RolePatchApplications: plan.RolePatchApplications,
		Vocabulary:            plan.Vocabulary,
		RoleName:              plan.RoleName,
	}
	got := renderNewPosition(t, broken, "leader", verifyBaseTemplate())
	if reflect.DeepEqual(oracleSpec.Spec, got.Spec) {
		t.Fatal("negative control failed: dropping the fragment pass did not change the rendered spec")
	}
}
