package csvimport

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

const header = "name,type,grade,description,unit_label,weight_grams,price_rupees,image_url\n"

// row builds a well-formed line, so each test varies exactly one thing.
func row(name, ptype, grade, label, weight, price string) string {
	return fmt.Sprintf("%s,%s,%s,,%s,%s,%s,\n",
		csvField(name), csvField(ptype), csvField(grade),
		csvField(label), csvField(weight), csvField(price))
}

// csvField quotes a value that contains the delimiter, as any real CSV writer
// would. A price such as "1,200.50" is only expressible in CSV when quoted;
// unquoted it is two fields, and the row is genuinely malformed.
func csvField(v string) string {
	if strings.ContainsAny(v, `,"`) {
		return `"` + strings.ReplaceAll(v, `"`, `""`) + `"`
	}
	return v
}

func parse(t *testing.T, body string) *Report {
	t.Helper()
	report, err := Parse(strings.NewReader(body), DefaultLimits)
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	return report
}

// TestParsePriceConversion is the rupee->paise boundary. Every value here is
// one a supplier can realistically type, and getting any of them wrong
// mis-prices real produce.
func TestParsePriceConversion(t *testing.T) {
	tests := []struct {
		name      string
		price     string
		wantPaise int64
		wantError bool
	}{
		{"smallest chargeable amount", "0.01", 1, false},
		{"one rupee", "1", 100, false},
		{"one rupee with decimals", "1.00", 100, false},
		{"typical price", "45.50", 4550, false},
		{"one decimal place pads", "45.5", 4550, false},
		{"large amount", "999999.99", 99999999, false},
		{"thousands separator", "1,200.50", 120050, false},
		{"indian grouping", "1,23,456.78", 12345678, false},
		{"leading and trailing spaces", "   75.25   ", 7525, false},
		{"rupee symbol", "₹99.99", 9999, false},
		// The float trap: 0.1 + 0.2 != 0.3 in binary. Integer parsing is exact.
		{"value unrepresentable as float64", "0.10", 10, false},
		{"leading dot shorthand", ".99", 99, false},

		{"three decimal places", "10.001", 0, true},
		{"zero is not a price", "0", 0, true},
		{"zero with decimals", "0.00", 0, true},
		{"negative", "-10.00", 0, true},
		{"not a number", "abc", 0, true},
		{"empty", "", 0, true},
		{"trailing decimal point", "10.", 0, true},
		// Previously parsed to a plausible-but-wrong 95 paise.
		{"sign inside the fraction", "1.-5", 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := parse(t, header+row("Tomato", "vegetable", "A", "1 kg", "1000", tc.price))

			if tc.wantError {
				if report.ErrorRows != 1 {
					t.Fatalf("price %q: ErrorRows = %d, want 1 (report: %+v)",
						tc.price, report.ErrorRows, report.Rows)
				}
				return
			}

			if report.ValidRows != 1 {
				t.Fatalf("price %q: ValidRows = %d, want 1 (errors: %v)",
					tc.price, report.ValidRows, report.Rows[0].Errors)
			}
			if got := report.Rows[0].Parsed.PricePaise; got != tc.wantPaise {
				t.Errorf("price %q -> %d paise, want %d", tc.price, got, tc.wantPaise)
			}
		})
	}
}

