package sdkbind

import "testing"

// findReconciliation answers the reconciliation for one attribute, or fails.
func findReconciliation(t *testing.T, reconciled []Reconciliation, key, attribute string) Reconciliation {
	t.Helper()
	for _, r := range reconciled {
		if r.Key == key && r.Attribute == attribute {
			return r
		}
	}
	t.Fatalf("no reconciliation for %s.%s in %+v", key, attribute, reconciled)
	return Reconciliation{}
}

// TestUnit_Prune_ARepairedAccessorIsRecorded holds the count Removed has to
// be read against. The same deletion means one thing beside a run that
// reconciled a dozen accessors and another beside one that reconciled none,
// and until the reconciliations are recorded a reader cannot tell which run
// they are looking at.
func TestUnit_Prune_ARepairedAccessorIsRecorded(t *testing.T) {
	b, _ := prunedKiota(t)

	vendor := findReconciliation(t, b.Reconciled, "tags", "vendor")
	if vendor.Drafted != "GetVendor" || vendor.Settled != "GetVendorEscaped" {
		t.Errorf("vendor reconciliation = %q -> %q, want GetVendor -> GetVendorEscaped",
			vendor.Drafted, vendor.Settled)
	}
	if vendor.Kind != "resource" {
		t.Errorf("vendor reconciliation kind = %q, want resource", vendor.Kind)
	}

	// The flip runs in both directions: the binder appends Escaped for a
	// name reserved by Go, and an SDK that did not escape it is just as
	// much a disagreement as one that did.
	owner := findReconciliation(t, b.Reconciled, "tags", "owner_id")
	if owner.Drafted != "SetOwnerIdEscaped" || owner.Settled != "SetOwnerId" {
		t.Errorf("owner_id reconciliation = %q -> %q, want the Escaped spelling dropped",
			owner.Drafted, owner.Settled)
	}
}

// TestUnit_Prune_ADraftTheSDKAgreesWithIsNotRecorded keeps the record to
// disagreements. "error" is reserved by Go, so the binder appends Escaped
// without being told and the SDK carries exactly that — nothing was
// settled, and recording it would inflate the one number Removed is read
// against.
func TestUnit_Prune_ADraftTheSDKAgreesWithIsNotRecorded(t *testing.T) {
	b, _ := prunedKiota(t)
	for _, r := range b.Reconciled {
		if r.Key == "tags" && r.Attribute == "error" {
			t.Errorf("the correctly drafted accessor was recorded as reconciled: %+v", r)
		}
	}
	if field := findField(t, b.Resources["tags"].Fields, "error"); field.Access.Get != "GetErrorEscaped" {
		t.Errorf("error accessor = %q, want the spelling the binder drafted", field.Access.Get)
	}
}

// TestUnit_Prune_ReconciliationsAreOrderedAndNamed keeps the record a stable
// diff and holds every entry to naming both spellings. A record that says
// something was reconciled without saying from what is not evidence of
// anything.
func TestUnit_Prune_ReconciliationsAreOrderedAndNamed(t *testing.T) {
	b, _ := prunedKiota(t)
	if len(b.Reconciled) == 0 {
		t.Fatal("the fixture SDK mangles names the binder cannot predict; none was recorded")
	}
	for i, r := range b.Reconciled {
		if r.Key == "" || r.Drafted == "" || r.Settled == "" {
			t.Errorf("reconciliation %d is not fully named: %+v", i, r)
		}
		if r.Drafted == r.Settled {
			t.Errorf("reconciliation %d records no disagreement: %+v", i, r)
		}
		if i > 0 && !reconciliationLess(b.Reconciled[i-1], r) {
			t.Errorf("reconciliations are not ordered at %d: %+v then %+v", i, b.Reconciled[i-1], r)
		}
	}
}

// reconciliationLess mirrors the order Prune fixes, so the test reads the
// same rule the code applies rather than a second copy of it.
func reconciliationLess(a, z Reconciliation) bool {
	switch {
	case a.Kind != z.Kind:
		return a.Kind < z.Kind
	case a.Key != z.Key:
		return a.Key < z.Key
	case a.Attribute != z.Attribute:
		return a.Attribute < z.Attribute
	default:
		return a.Drafted <= z.Drafted
	}
}
