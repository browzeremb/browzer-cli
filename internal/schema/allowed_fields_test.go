package schema

import (
	"reflect"
	"sort"
	"testing"
)

func TestAllowedFields_TaskBrief(t *testing.T) {
	got, err := AllowedFields("#TaskBrief")
	if err != nil {
		t.Fatalf("AllowedFields(#TaskBrief): %v", err)
	}
	want := map[string]bool{
		"taskId":         true,
		"title":          true,
		"suggestedModel": true,
		"trivial":        true,
		"skillsFound":    true,
		"scope":          true,
		"dependsOn":      true,
	}
	if !reflect.DeepEqual(sortedKeys(got), sortedKeys(want)) {
		t.Errorf("got %v, want %v", sortedKeys(got), sortedKeys(want))
	}
}

func TestAllowedFields_PRD(t *testing.T) {
	got, err := AllowedFields("PRD") // no `#` prefix — auto-added
	if err != nil {
		t.Fatalf("AllowedFields(PRD): %v", err)
	}
	for _, must := range []string{
		"title",
		"overview",
		"functionalRequirements",
		"acceptanceCriteria",
		"successMetrics",
		"nonFunctionalRequirements",
		"personas",
		"risks",
	} {
		if !got[must] {
			t.Errorf("expected %q in PRD allow-list, got %v", must, sortedKeys(got))
		}
	}
}

func TestAllowedFields_Unknown(t *testing.T) {
	if _, err := AllowedFields("#TotallyMadeUp"); err == nil {
		t.Errorf("expected error for unknown definition")
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