// TestParseMalformedRows is the table-driven pass over bad input. Every case
// must be reported as a row error rather than crashing or silently importing.
func TestParseMalformedRows(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		wantError string
	}{
		{"missing name", row("", "vegetable", "A", "1 kg", "1000", "45.00"), "name is required"},
		{"name too long", row(strings.Repeat("x", 121), "vegetable", "A", "1 kg", "1000", "45.00"), "at most 120"},
		{"missing type", row("Tomato", "", "A", "1 kg", "1000", "45.00"), "type is required"},
		{"unknown type", row("Tomato", "legume", "A", "1 kg", "1000", "45.00"), "type must be one of"},
		{"missing unit label", row("Tomato", "vegetable", "A", "", "1000", "45.00"), "unit_label is required"},
		{"unit label too long", row("Tomato", "vegetable", "A", strings.Repeat("x", 41), "1000", "45.00"), "at most 40"},
		{"missing weight", row("Tomato", "vegetable", "A", "1 kg", "", "45.00"), "weight_grams is required"},
		{"weight not a number", row("Tomato", "vegetable", "A", "1 kg", "heavy", "45.00"), "whole number"},
		{"weight zero", row("Tomato", "vegetable", "A", "1 kg", "0", "45.00"), "greater than zero"},
		{"weight negative", row("Tomato", "vegetable", "A", "1 kg", "-5", "45.00"), "greater than zero"},
		{"weight fractional", row("Tomato", "vegetable", "A", "1 kg", "10.5", "45.00"), "whole number"},
		{"weight absurd", row("Tomato", "vegetable", "A", "1 kg", "2000000", "45.00"), "at most 1000000"},
		{"missing price", row("Tomato", "vegetable", "A", "1 kg", "1000", ""), "price_rupees is required"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := parse(t, header+tc.line)

			if report.ErrorRows != 1 || report.ValidRows != 0 {
				t.Fatalf("ErrorRows=%d ValidRows=%d, want 1/0", report.ErrorRows, report.ValidRows)
			}
			joined := strings.Join(report.Rows[0].Errors, "; ")
			if !strings.Contains(joined, tc.wantError) {
				t.Errorf("errors = %q, want something containing %q", joined, tc.wantError)
			}
			// A rejected row must never leak into the importable set.
			if len(report.Products) != 0 {
				t.Errorf("invalid row produced %d products, want 0", len(report.Products))
			}
		})
	}
}

// TestParseReportsEveryProblemOnARow — a supplier fixing a file wants the
// whole list, not one error per upload.
func TestParseReportsEveryProblemOnARow(t *testing.T) {
	report := parse(t, header+row("", "legume", "A", "", "-1", "abc"))

	if report.ErrorRows != 1 {
		t.Fatalf("ErrorRows = %d, want 1", report.ErrorRows)
	}
	if got := len(report.Rows[0].Errors); got < 4 {
		t.Errorf("reported %d problems (%v), want at least 4",
			got, report.Rows[0].Errors)
	}
}

// TestParseFileEncodingQuirks covers what real spreadsheets actually emit.
func TestParseFileEncodingQuirks(t *testing.T) {
	t.Run("UTF-8 BOM from Excel", func(t *testing.T) {
		// Left in place, the BOM becomes part of the first header name and
		// every Excel export fails with "missing column: name".
		report := parse(t, "\ufeff"+header+row("Tomato", "vegetable", "A", "1 kg", "1000", "45.00"))
		if report.ValidRows != 1 {
			t.Errorf("ValidRows = %d, want 1", report.ValidRows)
		}
	})

	t.Run("CRLF line endings", func(t *testing.T) {
		body := strings.ReplaceAll(
			header+row("Tomato", "vegetable", "A", "1 kg", "1000", "45.00"), "\n", "\r\n")
		report := parse(t, body)
		if report.ValidRows != 1 {
			t.Errorf("ValidRows = %d, want 1 (errors: %+v)", report.ValidRows, report.Rows)
		}
		if got := report.Rows[0].Parsed.Name; got != "Tomato" {
			t.Errorf("name = %q — a stray \\r survived", got)
		}
	})

	t.Run("quoted field containing commas", func(t *testing.T) {
		body := header +
			`Tomato,vegetable,A,"Sweet, juicy, and vine ripened",1 kg,1000,45.00,` + "\n"
		report := parse(t, body)
		if report.ValidRows != 1 {
			t.Fatalf("ValidRows = %d, want 1 (errors: %+v)", report.ValidRows, report.Rows)
		}
		if got := report.Rows[0].Parsed.Description; got != "Sweet, juicy, and vine ripened" {
			t.Errorf("description = %q — quoted commas were mis-split", got)
		}
	})

	t.Run("blank trailing rows are ignored", func(t *testing.T) {
		// What a spreadsheet leaves behind after deleting rows.
		body := header + row("Tomato", "vegetable", "A", "1 kg", "1000", "45.00") +
			"\n,,,,,,,\n\n,,,,,,,\n"
		report := parse(t, body)
		if report.TotalRows != 1 || report.ValidRows != 1 || report.ErrorRows != 0 {
			t.Errorf("Total=%d Valid=%d Error=%d, want 1/1/0 — blank rows were counted",
				report.TotalRows, report.ValidRows, report.ErrorRows)
		}
	})

	t.Run("blank row between data rows is skipped", func(t *testing.T) {
		body := header +
			row("Tomato", "vegetable", "A", "1 kg", "1000", "45.00") +
			",,,,,,,\n" +
			row("Okra", "vegetable", "B", "500 g", "500", "30.00")
		report := parse(t, body)
		if report.ValidRows != 2 || report.ErrorRows != 0 {
			t.Errorf("Valid=%d Error=%d, want 2/0", report.ValidRows, report.ErrorRows)
		}
	})
}

