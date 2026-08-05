package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/clementnunes/sage-resilient-urgent-scheduler/pkg/intent"
)

func TestRunTranslatesIntentThroughHermes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		content, err := json.Marshal(validIntentResponse())
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(writer, `{"choices":[{"message":{"role":"assistant","content":%s}}]}`, content)
	}))
	defer server.Close()
	getenv := func(key string) string {
		if key == "HERMES_API_KEY" {
			return "secret"
		}
		return ""
	}
	var output bytes.Buffer
	err := run(
		context.Background(),
		[]string{"-endpoint", server.URL, "-text", "Run cloud-cover on W000 every hour.", "-pretty=false"},
		bytes.NewBuffer(nil),
		&output,
		&bytes.Buffer{},
		getenv,
	)
	if err != nil {
		t.Fatal(err)
	}
	var draft intent.Draft
	if err := json.Unmarshal(output.Bytes(), &draft); err != nil {
		t.Fatal(err)
	}
	if draft.Goal != "Measure cloud cover" || draft.SourceText != "Run cloud-cover on W000 every hour." {
		t.Fatalf("unexpected draft: %+v", draft)
	}
	if !draft.HumanApprovalRequired {
		t.Fatal("output did not require human approval")
	}
}

func TestBoundedTextRejectsOversizedInput(t *testing.T) {
	_, err := boundedText(strings.NewReader(strings.Repeat("x", intent.MaxSourceBytes+1)))
	if err == nil {
		t.Fatal("oversized input was accepted")
	}
}

func TestRunRejectsUnexpectedPositionalArguments(t *testing.T) {
	err := run(
		context.Background(),
		[]string{"unflagged intent"},
		bytes.NewBuffer(nil),
		&bytes.Buffer{},
		&bytes.Buffer{},
		func(string) string { return "" },
	)
	if err == nil || !strings.Contains(err.Error(), "unexpected positional arguments") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func validIntentResponse() string {
	return `{
  "sourceText":"",
  "goal":"Measure cloud cover",
  "applications":["cloud-cover"],
  "nodes":["W000"],
  "nodeTags":[],
  "scienceRules":["schedule(cloud-cover): cronjob('cloud-cover', '0 * * * *')"],
  "successCriteria":[],
  "questions":[],
  "humanApprovalRequired":true
}`
}
