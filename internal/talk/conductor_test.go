package talk

import (
	"strings"
	"testing"
)

func TestBuildDalList(t *testing.T) {
	c := &Conductor{
		cfg: ConductorConfig{
			Dals: []DalInfo{
				{Username: "dal-architect", Role: "구조 설계"},
				{Username: "dal-writer", Role: "집필"},
				{Username: "dal-story-checker", Role: "검증"},
			},
		},
	}
	list := c.buildDalList()
	if list == "" {
		t.Fatal("should not be empty")
	}
	for _, name := range []string{"architect", "writer", "story-checker"} {
		if !strings.Contains(list, name) {
			t.Errorf("missing %q in: %s", name, list)
		}
	}
}

func TestBuildDalList_Empty(t *testing.T) {
	c := &Conductor{cfg: ConductorConfig{}}
	if c.buildDalList() != "" {
		t.Error("empty dals should return empty")
	}
}

func TestIsDalBot(t *testing.T) {
	c := &Conductor{
		dalBotIDs: map[string]bool{
			"bot-1": true,
			"bot-2": true,
		},
	}
	if !c.isDalBot("bot-1") {
		t.Error("bot-1 should be a dal bot")
	}
	if c.isDalBot("user-1") {
		t.Error("user-1 should not be a dal bot")
	}
	if c.isDalBot("") {
		t.Error("empty should not be a dal bot")
	}
}

func TestConductorIsDuplicate(t *testing.T) {
	c := &Conductor{
		seen: make(map[string]bool),
	}
	if c.isDuplicate("msg-1") {
		t.Error("first time should not be duplicate")
	}
	if !c.isDuplicate("msg-1") {
		t.Error("second time should be duplicate")
	}
	if c.isDuplicate("msg-2") {
		t.Error("different message should not be duplicate")
	}
}