func TestParseHeaderHandling(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		wantErr error
	}{
		{
			name:   "columns in any order",
			header: "price_rupees,weight_grams,unit_label,type,name,grade,description,image_url\n",
		},
		{
			name:   "case-insensitive",
			header: "Name,TYPE,Grade,Description,Unit_Label,WEIGHT_GRAMS,Price_Rupees,Image_URL\n",
		},
		{
			name:   "optional columns omitted",
			header: "name,type,unit_label,weight_grams,price_rupees\n",
		},
		{
			name:    "missing a required column",
			header:  "name,type,unit_label,weight_grams\n",
			wantErr: ErrBadHeader,
		},
		{
			name:    "misspelled column is rejected, not ignored",
			header:  "name,type,grade,description,unit_label,weight_grams,price_rupee,image_url\n",
			wantErr: ErrBadHeader,
		},
		{
			name:    "duplicate column",
			header:  "name,name,type,unit_label,weight_grams,price_rupees\n",
			wantErr: ErrBadHeader,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// A body matching the declared column count.
			columns := strings.Count(strings.TrimSpace(tc.header), ",") + 1
			values := make([]string, columns)
			for i := range values {
				values[i] = "x"
			}
			body := tc.header + strings.Join(values, ",") + "\n"

			_, err := Parse(strings.NewReader(body), DefaultLimits)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			// A valid header may still yield row errors from the dummy values;
			// what matters is that it was not rejected as a header problem.
			if errors.Is(err, ErrBadHeader) {
				t.Fatalf("valid header rejected: %v", err)
			}
		})
	}
}

// TestParseCollapsesByNameAndGrade is the §6.5 rule: repeated name+grade rows
// become ONE product with several units.
func TestParseCollapsesByNameAndGrade(t *testing.T) {
	body := header +
		row("Tomato", "vegetable", "A", "1 kg", "1000", "45.00") +
		row("Tomato", "vegetable", "A", "3 kg", "3000", "130.50") +
		row("Tomato", "vegetable", "A", "5 kg", "5000", "210.00") +
		// Same name, DIFFERENT grade — a separate product.
		row("Tomato", "vegetable", "B", "1 kg", "1000", "35.00") +
		row("Okra", "vegetable", "A", "500 g", "500", "30.00")

	report := parse(t, body)

	if report.ValidRows != 5 {
		t.Fatalf("ValidRows = %d, want 5 (errors: %+v)", report.ValidRows, report.Rows)
	}
	if len(report.Products) != 3 {
		t.Fatalf("collapsed into %d products, want 3", len(report.Products))
	}

	first := report.Products[0]
	if first.Name != "Tomato" || first.Grade != "A" {
		t.Errorf("first product = %s/%s, want Tomato/A", first.Name, first.Grade)
	}
	if len(onlyUnits(t, first)) != 3 {
		t.Fatalf("Tomato grade A has %d units, want 3", len(onlyUnits(t, first)))
	}
	// Units are ordered by weight, and sort_order assigned to match.
	for i, want := range []int32{1000, 3000, 5000} {
		if onlyUnits(t, first)[i].WeightGrams != want {
			t.Errorf("unit %d weight = %d, want %d", i, onlyUnits(t, first)[i].WeightGrams, want)
		}
		if onlyUnits(t, first)[i].SortOrder != int32(i) {
			t.Errorf("unit %d sort_order = %d, want %d", i, onlyUnits(t, first)[i].SortOrder, i)
		}
	}
	// Grade is part of the identity: case differences must not split a product.
	if report.Products[1].Grade != "B" {
		t.Errorf("second product grade = %q, want B", report.Products[1].Grade)
	}
}

