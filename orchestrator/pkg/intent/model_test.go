package intent

import (
	"strings"
	"testing"
)

func TestDraftValidate(t *testing.T) {
	draft := validTestDraft()
	if err := draft.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDraftValidateRejectsEmptyGoalAndAutomaticApproval(t *testing.T) {
	draft := validTestDraft()
	draft.Goal = ""
	draft.HumanApprovalRequired = false
	err := draft.Validate()
	if err == nil {
		t.Fatal("expected invalid draft")
	}
	if !strings.Contains(err.Error(), "goal") || !strings.Contains(err.Error(), "humanApprovalRequired") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func validTestDraft() Draft {
	return Draft{
		SourceText:            "Measure cloud cover on W000 every hour.",
		Goal:                  "Measure cloud cover",
		Applications:          []string{"cloud-cover"},
		Nodes:                 []string{"W000"},
		NodeTags:              []string{},
		ScienceRules:          []string{},
		SuccessCriteria:       []string{},
		Questions:             []string{"Which SAGE science rule should trigger the application?"},
		HumanApprovalRequired: true,
	}
}
