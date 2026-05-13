package runfilters

import (
	"strconv"
	"strings"
	"testing"
)

const kubectlPodsHeader = "NAME                     READY   STATUS      RESTARTS   AGE\n"

// buildKubectlOutput builds a synthetic kubectl get pods output of approximately
// targetBytes in size. runningCount pods are Running, problemCount are not.
func buildKubectlOutput(targetBytes, runningCount, problemCount int) []byte {
	var sb strings.Builder
	sb.WriteString(kubectlPodsHeader)

	for i := 0; i < runningCount; i++ {
		sb.WriteString("pod-running-" + strconv.Itoa(i) + strings.Repeat(" ", 15-len(strconv.Itoa(i))) + "1/1     Running     0          5d\n")
	}
	for i := 0; i < problemCount; i++ {
		sb.WriteString("pod-error-" + strconv.Itoa(i) + strings.Repeat(" ", 17-len(strconv.Itoa(i))) + "0/1     Error       3          2h\n")
	}

	// Pad to targetBytes with additional Running pods.
	padLine := "pod-padding-0000        1/1     Running     0          1h\n"
	for sb.Len() < targetBytes {
		sb.WriteString(padLine)
	}

	return []byte(sb.String())
}

// TestFilterKubectl_ByteReduction asserts >65 % reduction on ≥2 KB output.
func TestFilterKubectl_ByteReduction(t *testing.T) {
	const targetRaw = 3 * 1024 // 3 KB
	raw := buildKubectlOutput(targetRaw, 50, 2)

	if len(raw) < 2*1024 {
		t.Fatalf("fixture too small: %d bytes (want ≥2 KB)", len(raw))
	}

	filtered := filterKubectl(raw)

	ratio := float64(len(filtered)) / float64(len(raw))
	if ratio > 0.35 {
		t.Errorf("byte reduction insufficient: filtered=%d raw=%d ratio=%.1f%% (want ≤35%%)",
			len(filtered), len(raw), ratio*100)
	}
}

// TestFilterKubectl_HeaderPreserved verifies the NAME header is in output.
func TestFilterKubectl_HeaderPreserved(t *testing.T) {
	raw := buildKubectlOutput(3*1024, 20, 1)
	got := string(filterKubectl(raw))

	if !strings.Contains(got, "NAME") {
		t.Errorf("expected NAME header in output, got: %q", got)
	}
}

// TestFilterKubectl_RunningDropped verifies Running pods are dropped from output.
func TestFilterKubectl_RunningDropped(t *testing.T) {
	input := []byte(kubectlPodsHeader +
		"web-abc123          1/1     Running     0          3d\n" +
		"db-xyz789           0/1     Error       2          1h\n")

	got := string(filterKubectl(input))

	if strings.Contains(got, "web-abc123") {
		t.Error("expected Running pod to be dropped from output")
	}
	if !strings.Contains(got, "db-xyz789") {
		t.Error("expected Error pod to be kept in output")
	}
}

// TestFilterKubectl_CompletedDropped verifies Completed pods are dropped.
func TestFilterKubectl_CompletedDropped(t *testing.T) {
	input := []byte(kubectlPodsHeader +
		"job-run-abc         0/1     Completed   0          10m\n" +
		"web-pod-001         0/1     Pending     0          30s\n")

	got := string(filterKubectl(input))

	if strings.Contains(got, "job-run-abc") {
		t.Error("expected Completed pod to be dropped from output")
	}
	if !strings.Contains(got, "web-pod-001") {
		t.Error("expected Pending pod to be kept in output")
	}
}

// TestFilterKubectl_SummaryLine verifies the N/M pods running summary.
func TestFilterKubectl_SummaryLine(t *testing.T) {
	input := []byte(kubectlPodsHeader +
		"pod-a               1/1     Running     0          1d\n" +
		"pod-b               1/1     Running     0          1d\n" +
		"pod-c               0/1     Error       5          2h\n")

	got := string(filterKubectl(input))

	if !strings.Contains(got, "2/3 pods running") {
		t.Errorf("expected '2/3 pods running' summary, got: %q", got)
	}
}

// TestFilterKubectl_FallbackOnNoHeader verifies raw is returned when no NAME header found.
func TestFilterKubectl_FallbackOnNoHeader(t *testing.T) {
	input := []byte("some random output without a NAME column\n")
	got := filterKubectl(input)
	if string(got) != string(input) {
		t.Errorf("expected fallback to raw, got: %q", got)
	}
}

// TestFilterKubectl_RegisteredInDefaultRegistry verifies init() registration.
func TestFilterKubectl_RegisteredInDefaultRegistry(t *testing.T) {
	if fn := DefaultRegistry.Resolve([]string{"kubectl", "get", "pods"}); fn == nil {
		t.Error("expected 'kubectl' filter registered in DefaultRegistry")
	}
}

// TestFilterKubectl_MultiNamespace verifies that kubectl get pods -A output
// (NAMESPACE NAME READY STATUS RESTARTS AGE header — STATUS at column index 3)
// is handled correctly: Running and Completed rows are still dropped, problem
// rows are kept, and the summary line reflects the correct counts.
func TestFilterKubectl_MultiNamespace(t *testing.T) {
	const header = "NAMESPACE   NAME                   READY   STATUS      RESTARTS   AGE\n"
	input := []byte(header +
		"default     web-abc123             1/1     Running     0          3d\n" +
		"kube-system coredns-xyz            1/1     Running     2          10d\n" +
		"default     job-batch-001          0/1     Completed   0          1h\n" +
		"default     db-crash-000           0/1     CrashLoopBackOff  5   2h\n" +
		"staging     api-error-001          0/1     Error       3          30m\n")

	got := string(filterKubectl(input))

	// Running and Completed rows must be dropped.
	if strings.Contains(got, "web-abc123") {
		t.Error("expected Running pod to be dropped from -A output")
	}
	if strings.Contains(got, "coredns-xyz") {
		t.Error("expected Running pod (kube-system) to be dropped from -A output")
	}
	if strings.Contains(got, "job-batch-001") {
		t.Error("expected Completed pod to be dropped from -A output")
	}

	// Problem rows must be kept.
	if !strings.Contains(got, "db-crash-000") {
		t.Error("expected CrashLoopBackOff pod to be kept in -A output")
	}
	if !strings.Contains(got, "api-error-001") {
		t.Error("expected Error pod to be kept in -A output")
	}

	// Summary: 3 running (web-abc + coredns + job-batch-001 Completed) out of 5 total.
	if !strings.Contains(got, "3/5 pods running") {
		t.Errorf("expected '3/5 pods running' summary for -A output, got: %q", got)
	}
}