// TestParseRejectsDuplicateWeight catches at preview time what would
// otherwise violate the (product_id, weight_grams) unique constraint at
// commit time — when it is far more confusing.
func TestParseRejectsDuplicateWeight(t *testing.T) {
	body := header +
		row("Tomato", "vegetable", "A", "1 kg", "1000", "45.00") +
		row("Tomato", "vegetable", "A", "1 kilo", "1000", "46.00")

	report := parse(t, body)

	if report.ValidRows != 1 || report.ErrorRows != 1 {
		t.Fatalf("Valid=%d Error=%d, want 1/1", report.ValidRows, report.ErrorRows)
	}
	joined := strings.Join(report.Rows[1].Errors, "; ")
	if !strings.Contains(joined, "duplicate weight_grams") {
		t.Errorf("errors = %q, want a duplicate-weight message", joined)
	}
	if len(onlyUnits(t, report.Products[0])) != 1 {
		t.Errorf("product kept %d units, want 1", len(onlyUnits(t, report.Products[0])))
	}
}

func TestParseImageURLValidation(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"https", "https://example.com/tomato.jpg", false},
		{"http", "http://example.com/tomato.jpg", false},
		{"empty is allowed", "", false},
		{"ftp scheme", "ftp://example.com/tomato.jpg", true},
		// A file:// or scheme-less value would make the server fetch a local
		// path at commit time.
		{"file scheme", "file:///etc/passwd", true},
		{"no scheme", "example.com/tomato.jpg", true},
		{"no host", "https://", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			line := fmt.Sprintf("Tomato,vegetable,A,,1 kg,1000,45.00,%s\n", tc.url)
			report := parse(t, header+line)

			gotErr := report.ErrorRows == 1
			if gotErr != tc.wantErr {
				t.Errorf("url %q: error=%v, want %v (%v)",
					tc.url, gotErr, tc.wantErr, report.Rows[0].Errors)
			}
		})
	}
}

func TestParseLimits(t *testing.T) {
	t.Run("row limit", func(t *testing.T) {
		var b strings.Builder
		b.WriteString(header)
		for i := 0; i < 11; i++ {
			b.WriteString(row(fmt.Sprintf("Item%d", i), "vegetable", "A", "1 kg", "1000", "45.00"))
		}
		_, err := Parse(strings.NewReader(b.String()), Limits{MaxBytes: 1 << 20, MaxRows: 10})
		if !errors.Is(err, ErrTooManyRows) {
			t.Errorf("err = %v, want ErrTooManyRows", err)
		}
	})

	t.Run("byte limit", func(t *testing.T) {
		body := header + row("Tomato", "vegetable", "A", "1 kg", "1000", "45.00")
		_, err := Parse(strings.NewReader(body), Limits{MaxBytes: 10, MaxRows: 100})
		if !errors.Is(err, ErrTooLarge) {
			t.Errorf("err = %v, want ErrTooLarge", err)
		}
	})

	t.Run("empty file", func(t *testing.T) {
		if _, err := Parse(strings.NewReader(""), DefaultLimits); !errors.Is(err, ErrEmptyFile) {
			t.Errorf("err = %v, want ErrEmptyFile", err)
		}
	})

	t.Run("header only is valid but imports nothing", func(t *testing.T) {
		report, err := Parse(strings.NewReader(header), DefaultLimits)
		if err != nil {
			t.Fatalf("Parse returned error: %v", err)
		}
		if report.TotalRows != 0 || len(report.Products) != 0 {
			t.Errorf("Total=%d Products=%d, want 0/0", report.TotalRows, len(report.Products))
		}
	})
}

