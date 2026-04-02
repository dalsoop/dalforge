package daemon

import (
	"os"
	"testing"
	"time"
)

func TestScheduledDalrootEnabled(t *testing.T) {
	t.Setenv("DALCENTER_SCHEDULED_DALROOT", "1")
	if !scheduledDalrootEnabled() {
		t.Fatal("expected enabled")
	}

	t.Setenv("DALCENTER_SCHEDULED_DALROOT", "")
	if scheduledDalrootEnabled() {
		t.Fatal("expected disabled")
	}
}

func TestExtractIssueNumbers(t *testing.T) {
	tests := []struct {
		text string
		want []int
	}{
		{"fix #42", []int{42}},
		{"closes #10 and #20", []int{10, 20}},
		{"no issues here", nil},
		{"#0 is not valid", nil},
		{"feat: something (#618)", []int{618}},
		{"PR for #123, #456", []int{123, 456}},
	}
	for _, tt := range tests {
		got := extractIssueNumbers(tt.text)
		if len(got) != len(tt.want) {
			t.Errorf("extractIssueNumbers(%q) = %v, want %v", tt.text, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("extractIssueNumbers(%q)[%d] = %d, want %d", tt.text, i, got[i], tt.want[i])
			}
		}
	}
}

func TestHasApproval(t *testing.T) {
	if hasApproval(nil) {
		t.Fatal("nil reviews should not have approval")
	}
	if hasApproval([]ghReview{{State: "COMMENTED"}}) {
		t.Fatal("COMMENTED should not count as approval")
	}
	if !hasApproval([]ghReview{{State: "APPROVED"}}) {
		t.Fatal("APPROVED should count as approval")
	}
	if !hasApproval([]ghReview{{State: "CHANGES_REQUESTED"}, {State: "APPROVED"}}) {
		t.Fatal("should find APPROVED among multiple reviews")
	}
}

func TestCheckOrphanIssues(t *testing.T) {
	tmp := t.TempDir()
	d := &Daemon{serviceRepo: tmp}

	issues := []ghIssue{
		{Number: 1, Title: "issue with PR", CreatedAt: time.Now().Add(-2 * time.Hour)},
		{Number: 2, Title: "orphan issue", CreatedAt: time.Now().Add(-2 * time.Hour)},
		{Number: 3, Title: "new issue", CreatedAt: time.Now()}, // too new
	}

	prs := []ghPullRequest{
		{Number: 10, Title: "fix #1", URL: "https://github.com/org/repo/pull/10"},
	}

	// Should not panic with no bridge configured
	d.checkOrphanIssues("owner/repo", issues, prs)
}

func TestCheckStalePRs(t *testing.T) {
	tmp := t.TempDir()
	d := &Daemon{serviceRepo: tmp}

	prs := []ghPullRequest{
		{Number: 1, Title: "fresh PR", CreatedAt: time.Now(), IsDraft: false},
		{Number: 2, Title: "stale PR", CreatedAt: time.Now().Add(-13 * time.Hour), IsDraft: false},
		{Number: 3, Title: "draft PR", CreatedAt: time.Now().Add(-24 * time.Hour), IsDraft: true},
		{Number: 4, Title: "reviewed PR", CreatedAt: time.Now().Add(-24 * time.Hour),
			Reviews: []ghReview{{State: "COMMENTED"}}},
	}

	// Should not panic with no bridge configured
	d.checkStalePRs(prs)
}

func TestCheckApprovedPRs(t *testing.T) {
	tmp := t.TempDir()
	os.MkdirAll(tmp+"/dalroot-notifications", 0o755)
	d := &Daemon{serviceRepo: tmp}

	prs := []ghPullRequest{
		{Number: 1, Title: "not approved", Reviews: []ghReview{{State: "COMMENTED"}}},
		{Number: 2, Title: "approved PR", URL: "https://github.com/org/repo/pull/2",
			Reviews: []ghReview{{State: "APPROVED"}}},
	}

	// Should not panic with no bridge configured
	d.checkApprovedPRs(prs)
}

func TestPostDailySummary(t *testing.T) {
	tmp := t.TempDir()
	d := &Daemon{serviceRepo: tmp}

	issues := []ghIssue{
		{Number: 1, Title: "issue 1"},
		{Number: 2, Title: "issue 2", URL: "https://github.com/org/repo/pull/2"}, // PR, should be excluded
	}
	prs := []ghPullRequest{
		{Number: 10, Title: "PR 1", IsDraft: true, CreatedAt: time.Now()},
		{Number: 11, Title: "PR 2", CreatedAt: time.Now().Add(-24 * time.Hour)},
	}

	// Should not panic with no bridge configured
	d.postDailySummary(issues, prs)
}
