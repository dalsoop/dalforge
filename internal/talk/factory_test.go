package talk

import "testing"

func TestExtractQuotedValue(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{`name: "leader"`, "leader"},
		{`uuid: "abc-123"`, "abc-123"},
		{`version: "1.0.0"`, "1.0.0"},
		{`no quotes here`, ""},
		{`empty: ""`, ""},
		{`single: "one"`, "one"},
		{`multiple: "first" "second"`, `first" "second`}, // captures first to last quote
	}
	for _, tt := range tests {
		got := extractQuotedValue(tt.input)
		if got != tt.want {
			t.Errorf("extractQuotedValue(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractStringList(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{`skills: ["a", "b", "c"]`, 3},
		{`skills: ["single"]`, 1},
		{`skills: []`, 0},
		{`no list here`, 0},
		{`hooks: ["hook1", "hook2"]`, 2},
	}
	for _, tt := range tests {
		got := extractStringList(tt.input)
		if len(got) != tt.want {
			t.Errorf("extractStringList(%q) = %v (len=%d), want len=%d", tt.input, got, len(got), tt.want)
		}
	}
}

func TestExtractStringList_Values(t *testing.T) {
	got := extractStringList(`skills: ["skills/plot-review", "skills/foreshadow-check"]`)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0] != "skills/plot-review" {
		t.Errorf("got[0] = %q", got[0])
	}
	if got[1] != "skills/foreshadow-check" {
		t.Errorf("got[1] = %q", got[1])
	}
}
