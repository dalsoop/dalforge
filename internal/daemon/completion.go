package daemon

import (
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

// CompletionResult holds the results of post-task go build and go test verification.
type CompletionResult struct {
	BuildOK     bool      `json:"build_ok"`
	BuildOutput string    `json:"build_output,omitempty"`
	TestOK      bool      `json:"test_ok"`
	TestOutput  string    `json:"test_output,omitempty"`
	Duration    string    `json:"duration"`
	Skipped     bool      `json:"skipped"`
	SkipReason  string    `json:"skip_reason,omitempty"`
}

// runCompletion executes go build and go test inside the container to verify task output.
// It only runs when the task produced git changes (verified == "yes").
func runCompletion(containerID string, tr *taskResult) {
	if tr.Verified != "yes" {
		tr.Completion = &CompletionResult{
			Skipped:    true,
			SkipReason: "no code changes detected",
		}
		return
	}

	start := time.Now()
	result := &CompletionResult{}

	// Run go build ./...
	buildOut, buildErr := dockerExecCmd(containerID,
		fmt.Sprintf("cd %s && go build ./... 2>&1", containerWorkDir))
	if buildErr != nil {
		result.BuildOK = false
		result.BuildOutput = truncateStr(buildOut, 2000)
		log.Printf("[completion] %s build FAILED: %s", tr.ID, truncateStr(buildOut, 200))
	} else {
		result.BuildOK = true
		if buildOut != "" {
			result.BuildOutput = truncateStr(buildOut, 2000)
		}
		log.Printf("[completion] %s build OK", tr.ID)
	}

	// Run go test ./... regardless of build result
	testOut, testErr := dockerExecCmd(containerID,
		fmt.Sprintf("cd %s && go test ./... 2>&1", containerWorkDir))
	if testErr != nil {
		result.TestOK = false
		result.TestOutput = truncateStr(testOut, 2000)
		log.Printf("[completion] %s test FAILED: %s", tr.ID, truncateStr(testOut, 200))
	} else {
		result.TestOK = true
		if testOut != "" {
			result.TestOutput = truncateStr(testOut, 2000)
		}
		log.Printf("[completion] %s test OK", tr.ID)
	}

	result.Duration = time.Since(start).Round(time.Millisecond).String()
	tr.Completion = result

	log.Printf("[completion] %s done (build=%v test=%v %s)", tr.ID, result.BuildOK, result.TestOK, result.Duration)
}

// dockerExecCmd runs a command inside the container and returns combined output.
func dockerExecCmd(containerID, shellCmd string) (string, error) {
	cmd := exec.Command("docker", "exec", containerID,
		"bash", "-c", shellCmd)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
