package proofs_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

	"fleetops/internal/proofs"
	"fleetops/internal/proofs/prooftest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	prooftest.TerminateMinIO()
	os.Exit(code)
}

// jpegBytes is a stand-in payload. The store never inspects bytes; the handler sniffs.
var jpegBytes = []byte("\xff\xd8\xff\xe0proof-photo-bytes")

// TestProofsFromEnvRequired checks that each MINIO_* variable is required and that
// the error names the variable, never the value (GF-201).
func TestProofsFromEnvRequired(t *testing.T) {
	full := map[string]string{"MINIO_ENDPOINT": "127.0.0.1:1", "MINIO_KEY": "k-secret", "MINIO_SECRET": "s-secret"}
	for name := range full {
		t.Run(name, func(t *testing.T) {
			env := map[string]string{}
			for k, v := range full {
				env[k] = v
			}
			env[name] = ""
			_, err := proofs.FromEnv(func(k string) string { return env[k] })
			if err == nil {
				t.Fatalf("FromEnv with empty %s: want error", name)
			}
			if !strings.Contains(err.Error(), name) {
				t.Fatalf("error %q does not name %s", err, name)
			}
			for _, v := range full {
				if strings.Contains(err.Error(), v) {
					t.Fatalf("error %q leaks a value", err)
				}
			}
		})
	}
}

// TestProofsBucketIdempotent: EnsureBucket twice is a no-op the second time, and
// an object put after the first call is listable and headable.
func TestProofsBucketIdempotent(t *testing.T) {
	st := prooftest.SharedStore(t)
	ctx := context.Background()
	if err := st.EnsureBucket(ctx); err != nil {
		t.Fatalf("first EnsureBucket: %v", err)
	}
	if err := st.EnsureBucket(ctx); err != nil {
		t.Fatalf("second EnsureBucket: %v", err)
	}

	key, err := st.Put(ctx, 1, 2, bytes.NewReader(jpegBytes), int64(len(jpegBytes)), "jpg")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	c := prooftest.Client(t)
	found := false
	for obj := range c.ListObjects(ctx, proofs.Bucket, minio.ListObjectsOptions{Prefix: "proofs/1/2/", Recursive: true}) {
		if obj.Err != nil {
			t.Fatalf("list: %v", obj.Err)
		}
		if obj.Key == key {
			found = true
		}
	}
	if !found {
		t.Fatalf("object %s not listed", key)
	}
	info, err := c.StatObject(ctx, proofs.Bucket, key, minio.StatObjectOptions{})
	if err != nil {
		t.Fatalf("head %s: %v", key, err)
	}
	if info.Size != int64(len(jpegBytes)) {
		t.Fatalf("head size = %d, want %d", info.Size, len(jpegBytes))
	}
}

// keyRE is the D-25 object name: proofs/<fleet>/<job>/<uuid>.<ext>.
var keyRE = regexp.MustCompile(`^proofs/7/9/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.jpg$`)

// TestProofsPutPresignFetch: Put builds the exact D-25 key; PresignGet GET returns
// 200 with the exact bytes; GET on a missing key does not return 200.
func TestProofsPutPresignFetch(t *testing.T) {
	st := prooftest.SharedStore(t)
	ctx := context.Background()

	key, err := st.Put(ctx, 7, 9, bytes.NewReader(jpegBytes), int64(len(jpegBytes)), "jpg")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if !keyRE.MatchString(key) {
		t.Fatalf("key %q does not match proofs/<fleet>/<job>/<uuid>.jpg", key)
	}

	url, err := st.PresignGet(ctx, key)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	status, body := get(t, url)
	if status != http.StatusOK {
		t.Fatalf("presigned GET = %d, want 200", status)
	}
	if !bytes.Equal(body, jpegBytes) {
		t.Fatalf("presigned GET body = %q, want %q", body, jpegBytes)
	}

	missing, err := st.PresignGet(ctx, "proofs/7/9/does-not-exist.jpg")
	if err != nil {
		t.Fatalf("PresignGet missing: %v", err)
	}
	if status, _ := get(t, missing); status == http.StatusOK {
		t.Fatalf("GET on a missing key returned 200")
	}
}

// TestProofsPresignExpiry: a 2 s TTL URL returns 200, then 403 once it expires.
func TestProofsPresignExpiry(t *testing.T) {
	st := prooftest.SharedStore(t)
	st.PresignTTL = 2 * time.Second
	ctx := context.Background()

	key, err := st.Put(ctx, 7, 9, bytes.NewReader(jpegBytes), int64(len(jpegBytes)), "jpg")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	url, err := st.PresignGet(ctx, key)
	if err != nil {
		t.Fatalf("PresignGet: %v", err)
	}
	if status, _ := get(t, url); status != http.StatusOK {
		t.Fatalf("GET before expiry = %d, want 200", status)
	}

	deadline := time.Now().Add(10 * time.Second)
	for {
		status, _ := get(t, url)
		if status == http.StatusForbidden {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("presigned URL still %d after 10s, want 403", status)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// get performs one GET and returns the status and body.
func get(t *testing.T, url string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, b
}
