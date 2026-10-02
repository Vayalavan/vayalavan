package serviceable

import "testing"

func TestPincode(t *testing.T) {
	cases := []struct {
		name string
		pin  string
		want bool
	}{
		// Bengaluru urban.
		{"bengaluru first", "560001", true},
		{"bengaluru last", "560999", true},
		{"bangalore rural is out", "561101", false},
		{"ramanagara is out", "562159", false},
		{"mysuru is out", "570001", false},

		// Tamil Nadu, both ends of the band and a few inside it.
		{"chennai", "600001", true},
		{"tamil nadu first", "600000", true},
		{"coimbatore", "641001", true},
		{"nilgiris, last band", "643001", true},
		{"tamil nadu last", "643999", true},
		{"one below the band", "599999", false},
		{"one above the band", "644001", false},
		{"kerala is out", "682001", false},

		// Inside the band but not actually Tamil Nadu — accepted knowingly.
		{"puducherry is accepted", "605001", true},
		{"karaikal is accepted", "609602", true},

		// Shape.
		{"five digits", "56000", false},
		{"seven digits", "5600012", false},
		{"leading zero", "060001", false},
		{"letters", "56000a", false},
		{"empty", "", false},
		{"spaces only", "   ", false},
		{"surrounding whitespace is trimmed", "  560001  ", true},
		{"inner space", "560 001", false},
		{"unicode digit", "56000١", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Pincode(tc.pin); got != tc.want {
				t.Fatalf("Pincode(%q) = %v, want %v", tc.pin, got, tc.want)
			}
		})
	}
}
