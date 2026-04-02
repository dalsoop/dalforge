package daemon

import (
	"testing"
)

func TestContainerShort(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"abc123def456789", "abc123def456"},
		{"short", "short"},
		{"exactly12ch", "exactly12ch"},
		{"", ""},
	}
	for _, tc := range tests {
		got := containerShort(tc.in)
		if got != tc.want {
			t.Errorf("containerShort(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
