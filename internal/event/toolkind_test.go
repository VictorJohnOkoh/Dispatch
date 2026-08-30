package event

import (
	"encoding/json"
	"testing"
)

// The set is exactly these five, in this order, because arrays are indexed by it.
func TestToolKindIsExactlyFive(t *testing.T) {
	want := []struct {
		kind ToolKind
		name string
	}{
		{ToolRead, "read"},
		{ToolEdit, "edit"},
		{ToolExecute, "execute"},
		{ToolFetch, "fetch"},
		{ToolOther, "other"},
	}

	if len(want) != NumToolKinds {
		t.Fatalf("NumToolKinds is %d, want %d", NumToolKinds, len(want))
	}

	for i, c := range want {
		if int(c.kind) != i {
			t.Errorf("%s is %d, want %d", c.name, c.kind, i)
		}
		b, err := json.Marshal(c.kind)
		if err != nil {
			t.Fatalf("marshal %s: %v", c.name, err)
		}
		if got := string(b); got != `"`+c.name+`"` {
			t.Errorf("marshal: got %s, want %q", got, c.name)
		}

		var back ToolKind
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatalf("unmarshal %s: %v", b, err)
		}
		if back != c.kind {
			t.Errorf("round trip: got %s, want %s", back, c.name)
		}
	}
}

func TestToolKindRejectsAnythingElse(t *testing.T) {
	var k ToolKind
	if err := json.Unmarshal([]byte(`"delete"`), &k); err == nil {
		t.Error("delete is not a ToolKind and should not decode")
	}
	if _, err := json.Marshal(ToolKind(NumToolKinds)); err == nil {
		t.Error("a ToolKind past the set should not encode")
	}
}

// The Approval Policy is five slots on the wire, named rather than positional, and
// in ToolKind order.
func TestPolicyIsFiveNamedSlots(t *testing.T) {
	p := Policy{Auto, Wait, Refuse, Wait, Auto}

	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	const want = `{"read":"auto","edit":"wait","execute":"refuse","fetch":"wait","other":"auto"}`
	if got := string(b); got != want {
		t.Errorf("\n got %s\nwant %s", got, want)
	}

	var back Policy
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back != p {
		t.Errorf("round trip: got %v, want %v", back, p)
	}
}

// A slot that is missing is an error, because the five are always all set.
func TestPolicyRefusesAPartialSet(t *testing.T) {
	var p Policy
	if err := json.Unmarshal([]byte(`{"read":"auto","edit":"wait","execute":"wait","fetch":"wait"}`), &p); err == nil {
		t.Error("four slots is not an Approval Policy")
	}
}
