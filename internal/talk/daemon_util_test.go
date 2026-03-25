package talk

import (
	"strings"
	"testing"
)

func TestDetectIP(t *testing.T) {
	ip := detectIP()
	// Should return something or empty — not panic
	if ip != "" && !strings.Contains(ip, ".") {
		t.Errorf("detectIP() = %q, doesn't look like IP", ip)
	}
}

func TestDetectVMID(t *testing.T) {
	vmid := detectVMID()
	// In non-LXC env returns empty — that's fine
	_ = vmid
}

func TestTalkTruncate(t *testing.T) {
	tests := []struct {
		input string
		max   int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
		{"ab", 1, "a..."},
		{"exactly", 7, "exactly"},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.max)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.max, got, tt.want)
		}
	}
}
