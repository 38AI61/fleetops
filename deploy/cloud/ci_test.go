package cloud

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// ciWorkflow is the part of ci.yml the shape test reads.
type ciWorkflow struct {
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]struct {
		Needs       yaml.Node         `yaml:"needs"`
		If          string            `yaml:"if"`
		Permissions map[string]string `yaml:"permissions"`
	} `yaml:"jobs"`
}

// needsOf returns the job names a job needs, for both the list and scalar forms.
func needsOf(n yaml.Node) []string {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Value == "" {
			return nil
		}
		return []string{n.Value}
	case yaml.SequenceNode:
		var out []string
		for _, c := range n.Content {
			out = append(out, c.Value)
		}
		return out
	}
	return nil
}

var secretRef = regexp.MustCompile(`secrets\.([A-Za-z_]+)`)

// TestCIWorkflowShape (GF-355): gates and e2e are unchanged; image and deploy run
// only on main after both; packages: write appears only on image; the only
// secrets are GITHUB_TOKEN and RENDER_DEPLOY_HOOK_URL; the image name is lowercase.
func TestCIWorkflowShape(t *testing.T) {
	raw, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatalf("read ci.yml: %v", err)
	}
	var wf ciWorkflow
	if err := yaml.Unmarshal(raw, &wf); err != nil {
		t.Fatalf("parse ci.yml: %v", err)
	}

	if wf.Permissions["contents"] != "read" || len(wf.Permissions) != 1 {
		t.Errorf("top-level permissions %v, want only contents: read", wf.Permissions)
	}
	for _, name := range []string{"gates", "e2e"} {
		if _, ok := wf.Jobs[name]; !ok {
			t.Errorf("job %s is missing", name)
		}
	}

	const mainOnly = "github.ref == 'refs/heads/main'"
	image, ok := wf.Jobs["image"]
	switch {
	case !ok:
		t.Fatal("job image is missing")
	case !slices.Equal(needsOf(image.Needs), []string{"gates", "e2e"}):
		t.Errorf("image needs %v, want [gates e2e]", needsOf(image.Needs))
	case !strings.Contains(image.If, mainOnly) || !strings.Contains(image.If, "push"):
		t.Errorf("image if %q must be a push to main", image.If)
	}

	deploy, ok := wf.Jobs["deploy"]
	switch {
	case !ok:
		t.Fatal("job deploy is missing")
	case !slices.Equal(needsOf(deploy.Needs), []string{"image"}):
		t.Errorf("deploy needs %v, want [image]", needsOf(deploy.Needs))
	case !strings.Contains(deploy.If, mainOnly):
		t.Errorf("deploy if %q must be guarded by main", deploy.If)
	}

	for name, job := range wf.Jobs {
		if job.Permissions["packages"] == "write" && name != "image" {
			t.Errorf("job %s grants packages: write; only image may", name)
		}
	}
	if wf.Jobs["image"].Permissions["packages"] != "write" {
		t.Error("image job does not grant packages: write")
	}

	seen := map[string]bool{}
	for _, m := range secretRef.FindAllStringSubmatch(string(raw), -1) {
		seen[m[1]] = true
	}
	for name := range seen {
		if name != "GITHUB_TOKEN" && name != "RENDER_DEPLOY_HOOK_URL" {
			t.Errorf("workflow reads secret %s; only GITHUB_TOKEN and RENDER_DEPLOY_HOOK_URL are allowed", name)
		}
	}

	if !strings.Contains(string(raw), "ghcr.io/38ai61/fleetops:") {
		t.Error("image name ghcr.io/38ai61/fleetops is missing")
	}
	if regexp.MustCompile(`ghcr\.io/[^\s:]*[A-Z]`).MatchString(string(raw)) {
		t.Error("GHCR image name has uppercase letters; GHCR requires lowercase")
	}
}
