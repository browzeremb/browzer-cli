package runfilters

import (
	"strconv"
	"strings"
	"testing"
)

// buildTerraformPlan builds a synthetic terraform plan output of approximately
// targetBytes in size containing a proper Plan: summary line.
func buildTerraformPlan(targetBytes int) []byte {
	header := `Refreshing Terraform state in-memory prior to plan...
The refreshed state will be used to calculate this plan, but will not be
persisted to local or remote state storage.

------------------------------------------------------------------------

An execution plan has been generated and is shown below.
Resource actions are indicated with the following symbols:
  + create
  ~ update in-place
  - destroy

Terraform will perform the following actions:

`
	resourceBlock := `  # aws_instance.example will be created
  + resource "aws_instance" "example" {
      + ami                          = "ami-0c55b159cbfafe1f0"
      + arn                          = (known after apply)
      + availability_zone            = (known after apply)
      + cpu_core_count               = (known after apply)
      + cpu_threads_per_core         = (known after apply)
      + get_password_data            = false
      + id                           = (known after apply)
      + instance_state               = (known after apply)
      + instance_type                = "t2.micro"
      + ipv6_address_count           = (known after apply)
      + ipv6_addresses               = (known after apply)
      + key_name                     = (known after apply)
      + outpost_arn                  = (known after apply)
      + password_length              = (known after apply)
      + placement_group              = (known after apply)
      + primary_network_interface_id = (known after apply)
      + private_dns                  = (known after apply)
      + private_ip                   = (known after apply)
      + public_dns                   = (known after apply)
      + public_ip                    = (known after apply)
      + secondary_private_ips        = (known after apply)
      + security_groups              = (known after apply)
      + source_dest_check            = true
      + subnet_id                    = (known after apply)
      + tags_all                     = (known after apply)
      + tenancy                      = (known after apply)
      + vpc_security_group_ids       = (known after apply)
    }

`
	footer := "\nPlan: 5 to add, 3 to change, 1 to destroy.\n\n" +
		"------------------------------------------------------------------------\n" +
		"Note: You didn't use the -out option to save this plan...\n"

	var sb strings.Builder
	sb.WriteString(header)
	// Repeat resource blocks until we approach targetBytes.
	for sb.Len()+len(resourceBlock) < targetBytes-len(footer)-100 {
		sb.WriteString(resourceBlock)
	}
	sb.WriteString(footer)
	return []byte(sb.String())
}

// TestFilterTerraformPlan verifies >90 % reduction and Plan: preserved on 50 KB+ output.
func TestFilterTerraformPlan(t *testing.T) {
	const targetRaw = 55 * 1024 // 55 KB
	raw := buildTerraformPlan(targetRaw)

	if len(raw) < 50*1024 {
		t.Fatalf("fixture too small: %d bytes (want ≥50 KB)", len(raw))
	}

	filtered := filterTerraform(raw)

	ratio := float64(len(filtered)) / float64(len(raw))
	if ratio > 0.10 {
		t.Errorf("byte reduction insufficient: filtered=%d raw=%d ratio=%.1f%% (want ≤10%%)",
			len(filtered), len(raw), ratio*100)
	}

	got := string(filtered)
	if !strings.Contains(got, "Plan:") {
		t.Errorf("expected 'Plan:' in output, got: %q", got)
	}
}

// TestFilterTerraform_SummaryFormat verifies the compact summary format.
func TestFilterTerraform_SummaryFormat(t *testing.T) {
	input := []byte("Plan: 5 to add, 3 to change, 1 to destroy.\n\nNote: blah blah\n")
	got := string(filterTerraform(input))

	if !strings.Contains(got, "+5 add") {
		t.Errorf("expected '+5 add' in output, got: %q", got)
	}
	if !strings.Contains(got, "~3 change") {
		t.Errorf("expected '~3 change' in output, got: %q", got)
	}
	if !strings.Contains(got, "-1 destroy") {
		t.Errorf("expected '-1 destroy' in output, got: %q", got)
	}
}

// TestFilterTerraform_ResourceLines verifies at most 5 resource change lines are kept.
func TestFilterTerraform_ResourceLines(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("Plan: 10 to add, 0 to change, 0 to destroy.\n")
	for i := 0; i < 20; i++ {
		sb.WriteString("+ aws_instance.example_" + strconv.Itoa(i) + "\n")
	}
	got := string(filterTerraform([]byte(sb.String())))

	lines := strings.Split(strings.TrimSpace(got), "\n")
	// First line is summary; remaining are resource lines (≤5).
	resourceLineCount := 0
	for _, l := range lines[1:] {
		if len(l) > 0 && (l[0] == '+' || l[0] == '-' || l[0] == '~') {
			resourceLineCount++
		}
	}
	if resourceLineCount > 5 {
		t.Errorf("expected ≤5 resource lines, got %d", resourceLineCount)
	}
}

// TestFilterTerraform_FallbackOnNoPlan verifies raw is returned when no Plan: line found.
func TestFilterTerraform_FallbackOnNoPlan(t *testing.T) {
	input := []byte("Refreshing state...\nNo changes.\n")
	got := filterTerraform(input)
	if string(got) != string(input) {
		t.Errorf("expected fallback to raw, got: %q", got)
	}
}

// TestFilterTerraform_RegisteredInDefaultRegistry verifies init() registration.
func TestFilterTerraform_RegisteredInDefaultRegistry(t *testing.T) {
	if fn := DefaultRegistry.Resolve([]string{"terraform", "plan"}); fn == nil {
		t.Error("expected 'terraform' filter registered in DefaultRegistry")
	}
}
