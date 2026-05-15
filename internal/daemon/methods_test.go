package daemon

import (
	"slices"
	"testing"
)

// TestProtocolFeatures_OmitsRetiredWorkflowFeatures is the regression pin
// for the wire-contract bump that retired the workflow-mutate RPC surface.
// CurrentProtocolVersion must be 3 (the bump signals the contract break to
// clients pinned to v2), and protocolFeatures must not advertise the
// workflow-coupled features `save` or `schemaV2`.
func TestProtocolFeatures_OmitsRetiredWorkflowFeatures(t *testing.T) {
	if got, want := CurrentProtocolVersion, 3; got != want {
		t.Fatalf("CurrentProtocolVersion = %d, want %d", got, want)
	}
	tests := []struct {
		name    string
		feature string
		want    bool
	}{
		{name: "estimationMethod is retained", feature: "estimationMethod", want: true},
		{name: "save is dropped", feature: "save", want: false},
		{name: "schemaV2 is dropped", feature: "schemaV2", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := slices.Contains(protocolFeatures, tc.feature)
			if got != tc.want {
				t.Fatalf("slices.Contains(protocolFeatures, %q) = %v, want %v (slice=%v)",
					tc.feature, got, tc.want, protocolFeatures)
			}
		})
	}
}
