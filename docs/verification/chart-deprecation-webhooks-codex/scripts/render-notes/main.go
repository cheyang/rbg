// render-notes renders the chart's NOTES.txt in the toggle x release-mode
// matrix via Helm's own template engine, so template branches in NOTES.txt are
// exercised the same way helm install/upgrade renders them.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
)

func render(chartPath string, isInstall bool, toggleSet map[string]interface{}) (string, error) {
	ch, err := loader.Load(chartPath)
	if err != nil {
		return "", err
	}
	vals, err := chartutil.ToRenderValues(ch, map[string]interface{}{"controller": toggleSet},
		chartutil.ReleaseOptions{Name: "rbgs", Namespace: "rbgs-system", IsInstall: isInstall, IsUpgrade: !isInstall},
		chartutil.DefaultCapabilities)
	if err != nil {
		return "", err
	}
	rendered, err := engine.Render(ch, vals)
	if err != nil {
		return "", err
	}
	return rendered["rbgs/templates/NOTES.txt"], nil
}

func main() {
	chartPath, _ := filepath.Abs(os.Args[1])
	type scenario struct {
		name      string
		isInstall bool
		toggle    map[string]interface{}
	}
	scenarios := []scenario{
		{"install/toggle-default(true)", true, map[string]interface{}{}},
		{"install/toggle-false", true, map[string]interface{}{"deprecatedWorkloadTypes": map[string]interface{}{"enabled": false}}},
		{"upgrade/toggle-default(true)", false, map[string]interface{}{}},
		{"upgrade/toggle-false", false, map[string]interface{}{"deprecatedWorkloadTypes": map[string]interface{}{"enabled": false}}},
	}
	rc := 0
	for _, s := range scenarios {
		out, err := render(chartPath, s.isInstall, s.toggle)
		if err != nil {
			fmt.Printf("== %s ==\nRENDER ERROR: %v\n", s.name, err)
			rc = 1
			continue
		}
		fmt.Printf("== %s ==\n%s\n", s.name, out)
	}
	os.Exit(rc)
}
