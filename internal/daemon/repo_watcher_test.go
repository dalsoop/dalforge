package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// initBareAndClone creates a bare repo and a working clone for testing.
// Returns (bareDir, cloneDir).
func initBareAndClone(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()

	bareDir := filepath.Join(base, "origin.git")
	cloneDir := filepath.Join(base, "clone")

	// Create bare repo
	run(t, bareDir, "git", "init", "--bare", bareDir)

	// Clone it
	run(t, base, "git", "clone", bareDir, cloneDir)

	// Initial commit with .dal/
	dalDir := filepath.Join(cloneDir, ".dal", "leader")
	os.MkdirAll(dalDir, 0755)
	os.WriteFile(filepath.Join(dalDir, "instructions.md"), []byte("# Leader v1\n"), 0644)
	run(t, cloneDir, "git", "add", ".")
	run(t, cloneDir, "git", "-c", "user.name=test", "-c", "user.email=test@test", "commit", "-m", "init")
	run(t, cloneDir, "git", "push")

	return bareDir, cloneDir
}

// pushToBare makes a commit directly in a temp clone and pushes to bare.
func pushToBare(t *testing.T, bareDir string, file, content string) {
	t.Helper()
	tmp := t.TempDir()
	run(t, tmp, "git", "clone", bareDir, tmp+"/push")
	pushDir := tmp + "/push"

	dir := filepath.Dir(filepath.Join(pushDir, file))
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(pushDir, file), []byte(content), 0644)
	run(t, pushDir, "git", "add", ".")
	run(t, pushDir, "git", "-c", "user.name=test", "-c", "user.email=test@test", "commit", "-m", "update")
	run(t, pushDir, "git", "push")
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if dir != "" {
		os.MkdirAll(dir, 0755)
	}
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}

func TestIsGitRepo(t *testing.T) {
	t.Run("valid git repo", func(t *testing.T) {
		dir := t.TempDir()
		run(t, dir, "git", "init", dir)
		if !isGitRepo(dir) {
			t.Error("expected true for git repo")
		}
	})

	t.Run("not a git repo", func(t *testing.T) {
		dir := t.TempDir()
		if isGitRepo(dir) {
			t.Error("expected false for non-git dir")
		}
	})

	t.Run("nonexistent dir", func(t *testing.T) {
		if isGitRepo("/nonexistent/path/xyz") {
			t.Error("expected false for nonexistent dir")
		}
	})
}

func TestShort(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"abc1234567890", "abc1234"},
		{"abc", "abc"},
		{"", ""},
		{"1234567", "1234567"},
		{"12345678", "1234567"},
	}
	for _, tt := range tests {
		if got := short(tt.in); got != tt.want {
			t.Errorf("short(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGitRevParse(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "git", "init", dir)
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0644)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "-c", "user.name=test", "-c", "user.email=test@test", "commit", "-m", "init")

	hash := gitRevParse(dir, "HEAD")
	if len(hash) != 40 {
		t.Errorf("expected 40-char hash, got %q (len=%d)", hash, len(hash))
	}

	empty := gitRevParse(dir, "nonexistent-ref")
	if empty != "" {
		t.Errorf("expected empty for bad ref, got %q", empty)
	}

	empty2 := gitRevParse("/nonexistent", "HEAD")
	if empty2 != "" {
		t.Errorf("expected empty for bad dir, got %q", empty2)
	}
}

func TestFetchAndPull_NoRemoteChanges(t *testing.T) {
	_, cloneDir := initBareAndClone(t)

	changed := fetchAndPull(cloneDir)
	if changed {
		t.Error("expected no changes when repo is up to date")
	}
}

func TestFetchAndPull_DalChanged(t *testing.T) {
	bareDir, cloneDir := initBareAndClone(t)

	// Push a .dal/ change via another clone
	pushToBare(t, bareDir, ".dal/leader/instructions.md", "# Leader v2 — updated\n")

	changed := fetchAndPull(cloneDir)
	if !changed {
		t.Error("expected changes detected after .dal/ update")
	}

	// Verify file was pulled
	content, _ := os.ReadFile(filepath.Join(cloneDir, ".dal", "leader", "instructions.md"))
	if string(content) != "# Leader v2 — updated\n" {
		t.Errorf("file not updated after pull: %q", string(content))
	}
}

