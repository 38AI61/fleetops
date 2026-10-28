// Package cloud holds the static checks for the cloud profile files (P24).
// The docker build itself runs in validation and in CI, not in go test.
package cloud

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// secretKeyName matches an ARG or ENV key that could carry a secret (D-40).
var secretKeyName = regexp.MustCompile(`(?i)secret|password|token|salt|database|jwt|key`)

// dockerfileLines reads Dockerfile.render and drops blank lines and comments.
func dockerfileLines(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("Dockerfile.render")
	if err != nil {
		t.Fatalf("read Dockerfile.render: %v", err)
	}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		out = append(out, trim)
	}
	return out
}

// TestDockerfileShape (GF-349, GF-350): one port, the fleetops-cloud entrypoint,
// the four binaries and the minio copy, a non-root user, FANOUT_MODE=nats, no
// REDIS_URL, and no ARG or ENV that could carry a secret.
func TestDockerfileShape(t *testing.T) {
	lines := dockerfileLines(t)
	joined := strings.Join(lines, "\n")

	var exposes int
	for _, l := range lines {
		if strings.HasPrefix(l, "EXPOSE ") {
			exposes++
			if l != "EXPOSE 8000" {
				t.Errorf("EXPOSE line %q, want EXPOSE 8000", l)
			}
		}
	}
	if exposes != 1 {
		t.Errorf("%d EXPOSE lines, want exactly 1", exposes)
	}

	if !strings.Contains(joined, `ENTRYPOINT ["/usr/local/bin/fleetops-cloud"]`) {
		t.Error("ENTRYPOINT is not fleetops-cloud")
	}
	for _, bin := range []string{"fleetops-api", "fleetops-ingest", "fleetops-ws-hub", "fleetops-cloud"} {
		if !strings.Contains(joined, "-o /out/"+bin+" ") {
			t.Errorf("build stage does not build %s", bin)
		}
	}
	if !strings.Contains(joined, "FROM ${MINIO_IMAGE} AS minio") {
		t.Error("minio is not a named stage from the pinned image")
	}
	if !strings.Contains(joined, "COPY --from=minio /opt/bitnami/minio/bin/minio /usr/local/bin/minio") {
		t.Error("minio binary is not copied from the pinned image")
	}

	var user string
	for _, l := range lines {
		if strings.HasPrefix(l, "USER ") {
			user = strings.TrimPrefix(l, "USER ")
		}
	}
	if user == "" || user == "root" || user == "0" {
		t.Errorf("USER %q, want a non-root user", user)
	}

	if !strings.Contains(joined, "FANOUT_MODE=nats") {
		t.Error("ENV does not set FANOUT_MODE=nats")
	}
	if strings.Contains(joined, "REDIS_URL") {
		t.Error("Dockerfile mentions REDIS_URL; the cloud image has no Redis")
	}

	for _, l := range lines {
		fields := strings.Fields(l)
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "ARG":
			key, _, _ := strings.Cut(fields[1], "=")
			if secretKeyName.MatchString(key) {
				t.Errorf("ARG %s could carry a secret", key)
			}
		case "ENV":
			for _, kv := range fields[1:] {
				key, _, _ := strings.Cut(kv, "=")
				if secretKeyName.MatchString(key) {
					t.Errorf("ENV %s could carry a secret", key)
				}
			}
		}
	}
}

// TestDockerignoreExcludesLocalSecrets (GF-349): the build context never holds
// device keys or demo passwords.
func TestDockerignoreExcludesLocalSecrets(t *testing.T) {
	raw, err := os.ReadFile("../../.dockerignore")
	if err != nil {
		t.Fatalf("read .dockerignore: %v", err)
	}
	for _, want := range []string{"deploy/.local", "deploy/.env"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf(".dockerignore does not exclude %s", want)
		}
	}
}
