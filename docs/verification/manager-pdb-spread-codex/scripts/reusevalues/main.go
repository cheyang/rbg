// reusevalues simulates `helm upgrade --reuse-values` value resolution for PR #589
// without a cluster, replicating the exact coalescing steps of helm v3.16.3's
// (*Upgrade).reuseValues (pkg/action/upgrade.go):
//
//	oldVals = CoalesceValues(oldChart, oldUserConfig)   // old chart defaults + old user values
//	newVals = CoalesceTables(newUserVals, oldUserConfig)
//	newChart.Values = oldVals                            // <-- old defaults replace new defaults
//	render with ToRenderValues(newChart, newVals, ...)
//
// The question: does an upgrader from the pre-PR chart with --reuse-values get the new
// controller.podDisruptionBudget / controller.topologySpreadConstraints defaults?
// Expected (author's claim in the PR body): no — the keys stay absent, the templates
// (`if $pdb.enabled`, `with ...`) render nothing.
package main

import (
	"fmt"
	"os"

	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
)

func dig(vals map[string]interface{}, path ...string) (interface{}, bool) {
	cur := vals
	for i, p := range path {
		v, ok := cur[p]
		if !ok {
			return nil, false
		}
		if i == len(path)-1 {
			return v, true
		}
		cur, ok = v.(map[string]interface{})
		if !ok {
			return nil, false
		}
	}
	return nil, false
}

func main() {
	oldChartDir, newChartDir := os.Args[1], os.Args[2]
	oldChart, err := loader.Load(oldChartDir)
	must(err)
	newChart, err := loader.Load(newChartDir)
	must(err)

	// A realistic old-install user config: anything the user set before this PR.
	oldUserConfig := map[string]interface{}{
		"controller": map[string]interface{}{
			"tolerations": []interface{}{map[string]interface{}{"operator": "Exists"}},
		},
	}

	report := func(label string, top map[string]interface{}) {
		vals := map[string]interface{}(top["Values"].(chartutil.Values))
		pdb, pdbOK := dig(vals, "controller", "podDisruptionBudget")
		_, tscOK := dig(vals, "controller", "topologySpreadConstraints")
		fmt.Printf("%-46s podDisruptionBudget=%v (present=%v)  topologySpreadConstraints present=%v\n",
			label, pdb, pdbOK, tscOK)
	}

	// Scenario A: helm upgrade (no --reuse-values) — new chart defaults coalesce in.
	vals, err := chartutil.ToRenderValues(newChart, map[string]interface{}{}, chartutil.ReleaseOptions{}, nil)
	must(err)
	report("A plain upgrade (new chart defaults)", vals)

	// Scenario B: helm upgrade --reuse-values — helm replaces newChart.Values with the
	// old chart's coalesced values before rendering.
	oldVals, err := chartutil.CoalesceValues(oldChart, oldUserConfig)
	must(err)
	newVals := chartutil.CoalesceTables(map[string]interface{}{}, oldUserConfig)
	newChart.Values = oldVals
	vals, err = chartutil.ToRenderValues(newChart, newVals, chartutil.ReleaseOptions{}, nil)
	must(err)
	report("B upgrade --reuse-values", vals)

	// Scenario C: --reuse-values plus an explicit opt-in.
	newChart2, err := loader.Load(newChartDir)
	must(err)
	newChart2.Values = oldVals
	optIn := map[string]interface{}{
		"controller": map[string]interface{}{
			"podDisruptionBudget": map[string]interface{}{"enabled": true},
		},
	}
	newVals = chartutil.CoalesceTables(optIn, oldUserConfig)
	vals, err = chartutil.ToRenderValues(newChart2, newVals, chartutil.ReleaseOptions{}, nil)
	must(err)
	report("C --reuse-values + enabled=true", vals)

	// Scenario D: fresh install / helm template — defaults present.
	newChart3, err := loader.Load(newChartDir)
	must(err)
	vals, err = chartutil.ToRenderValues(newChart3, map[string]interface{}{}, chartutil.ReleaseOptions{}, nil)
	must(err)
	report("D fresh install / helm template", vals)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