func TestFetchAndPull_NonDalChanged(t *testing.T) {
	bareDir, cloneDir := initBareAndClone(t)

	// Push a change outside .dal/
	pushToBare(t, bareDir, "README.md", "# Updated readme\n")

	changed := fetchAndPull(cloneDir)
	if changed {
		t.Error("expected no sync trigger when .dal/ not affected")
	}

	// Verify the pull still happened
	content, _ := os.ReadFile(filepath.Join(cloneDir, "README.md"))
	if string(content) != "# Updated readme\n" {
		t.Errorf("non-dal file not pulled: %q", string(content))
	}
}

func TestFetchAndPull_NotGitRepo(t *testing.T) {
	dir := t.TempDir()
	changed := fetchAndPull(dir)
	if changed {
		t.Error("expected false for non-git dir")
	}
}

func TestFetchAndPull_MultipleCommits(t *testing.T) {
	bareDir, cloneDir := initBareAndClone(t)

	// Push two commits: one non-dal, one dal
	tmp := t.TempDir()
	run(t, tmp, "git", "clone", bareDir, tmp+"/push")
	pushDir := tmp + "/push"

	os.WriteFile(filepath.Join(pushDir, "README.md"), []byte("readme\n"), 0644)
	run(t, pushDir, "git", "add", ".")
	run(t, pushDir, "git", "-c", "user.name=test", "-c", "user.email=test@test", "commit", "-m", "readme")

	os.MkdirAll(filepath.Join(pushDir, ".dal", "dev"), 0755)
	os.WriteFile(filepath.Join(pushDir, ".dal", "dev", "instructions.md"), []byte("# Dev\n"), 0644)
	run(t, pushDir, "git", "add", ".")
	run(t, pushDir, "git", "-c", "user.name=test", "-c", "user.email=test@test", "commit", "-m", "add dev dal")
	run(t, pushDir, "git", "push")

	changed := fetchAndPull(cloneDir)
	if !changed {
		t.Error("expected .dal/ change detected across multiple commits")
	}
}

func TestFetchAndPull_Idempotent(t *testing.T) {
	bareDir, cloneDir := initBareAndClone(t)

	pushToBare(t, bareDir, ".dal/leader/instructions.md", "# Leader v2\n")

	// First call: should detect change
	if !fetchAndPull(cloneDir) {
		t.Fatal("first call should detect changes")
	}

	// Second call: already up to date
	if fetchAndPull(cloneDir) {
		t.Error("second call should return false (already pulled)")
	}
}

func TestFetchAndPull_DalFileDeleted(t *testing.T) {
	bareDir, cloneDir := initBareAndClone(t)

	// Delete .dal/leader/instructions.md via another clone
	tmp := t.TempDir()
	run(t, tmp, "git", "clone", bareDir, tmp+"/push")
	pushDir := tmp + "/push"
	os.Remove(filepath.Join(pushDir, ".dal", "leader", "instructions.md"))
	run(t, pushDir, "git", "add", "-A")
	run(t, pushDir, "git", "-c", "user.name=test", "-c", "user.email=test@test", "commit", "-m", "delete instructions")
	run(t, pushDir, "git", "push")

	changed := fetchAndPull(cloneDir)
	if !changed {
		t.Error("expected change detected when .dal/ file deleted")
	}

	// Verify file is gone
	if _, err := os.Stat(filepath.Join(cloneDir, ".dal", "leader", "instructions.md")); !os.IsNotExist(err) {
		t.Error("file should be deleted after pull")
	}
}

func TestFetchAndPull_SkillsChanged(t *testing.T) {
	bareDir, cloneDir := initBareAndClone(t)

	pushToBare(t, bareDir, ".dal/skills/go-review/SKILL.md", "# Go Review Skill\nReview Go code.\n")

	changed := fetchAndPull(cloneDir)
	if !changed {
		t.Error("expected change detected for .dal/skills/ update")
	}

	content, _ := os.ReadFile(filepath.Join(cloneDir, ".dal", "skills", "go-review", "SKILL.md"))
	if string(content) != "# Go Review Skill\nReview Go code.\n" {
		t.Errorf("skill file not updated: %q", string(content))
	}
}

func TestFetchAndPull_DalCueChanged(t *testing.T) {
	bareDir, cloneDir := initBareAndClone(t)

	pushToBare(t, bareDir, ".dal/leader/dal.cue", "uuid: \"leader-v2\"\nname: \"leader\"\n")

	changed := fetchAndPull(cloneDir)
	if !changed {
		t.Error("expected change detected for dal.cue update")
	}
}

