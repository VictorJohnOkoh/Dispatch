package vendors

import "testing"

// Unknown is an answer and not a missing value, and it is the zero value, so an
// unfilled Capabilities is honest rather than wrong. A zero value of No would be
// a claim the Vendor never made.
func TestSupportHasThreeValuesAndUnknownIsZero(t *testing.T) {
	var zero Support
	if zero != Unknown {
		t.Errorf("the zero Support is %d, want Unknown", zero)
	}
	if Unknown == No || No == Yes || Unknown == Yes {
		t.Error("Unknown, No and Yes are three distinct answers")
	}

	var caps Capabilities
	for _, c := range []struct {
		name string
		got  Support
	}{{"Chat", caps.Chat}, {"Tools", caps.Tools}, {"Reasoning", caps.Reasoning}, {"Vision", caps.Vision}} {
		if c.got != Unknown {
			t.Errorf("an unfilled %s is %d, want Unknown", c.name, c.got)
		}
	}
}
