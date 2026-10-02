package produce

import "testing"

// The two cases that must stay hidden are the whole point of the helper: an
// ungraded listing has no grade to show, and the implicit 'STD' is a choice of
// one. Everything else has to survive verbatim — a grower's code is theirs.
func TestGradeLabel(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"XL2", "XL2"},
		{"  M2  ", "M2"},
		{"", ""},
		{"   ", ""},
		{"STD", ""},
		{"std", ""},
		{" Std ", ""},
		// Not the default, merely starting with it: a grower could name a
		// grade "STD2" and it would be theirs to see.
		{"STD2", "STD2"},
	} {
		if got := GradeLabel(tc.in); got != tc.want {
			t.Errorf("GradeLabel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestGradedName(t *testing.T) {
	for _, tc := range []struct{ name, code, want string }{
		{"Pomegranate", "XL2", "Pomegranate (XL2)"},
		{"Pomegranate", "", "Pomegranate"},
		{"Pomegranate", "STD", "Pomegranate"},
		{"Tomato", " M ", "Tomato (M)"},
	} {
		if got := GradedName(tc.name, tc.code); got != tc.want {
			t.Errorf("GradedName(%q, %q) = %q, want %q", tc.name, tc.code, got, tc.want)
		}
	}
}