// TestTemplateCSVParsesCleanly — the file we hand suppliers must import
// without a single error, or it teaches them the wrong format.
func TestTemplateCSVParsesCleanly(t *testing.T) {
	report, err := Parse(strings.NewReader(TemplateCSV), DefaultLimits)
	if err != nil {
		t.Fatalf("template failed to parse: %v", err)
	}
	if report.ErrorRows != 0 {
		t.Fatalf("template has %d bad rows: %+v", report.ErrorRows, report.Rows)
	}
	if report.ValidRows != 4 {
		t.Errorf("ValidRows = %d, want 4", report.ValidRows)
	}
	// Demonstrates the collapsing rule: 4 rows, 3 products, Tomato with 2 units.
	if len(report.Products) != 3 {
		t.Fatalf("template produced %d products, want 3", len(report.Products))
	}
	if len(onlyUnits(t, report.Products[0])) != 2 {
		t.Errorf("template's first product has %d units, want 2 (it demonstrates collapsing)",
			len(onlyUnits(t, report.Products[0])))
	}
}

// ---------------------------------------------------------------------------
// Gallery columns (CLAUDE.md §6.5)
// ---------------------------------------------------------------------------

// mediaHeader is the full column set, image_url and video_url included.
const mediaHeader = "name,type,grade,description,unit_label,weight_grams,price_rupees,image_url,video_url\n"

// TestParseVideoURLValidation mirrors the image_url cases: a video_url is
// validated for SHAPE only, and the same schemes are refused, because both end
// up as a server-side fetch at commit time.
func TestParseVideoURLValidation(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"https", "https://example.com/tomato.mp4", false},
		{"http", "http://example.com/tomato.mp4", false},
		{"empty is allowed", "", false},
		{"ftp scheme", "ftp://example.com/tomato.mp4", true},
		// A file:// or scheme-less value would make the server fetch a local
		// path at commit time.
		{"file scheme", "file:///etc/passwd", true},
		{"no scheme", "example.com/tomato.mp4", true},
		{"no host", "https://", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			line := fmt.Sprintf("Tomato,vegetable,A,,1 kg,1000,45.00,,%s\n", tc.url)
			report := parse(t, mediaHeader+line)

			gotErr := report.ErrorRows == 1
			if gotErr != tc.wantErr {
				t.Errorf("url %q: error=%v, want %v (%v)",
					tc.url, gotErr, tc.wantErr, report.Rows[0].Errors)
			}
		})
	}
}

// The gallery is how a spreadsheet expresses several pictures: repeat the
// product row and vary the URL. Rows that collapse into one product contribute
// every DISTINCT media URL between them.
func TestParseCollectsGalleryAcrossCollapsedRows(t *testing.T) {
	csv := mediaHeader +
		"Tomato,vegetable,A,,1 kg,1000,45.00,https://example.com/a.jpg,https://example.com/clip.mp4\n" +
		"Tomato,vegetable,A,,3 kg,3000,130.50,https://example.com/b.jpg,\n" +
		// The same photograph again — the usual result of copying a row to
		// add a pack size, and not a second picture.
		"Tomato,vegetable,A,,5 kg,5000,210.00,https://example.com/a.jpg,\n"

	report := parse(t, csv)

	if len(report.Products) != 1 {
		t.Fatalf("collapsed to %d products, want 1", len(report.Products))
	}
	product := report.Products[0]

	want := []DraftMedia{
		// Images first, whatever order the rows were written in, so the first
		// entry is the one that becomes the cover.
		{Kind: "image", URL: "https://example.com/a.jpg"},
		{Kind: "image", URL: "https://example.com/b.jpg"},
		{Kind: "video", URL: "https://example.com/clip.mp4"},
	}
	if len(onlyMedia(t, product)) != len(want) {
		t.Fatalf("gallery has %d items, want %d: %+v", len(onlyMedia(t, product)), len(want), onlyMedia(t, product))
	}
	for i, item := range want {
		if onlyMedia(t, product)[i] != item {
			t.Errorf("media[%d] = %+v, want %+v", i, onlyMedia(t, product)[i], item)
		}
	}
}