func TestFetchAndPull_LocalDirtyFails(t *testing.T) {
	bareDir, cloneDir := initBareAndClone(t)

	// Make a local commit on main that diverges from remote
	os.WriteFile(filepath.Join(cloneDir, "local-only.txt"), []byte("local\n"), 0644)
	run(t, cloneDir, "git", "add", ".")
	run(t, cloneDir, "git", "-c", "user.name=test", "-c", "user.email=test@test", "commit", "-m", "local only")

	// Push a different change to remote
	pushToBare(t, bareDir, "remote-only.txt", "remote\n")

	// ff-only should fail (diverged history)
	changed := fetchAndPull(cloneDir)
	if changed {
		t.Error("expected false when ff-only fails due to diverged history")
	}
}

func TestFetchAndPull_MixedDalAndNonDal(t *testing.T) {
	bareDir, cloneDir := initBareAndClone(t)

	// Push a commit that touches both .dal/ and non-.dal/ files
	tmp := t.TempDir()
	run(t, tmp, "git", "clone", bareDir, tmp+"/push")
	pushDir := tmp + "/push"

	os.WriteFile(filepath.Join(pushDir, "README.md"), []byte("updated\n"), 0644)
	os.MkdirAll(filepath.Join(pushDir, ".dal", "dev"), 0755)
	os.WriteFile(filepath.Join(pushDir, ".dal", "dev", "instructions.md"), []byte("# Dev\n"), 0644)
	run(t, pushDir, "git", "add", ".")
	run(t, pushDir, "git", "-c", "user.name=test", "-c", "user.email=test@test", "commit", "-m", "mixed")
	run(t, pushDir, "git", "push")

	changed := fetchAndPull(cloneDir)
	if !changed {
		t.Error("expected change when commit touches both .dal/ and other files")
	}
}

func TestIsGitRepo_BareRepo(t *testing.T) {
	dir := t.TempDir()
	bareDir := filepath.Join(dir, "bare.git")
	run(t, dir, "git", "init", "--bare", bareDir)
	if !isGitRepo(bareDir) {
		t.Error("expected true for bare git repo")
	}
}

func TestGitRevParse_MultipleCommits(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "git", "init", dir)

	// Commit 1
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0644)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "-c", "user.name=test", "-c", "user.email=test@test", "commit", "-m", "first")
	hash1 := gitRevParse(dir, "HEAD")

	// Commit 2
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0644)
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "-c", "user.name=test", "-c", "user.email=test@test", "commit", "-m", "second")
	hash2 := gitRevParse(dir, "HEAD")

	if hash1 == hash2 {
		t.Error("different commits should have different hashes")
	}
	if len(hash1) != 40 || len(hash2) != 40 {
		t.Errorf("hashes should be 40 chars: %q, %q", hash1, hash2)
	}

	// HEAD~1 should match first commit
	parent := gitRevParse(dir, "HEAD~1")
	if parent != hash1 {
		t.Errorf("HEAD~1 = %q, want %q", parent, hash1)
	}
}

func TestRunSync_EmptyContainers(t *testing.T) {
	d, _ := setupTestDaemon(t)
	synced, restarted := d.runSync()
	if synced != nil || restarted != nil {
		t.Errorf("expected nil for empty containers, got synced=%v restarted=%v", synced, restarted)
	}
}

func TestHandleSync_NilWriter(t *testing.T) {
	d, _ := setupTestDaemon(t)
	// Should not panic with nil writer
	d.handleSync(nil, nil)
}

func TestStartRepoWatcher_EmptyDir(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	var called atomic.Int32
	// Should return immediately for empty repoDir
	startRepoWatcher(ctx, "", func() { called.Add(1) })

	if called.Load() != 0 {
		t.Error("syncFn should not be called for empty dir")
	}
}

func TestStartRepoWatcher_NotGitRepo(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	var called atomic.Int32
	startRepoWatcher(ctx, t.TempDir(), func() { called.Add(1) })

	if called.Load() != 0 {
		t.Error("syncFn should not be called for non-git dir")
	}
}

func TestStartRepoWatcher_CancelStops(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "git", "init", dir)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		startRepoWatcher(ctx, dir, func() {})
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// OK — watcher stopped
	case <-time.After(3 * time.Second):
		t.Fatal("repo-watcher did not stop after context cancel")
	}
}
