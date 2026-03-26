package main

import "testing"

func TestClassifyTaskError_Env(t *testing.T) {
	cases := []string{
		"bash: claude: command not found",
		"/bin/sh: go: No such file or directory",
		"Error: permission denied while trying to connect",
	}
	for _, c := range cases {
		if got := classifyTaskError(c); got != ErrClassEnv {
			t.Errorf("classifyTaskError(%q) = %s, want env", c, got)
		}
	}
}

func TestClassifyTaskError_Deps(t *testing.T) {
	cases := []string{
		"go: module github.com/foo/bar: not found",
		"npm ERR! missing: react@^18.0.0",
		"error: could not compile `veil-cli`",
	}
	for _, c := range cases {
		if got := classifyTaskError(c); got != ErrClassDeps {
			t.Errorf("classifyTaskError(%q) = %s, want deps", c, got)
		}
	}
}

func TestClassifyTaskError_Git(t *testing.T) {
	cases := []string{
		"error: merge conflict in main.go",
		"HEAD detached at abc1234",
		"fatal: not a git repository",
	}
	for _, c := range cases {
		if got := classifyTaskError(c); got != ErrClassGit {
			t.Errorf("classifyTaskError(%q) = %s, want git", c, got)
		}
	}
}

func TestClassifyTaskError_Unknown(t *testing.T) {
	cases := []string{
		"something went wrong",
		"exit status 1",
		"",
	}
	for _, c := range cases {
		if got := classifyTaskError(c); got != ErrClassUnknown {
			t.Errorf("classifyTaskError(%q) = %s, want unknown", c, got)
		}
	}
}

func TestSelfRepair_UnknownReturnsNoRetry(t *testing.T) {
	retry, fix := selfRepair("test task", "unknown error", nil)
	if retry {
		t.Error("unknown error should not retry")
	}
	if fix != "" {
		t.Errorf("fix should be empty, got %q", fix)
	}
}

func TestSelfRepair_Cooldown(t *testing.T) {
	task := "cooldown-test-task-unique"
	// First attempt marks cooldown
	selfRepair(task, "unknown error", nil)
	// Second attempt should be cooled down
	if !isRepairCoolingDown(task) {
		t.Error("should be cooling down after first attempt")
	}
}

func TestClassifyTaskError_Instructions(t *testing.T) {
	// Needs multiple indicators to classify as instructions
	output := "error: instructions.md references issue #398 which is stale, task.md conflict"
	if got := classifyTaskError(output); got != ErrClassInstructions {
		t.Errorf("got %s, want instructions", got)
	}
}

func TestClassifyTaskError_InstructionsSingleIndicator(t *testing.T) {
	// Single indicator should NOT be classified as instructions
	output := "reading instructions.md"
	if got := classifyTaskError(output); got == ErrClassInstructions {
		t.Error("single indicator should not classify as instructions")
	}
}
