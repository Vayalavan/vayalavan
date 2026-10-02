package grams

import "testing"

func TestFormat(t *testing.T) {
	tests := []struct {
		grams int32
		want  string
	}{
		{0, "0 g"},
		{1, "1 g"},
		{250, "250 g"},
		{999, "999 g"},
		{1000, "1 kg"},
		{1500, "1.5 kg"},
		{1050, "1.1 kg"}, // rounds up at the half
		{1040, "1 kg"},   // rounds down to a whole kilo, no ".0"
		{1949, "1.9 kg"},
		{1950, "2 kg"}, // the tenth carries into the kilo
		{10000, "10 kg"},
		{40000, "40 kg"},
		// Negative is not a real weight; report nothing rather than "-2 kg".
		{-2000, "0 g"},
	}

	for _, tc := range tests {
		if got := Format(tc.grams); got != tc.want {
			t.Errorf("Format(%d) = %q, want %q", tc.grams, got, tc.want)
		}
	}
}