// A video listed before any image must not become the first gallery entry:
// the cover is the first IMAGE, and a video can never be one.
func TestParseKeepsImagesAheadOfVideos(t *testing.T) {
	csv := mediaHeader +
		"Tomato,vegetable,A,,1 kg,1000,45.00,,https://example.com/clip.mp4\n" +
		"Tomato,vegetable,A,,3 kg,3000,130.50,https://example.com/a.jpg,\n"

	report := parse(t, csv)

	if len(report.Products) != 1 {
		t.Fatalf("collapsed to %d products, want 1", len(report.Products))
	}
	media := onlyMedia(t, report.Products[0])
	if len(media) != 2 {
		t.Fatalf("gallery has %d items, want 2: %+v", len(media), media)
	}
	if media[0].Kind != "image" || media[0].URL != "https://example.com/a.jpg" {
		t.Errorf("media[0] = %+v, want the image first", media[0])
	}
	if media[1].Kind != "video" {
		t.Errorf("media[1] = %+v, want the video second", media[1])
	}
}

// A product with no media at all stays valid: produce may be listed before
// there is a photograph of it, exactly as it could when this was one column.
func TestParseAllowsNoMedia(t *testing.T) {
	report := parse(t, mediaHeader+"Tomato,vegetable,A,,1 kg,1000,45.00,,\n")

	if report.ErrorRows != 0 {
		t.Fatalf("got %d error rows, want 0: %v", report.ErrorRows, report.Rows[0].Errors)
	}
	if len(report.Products) != 1 {
		t.Fatalf("parsed %d products, want 1", len(report.Products))
	}
	if len(onlyMedia(t, report.Products[0])) != 0 {
		t.Errorf("gallery has %d items, want 0", len(onlyMedia(t, report.Products[0])))
	}
}

// video_url is optional, so a file written before the column existed must
// still parse unchanged.
func TestParseAcceptsHeaderWithoutVideoColumn(t *testing.T) {
	report := parse(t, header+"Tomato,vegetable,A,,1 kg,1000,45.00,https://example.com/a.jpg\n")

	if report.ErrorRows != 0 {
		t.Fatalf("got %d error rows, want 0: %v", report.ErrorRows, report.Rows[0].Errors)
	}
	media := onlyMedia(t, report.Products[0])
	if len(media) != 1 || media[0].Kind != "image" {
		t.Errorf("gallery = %+v, want one image", media)
	}
}

// ---------------------------------------------------------------------------
// Helpers for files with no size_code column
// ---------------------------------------------------------------------------
//
// Such a file collapses into ONE implicit grade per product (DefaultSizeCode),
// so these read the packs and gallery of that grade. The assertions using them
// are the pre-grade behaviour, and still hold exactly.

func onlySize(t *testing.T, p DraftProduct) DraftSizeCode {
	t.Helper()
	if len(p.SizeCodes) != 1 {
		t.Fatalf("%s has %d size codes, want 1 (the implicit %q)",
			p.Name, len(p.SizeCodes), DefaultSizeCode)
	}
	if p.SizeCodes[0].Code != DefaultSizeCode {
		t.Fatalf("implicit size code is %q, want %q", p.SizeCodes[0].Code, DefaultSizeCode)
	}
	return p.SizeCodes[0]
}

func onlyUnits(t *testing.T, p DraftProduct) []DraftUnit {
	t.Helper()
	return onlySize(t, p).Units
}

func onlyMedia(t *testing.T, p DraftProduct) []DraftMedia {
	t.Helper()
	return onlySize(t, p).Media
}
