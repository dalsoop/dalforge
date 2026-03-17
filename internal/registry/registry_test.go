package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func tempRegistry(t *testing.T) *Registry {
	t.Helper()
	dir := t.TempDir()
	reg, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	t.Cleanup(func() { reg.Close() })
	return reg
}

func TestStatusExactDalID(t *testing.T) {
	reg := tempRegistry(t)
	inst, err := reg.Join("default", "local", "/repo/dalcli-agent-coach", "/repo/dalcli-agent-coach", "/repo/dalcli-agent-coach/.dalfactory/dal.cue", "", 2)
	if err != nil {
		t.Fatalf("join: %v", err)
	}

	result, err := reg.Status(inst.DalID)
	if err != nil {
		t.Fatalf("status by dal_id: %v", err)
	}
	if result.Instance.DalID != inst.DalID {
		t.Fatalf("expected %s, got %s", inst.DalID, result.Instance.DalID)
	}
	if len(result.Candidates) != 0 {
		t.Fatalf("expected no candidates for exact match, got %d", len(result.Candidates))
	}
}

func TestStatusSingleNameMatch(t *testing.T) {
	reg := tempRegistry(t)
	inst, err := reg.Join("default", "local", "/repo/dalcli-agent-coach", "/repo/dalcli-agent-coach", "/repo/dalcli-agent-coach/.dalfactory/dal.cue", "", 2)
	if err != nil {
		t.Fatalf("join: %v", err)
	}

	// Match by short name
	result, err := reg.Status("agent-coach")
	if err != nil {
		t.Fatalf("status by name: %v", err)
	}
	if result.Instance.DalID != inst.DalID {
		t.Fatalf("expected %s, got %s", inst.DalID, result.Instance.DalID)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("expected 1 candidate for unique match, got %d", len(result.Candidates))
	}
}

func TestStatusAmbiguousMatch(t *testing.T) {
	reg := tempRegistry(t)
	// Create two instances from same repo pattern
	inst1, err := reg.Join("default", "local", "/repo/dalcli-agent-coach", "/repo/dalcli-agent-coach", "/repo/dalcli-agent-coach/.dalfactory/dal.cue", "", 2)
	if err != nil {
		t.Fatalf("join 1: %v", err)
	}
	inst2, err := reg.Join("default", "local", "/repo/dalcli-agent-coach", "/repo/dalcli-agent-coach", "/repo/dalcli-agent-coach/.dalfactory/dal.cue", "", 3)
	if err != nil {
		t.Fatalf("join 2: %v", err)
	}

	result, err := reg.Status("agent-coach")
	if err != nil {
		t.Fatalf("status ambiguous: %v", err)
	}
	if len(result.Candidates) < 2 {
		t.Fatalf("expected >= 2 candidates, got %d", len(result.Candidates))
	}
	// Result should be one of the two instances
	if result.Instance.DalID != inst1.DalID && result.Instance.DalID != inst2.DalID {
		t.Fatalf("expected one of %s or %s, got %s", inst1.DalID, inst2.DalID, result.Instance.DalID)
	}
}

func TestStatusNotFound(t *testing.T) {
	reg := tempRegistry(t)
	_, err := reg.Status("nonexistent-package")
	if err == nil {
		t.Fatal("expected error for nonexistent package")
	}
	if !os.IsNotExist(err) && err.Error() != `instance "nonexistent-package" not found` {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStatusFullNameMatch(t *testing.T) {
	reg := tempRegistry(t)
	inst, err := reg.Join("default", "local", "/repo/dalcli-agent-bridge", "/repo/dalcli-agent-bridge", "/repo/dalcli-agent-bridge/.dalfactory/dal.cue", "", 0)
	if err != nil {
		t.Fatalf("join: %v", err)
	}

	// Match by full dalcli- prefixed name
	result, err := reg.Status("dalcli-agent-bridge")
	if err != nil {
		t.Fatalf("status by full name: %v", err)
	}
	if result.Instance.DalID != inst.DalID {
		t.Fatalf("expected %s, got %s", inst.DalID, result.Instance.DalID)
	}
}

func TestConcurrentJoinAndList(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "concurrent.db")

	var wg sync.WaitGroup
	errCh := make(chan error, 20)

	// 10 concurrent joins
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			reg, err := Open(dbPath)
			if err != nil {
				errCh <- fmt.Errorf("open for join %d: %w", n, err)
				return
			}
			defer reg.Close()
			_, err = reg.Join("default", "local", fmt.Sprintf("/repo/pkg-%d", n), fmt.Sprintf("/repo/pkg-%d", n), fmt.Sprintf("/repo/pkg-%d/dal.cue", n), "", n)
			if err != nil {
				errCh <- fmt.Errorf("join %d: %w", n, err)
			}
		}(i)
	}

	// 10 concurrent lists
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			reg, err := Open(dbPath)
			if err != nil {
				errCh <- fmt.Errorf("open for list %d: %w", n, err)
				return
			}
			defer reg.Close()
			_, err = reg.List()
			if err != nil {
				errCh <- fmt.Errorf("list %d: %w", n, err)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent error: %v", err)
	}
}

func TestStatusSourceRefMatch(t *testing.T) {
	reg := tempRegistry(t)
	inst, err := reg.Join("default", "cloud", "dalcli-agent-coach", "/cache/dalcli-agent-coach", "/cache/dalcli-agent-coach/.dalfactory/dal.cue", "", 2)
	if err != nil {
		t.Fatalf("join: %v", err)
	}

	result, err := reg.Status("agent-coach")
	if err != nil {
		t.Fatalf("status by source ref: %v", err)
	}
	if result.Instance.DalID != inst.DalID {
		t.Fatalf("expected %s, got %s", inst.DalID, result.Instance.DalID)
	}
	if result.Instance.SourceType != "cloud" {
		t.Fatalf("expected source_type cloud, got %q", result.Instance.SourceType)
	}
}
