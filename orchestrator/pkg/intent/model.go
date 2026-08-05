// Package intent translates human intent into the words already used by SAGE.
package intent

import (
	"errors"
	"fmt"
	"strings"
)

const (
	MaxSourceBytes = 16 << 10
	MaxStringBytes = 4 << 10
	MaxListItems   = 32
)

// Draft is a reviewable science-goal draft, not an executable SAGE job.
type Draft struct {
	SourceText            string   `json:"sourceText"`
	Goal                  string   `json:"goal"`
	Applications          []string `json:"applications"`
	Nodes                 []string `json:"nodes"`
	NodeTags              []string `json:"nodeTags"`
	ScienceRules          []string `json:"scienceRules"`
	SuccessCriteria       []string `json:"successCriteria"`
	Questions             []string `json:"questions"`
	HumanApprovalRequired bool     `json:"humanApprovalRequired"`
}

func (d Draft) Validate() error {
	var errs []error
	if err := requiredString("sourceText", d.SourceText, MaxSourceBytes); err != nil {
		errs = append(errs, err)
	}
	if err := requiredString("goal", d.Goal, MaxStringBytes); err != nil {
		errs = append(errs, err)
	}
	for name, values := range map[string][]string{
		"applications":    d.Applications,
		"nodes":           d.Nodes,
		"nodeTags":        d.NodeTags,
		"scienceRules":    d.ScienceRules,
		"successCriteria": d.SuccessCriteria,
		"questions":       d.Questions,
	} {
		if err := validateList(name, values); err != nil {
			errs = append(errs, err)
		}
	}
	if !d.HumanApprovalRequired {
		errs = append(errs, errors.New("humanApprovalRequired must be true"))
	}
	return errors.Join(errs...)
}

func requiredString(name, value string, maximum int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if len(value) > maximum {
		return fmt.Errorf("%s exceeds %d bytes", name, maximum)
	}
	return nil
}

func validateList(name string, values []string) error {
	if len(values) > MaxListItems {
		return fmt.Errorf("%s exceeds %d items", name, MaxListItems)
	}
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if err := requiredString(fmt.Sprintf("%s[%d]", name, index), value, MaxStringBytes); err != nil {
			return err
		}
		normalized := strings.ToLower(strings.TrimSpace(value))
		if _, exists := seen[normalized]; exists {
			return fmt.Errorf("%s[%d] duplicates %q", name, index, value)
		}
		seen[normalized] = struct{}{}
	}
	return nil
}
