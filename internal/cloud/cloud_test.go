package cloud

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAndStage(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v4/groups/"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"id":42,"name":"dalcli-agent-coach","path":"dalcli-agent-coach","description":"coach","default_branch":"main","web_url":"https://example.test/dalcli-agent-coach","http_url_to_repo":"https://example.test/dalcli-agent-coach.git"}]`))
		case r.URL.Path == "/api/v4/projects/42/repository/archive.tar.gz":
			w.Header().Set("Content-Type", "application/gzip")
			gzw := gzip.NewWriter(w)
			tw := tar.NewWriter(gzw)
			writeTarFile(t, tw, "dalcli-agent-coach-main/.dalfactory/dal.cue", "name: \"coach\"\n")
			writeTarFile(t, tw, "dalcli-agent-coach-main/README.md", "# coach\n")
			if err := tw.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gzw.Close(); err != nil {
				t.Fatal(err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := New(Config{
		BaseURL:   srv.URL,
		GroupPath: "dalforge-hub/dalcli",
	})

	dir := t.TempDir()
	staged, pkg, err := c.Stage(context.Background(), "agent-coach", dir)
	if err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	if pkg.Path != "dalcli-agent-coach" {
		t.Fatalf("pkg.Path = %q, want dalcli-agent-coach", pkg.Path)
	}
	if _, err := os.Stat(filepath.Join(staged, ".dalfactory", "dal.cue")); err != nil {
		t.Fatalf("staged manifest missing: %v", err)
	}
}

func TestResolveAmbiguous(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[
			{"id":1,"name":"dalcli-agent-coach","path":"dalcli-agent-coach","default_branch":"main"},
			{"id":2,"name":"dalcli-agent-coach-v2","path":"dalcli-agent-coach-v2","default_branch":"main"}
		]`))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, GroupPath: "dalforge-hub/dalcli"})
	if _, err := c.Resolve(context.Background(), "coach"); err == nil {
		t.Fatalf("Resolve() error = nil, want ambiguous error")
	}
}

func writeTarFile(t *testing.T, tw *tar.Writer, name, body string) {
	t.Helper()
	hdr := &tar.Header{
		Name: name,
		Mode: 0644,
		Size: int64(len(body)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
}
