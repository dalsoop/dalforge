package talk

import (
	"testing"
)

func TestIsMentioned(t *testing.T) {
	d := &Daemon{
		cfg: Config{
			BotUsername: "dal-tech-writer-201",
			DalName:    "tech-writer",
		},
	}

	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"exact mention", "@dal-tech-writer-201 hello", true},
		{"dal name mention", "@tech-writer do this", true},
		{"no mention", "just a message", false},
		{"partial match", "dal-tech-writer-201 without @", false},
		{"case insensitive", "@DAL-TECH-WRITER-201 hello", true},
		{"mention in middle", "hey @dal-tech-writer-201 check this", true},
		{"empty", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := d.isMentioned(tc.content)
			if got != tc.want {
				t.Fatalf("isMentioned(%q) = %v, want %v", tc.content, got, tc.want)
			}
		})
	}
}

func TestStripMention(t *testing.T) {
	d := &Daemon{
		cfg: Config{
			BotUsername: "dal-tech-writer-201",
			DalName:    "tech-writer",
		},
	}

	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"strip bot mention", "@dal-tech-writer-201 hello", "hello"},
		{"strip dal name", "@tech-writer do this", "do this"},
		{"strip both", "@dal-tech-writer-201 @tech-writer hello", "hello"},
		{"no mention", "just a message", "just a message"},
		{"mention only", "@dal-tech-writer-201", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := d.stripMention(tc.content)
			if got != tc.want {
				t.Fatalf("stripMention(%q) = %q, want %q", tc.content, got, tc.want)
			}
		})
	}
}

func TestIsDuplicate(t *testing.T) {
	d := &Daemon{seen: make(map[string]bool)}

	if d.isDuplicate("msg-1") {
		t.Fatal("first call should not be duplicate")
	}
	if !d.isDuplicate("msg-1") {
		t.Fatal("second call should be duplicate")
	}
	if d.isDuplicate("msg-2") {
		t.Fatal("different ID should not be duplicate")
	}
}
