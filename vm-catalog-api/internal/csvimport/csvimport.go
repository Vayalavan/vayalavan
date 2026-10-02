// Package csvimport parses and validates the supplier product CSV
// (CLAUDE.md §6.5).
//
// Parsing is deliberately separated from persistence: this package never
// touches the database. It turns bytes into a per-row report, and the handler
// decides what to do with it. That is what makes the two-step
// validate/preview-then-commit flow possible, and what makes the rules here
// testable without a database.
package csvimport

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/vayal-mikrogreenz/vm-go-common/money"
)

// Column names, lower-cased. Order in the file does not matter; presence does.
const (
	ColName        = "name"
	ColType        = "type"
	ColGrade       = "grade"
	ColDescription = "description"
	ColUnitLabel   = "unit_label"
	ColWeightGrams = "weight_grams"
	ColPriceRupees = "price_rupees"
	ColImageURL    = "image_url"
	ColVideoURL    = "video_url"
	ColSizeCode    = "size_code"
	ColSizeMeta    = "size_meta"
)

// requiredColumns must all be present in the header.
var requiredColumns = []string{
	ColName, ColType, ColUnitLabel, ColWeightGrams, ColPriceRupees,
}

// optionalColumns may be present; anything else is rejected so a typo in a
// header ("grades") is reported rather than silently ignored.
var optionalColumns = []string{
	ColGrade, ColDescription, ColImageURL, ColVideoURL, ColSizeCode, ColSizeMeta,
}

// DefaultSizeCode is what a row with no size_code collapses under.
//
// A file written before grades existed, or by a grower who does not grade,
// still imports: every row lands in one implicit grade and the product has a
// single size code, exactly as the migration gave existing products.
const DefaultSizeCode = "STD"

// productTypes mirrors the CHECK constraint on products.type.
var productTypes = map[string]struct{}{
	"fruit": {}, "vegetable": {}, "microgreen": {}, "other": {},
}

// Limits bound an upload (CLAUDE.md §6.5).
type Limits struct {
	MaxBytes int64
	MaxRows  int
}

// DefaultLimits matches the documented defaults.
var DefaultLimits = Limits{MaxBytes: 5 << 20, MaxRows: 2000}

// ErrTooLarge and friends are surfaced to the supplier verbatim.
var (
	ErrTooLarge     = errors.New("file is larger than the maximum allowed size")
	ErrTooManyRows  = errors.New("file has more rows than the maximum allowed")
	ErrEmptyFile    = errors.New("file is empty")
	ErrBadHeader    = errors.New("header row is missing or invalid")
	ErrNoValidRows  = errors.New("no valid rows to import")
	ErrNotDelimited = errors.New("file could not be parsed as CSV")
)

// ParsedRow is one successfully-validated line.
type ParsedRow struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Grade       string `json:"grade"`
	Description string `json:"description"`
	UnitLabel   string `json:"unit_label"`
	WeightGrams int32  `json:"weight_grams"`
	// Converted from price_rupees via money.ParseRupees — integer paise, never
	// a float (CLAUDE.md rule 1).
	PricePaise int64  `json:"price_paise"`
	ImageURL   string `json:"image_url"`
	VideoURL   string `json:"video_url"`
	SizeCode   string `json:"size_code"`
	SizeMeta   string `json:"size_meta"`
}

// RowReport is the per-row entry in the preview the supplier sees.
type RowReport struct {
	// Line is the 1-based line number in the original file, header included,
	// so it matches what the supplier sees in their spreadsheet.
	Line   int        `json:"line"`
	Status string     `json:"status"` // "valid" | "error"
	Errors []string   `json:"errors,omitempty"`
	Parsed *ParsedRow `json:"parsed,omitempty"`
}

// DraftUnit is one unit of a collapsed product.
type DraftUnit struct {
	Label       string `json:"label"`
	WeightGrams int32  `json:"weight_grams"`
	PricePaise  int64  `json:"price_paise"`
	SortOrder   int32  `json:"sort_order"`
}

// DraftMedia is one gallery item a draft product asks us to fetch.
type DraftMedia struct {
	Kind string `json:"kind"` // "image" or "video"
	URL  string `json:"url"`
}

// DraftProduct is the result of collapsing rows that share name + grade
// (CLAUDE.md §6.5).
// DraftSizeCode is one grade of a collapsed product, with the gallery and the
// packs that belong to it.
type DraftSizeCode struct {
	Code string `json:"code"`
	Meta string `json:"meta"`
	// Media is every distinct image_url and video_url across the rows that
	// collapsed into THIS GRADE, in the order the file listed them. That is
	// how a spreadsheet expresses a gallery without inventing a column
	// syntax: repeat the row, vary the URL.
	//
	// Images come before videos regardless of row order, so the first entry
	// is the one that becomes the cover.
	Media []DraftMedia `json:"media"`
	Units []DraftUnit  `json:"units"`
}

// DraftProduct is the result of collapsing rows that share name + grade
// (CLAUDE.md §6.5). Within it, rows are collapsed again by size_code.
type DraftProduct struct {
	Name        string          `json:"name"`
	Type        string          `json:"type"`
	Grade       string          `json:"grade"`
	Description string          `json:"description"`
	SizeCodes   []DraftSizeCode `json:"size_codes"`
}

// Report is the full outcome of parsing one upload.
type Report struct {
	TotalRows int            `json:"total_rows"`
	ValidRows int            `json:"valid_rows"`
	ErrorRows int            `json:"error_rows"`
	Rows      []RowReport    `json:"rows"`
	Products  []DraftProduct `json:"products"`
}

// Parse reads, validates and collapses a supplier CSV.
//
// Every row is examined even after one fails: a supplier fixing a 300-row
// file wants the whole list of problems, not the first one.
func Parse(r io.Reader, limits Limits) (*Report, error) {
	if limits.MaxBytes <= 0 {
		limits = DefaultLimits
	}

	// One byte over the limit is enough to know it is too large, without
	// buffering the whole oversized file.
	limited := io.LimitReader(r, limits.MaxBytes+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("csvimport: reading upload: %w", err)
	}
	if int64(len(raw)) > limits.MaxBytes {
		return nil, ErrTooLarge
	}

	// Excel writes a UTF-8 BOM. Left in place it becomes part of the first
	// header name, so "name" would not match and every upload from Excel
	// would fail with a baffling "missing column: name".
	raw = trimBOM(raw)
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, ErrEmptyFile
	}

	reader := csv.NewReader(strings.NewReader(string(raw)))
	// Rows legitimately vary in length when trailing optional columns are
	// omitted; we validate field counts ourselves with a better message.
	reader.FieldsPerRecord = -1
	// Bare quotes inside an unquoted field are common in hand-edited files
	// (5" pot). Tolerating them beats rejecting the upload.
	reader.LazyQuotes = true
	// encoding/csv already normalises CRLF to LF inside records.
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotDelimited, err)
	}
	if len(records) == 0 {
		return nil, ErrEmptyFile
	}

	index, err := parseHeader(records[0])
	if err != nil {
		return nil, err
	}

	body := records[1:]
	// Trailing blank lines are what a spreadsheet leaves behind after a
	// delete; they are not rows the supplier meant to submit.
	body = trimTrailingBlankRows(body)

	if len(body) > limits.MaxRows {
		return nil, fmt.Errorf("%w (%d rows, limit %d)", ErrTooManyRows, len(body), limits.MaxRows)
	}

	report := &Report{Rows: make([]RowReport, 0, len(body))}

	// Keyed by name+grade so repeat rows collapse into one product with
	// several units.
	type productKey struct{ name, grade string }
	// A grade WITHIN a product. Two products may both have an 'M'; they are
	// different gram pools, so they are different keys.
	type sizeKey struct {
		product productKey
		code    string
	}
	drafts := map[productKey]*DraftProduct{}
	var order []productKey
	// Duplicate weights within one product's GRADE would violate the unique
	// constraint at commit time; catching it here keeps the failure in the
	// preview where it can be fixed. The same weight under a different grade
	// is fine, and is the point of size codes — so the key is (product, grade,
	// weight), not (product, weight).
	seenWeights := map[sizeKey]map[int32]int{}
	// Per-GRADE set of media URLs already taken, so copied rows do not attach
	// the same photograph several times.
	seenMedia := map[sizeKey]map[string]struct{}{}
	// Where each grade lives inside its product, so rows find it again.
	sizeIndex := map[sizeKey]int{}

	for i, record := range body {
		// +2: one for the header, one for 1-based numbering.
		line := i + 2

		if isBlankRecord(record) {
			continue
		}
		report.TotalRows++

		parsed, rowErrors := parseRow(record, index)
		if len(rowErrors) > 0 {
			report.ErrorRows++
			report.Rows = append(report.Rows, RowReport{
				Line: line, Status: "error", Errors: rowErrors,
			})
			continue
		}

		key := productKey{
			name:  strings.ToLower(parsed.Name),
			grade: strings.ToLower(parsed.Grade),
		}
		sk := sizeKey{product: key, code: strings.ToLower(strings.TrimSpace(parsed.SizeCode))}

		if prior, dup := seenWeights[sk][parsed.WeightGrams]; dup {
			report.ErrorRows++
			report.Rows = append(report.Rows, RowReport{
				Line:   line,
				Status: "error",
				Errors: []string{fmt.Sprintf(
					"duplicate weight_grams %d for size code %q of this product "+
						"(already on line %d)",
					parsed.WeightGrams, parsed.SizeCode, prior)},
			})
			continue
		}

		report.ValidRows++
		report.Rows = append(report.Rows, RowReport{
			Line: line, Status: "valid", Parsed: parsed,
		})

		draft, exists := drafts[key]
		if !exists {
			draft = &DraftProduct{
				Name:        parsed.Name,
				Type:        parsed.Type,
				Grade:       parsed.Grade,
				Description: parsed.Description,
			}
			drafts[key] = draft
			order = append(order, key)
		}
		// Later rows may carry detail the first omitted.
		if draft.Description == "" {
			draft.Description = parsed.Description
		}

		// Second-level collapse: rows sharing a size_code within one product
		// become one grade with several packs, exactly as rows sharing
		// name+grade become one product.
		at, seenSize := sizeIndex[sk]
		if !seenSize {
			draft.SizeCodes = append(draft.SizeCodes, DraftSizeCode{
				Code: parsed.SizeCode,
				Meta: parsed.SizeMeta,
			})
			at = len(draft.SizeCodes) - 1
			sizeIndex[sk] = at
			seenWeights[sk] = map[int32]int{}
			seenMedia[sk] = map[string]struct{}{}
		}
		size := &draft.SizeCodes[at]
		if size.Meta == "" {
			size.Meta = parsed.SizeMeta
		}

		// Every row contributes its media to ITS GRADE. Deduped by URL,
		// because the usual way to write three pack sizes is to copy the row
		// and change the weight — which repeats the same photograph three
		// times.
		addMedia(size, seenMedia[sk], "image", parsed.ImageURL)
		addMedia(size, seenMedia[sk], "video", parsed.VideoURL)

		seenWeights[sk][parsed.WeightGrams] = line
		size.Units = append(size.Units, DraftUnit{
			Label:       parsed.UnitLabel,
			WeightGrams: parsed.WeightGrams,
			PricePaise:  parsed.PricePaise,
		})
	}

	// Preserve first-seen order so the preview matches the file: products in
	// the order they first appear, grades in the order they first appear
	// within each, packs smallest first.
	for _, key := range order {
		draft := drafts[key]
		for si := range draft.SizeCodes {
			size := &draft.SizeCodes[si]
			sort.SliceStable(size.Units, func(a, b int) bool {
				return size.Units[a].WeightGrams < size.Units[b].WeightGrams
			})
			for i := range size.Units {
				size.Units[i].SortOrder = int32(i)
			}
		}
		report.Products = append(report.Products, *draft)
	}

	return report, nil
}

// trimBOM removes a leading UTF-8 byte order mark.
func trimBOM(b []byte) []byte {
	const bom = "\ufeff"
	return []byte(strings.TrimPrefix(string(b), bom))
}

func isBlankRecord(record []string) bool {
	for _, field := range record {
		if strings.TrimSpace(field) != "" {
			return false
		}
	}
	return true
}

func trimTrailingBlankRows(records [][]string) [][]string {
	end := len(records)
	for end > 0 && isBlankRecord(records[end-1]) {
		end--
	}
	return records[:end]
}

// parseHeader maps column names to positions, case-insensitively and in any
// order (CLAUDE.md §6.5).
func parseHeader(header []string) (map[string]int, error) {
	index := map[string]int{}
	known := map[string]struct{}{}
	for _, c := range append(append([]string{}, requiredColumns...), optionalColumns...) {
		known[c] = struct{}{}
	}

	var unknown []string
	for position, rawName := range header {
		name := strings.ToLower(strings.TrimSpace(rawName))
		if name == "" {
			continue
		}
		if _, ok := known[name]; !ok {
			unknown = append(unknown, rawName)
			continue
		}
		if _, dup := index[name]; dup {
			return nil, fmt.Errorf("%w: column %q appears more than once", ErrBadHeader, name)
		}
		index[name] = position
	}

	var missing []string
	for _, required := range requiredColumns {
		if _, ok := index[required]; !ok {
			missing = append(missing, required)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: missing required column(s): %s",
			ErrBadHeader, strings.Join(missing, ", "))
	}
	if len(unknown) > 0 {
		// Rejected rather than ignored: a misspelled header means the
		// supplier's data is not going where they think it is.
		return nil, fmt.Errorf("%w: unrecognised column(s): %s",
			ErrBadHeader, strings.Join(unknown, ", "))
	}
	return index, nil
}

// field returns a trimmed value for a column, or "" when absent.
func field(record []string, index map[string]int, column string) string {
	position, ok := index[column]
	if !ok || position >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[position])
}

// parseRow validates one record, returning every problem it finds.
func parseRow(record []string, index map[string]int) (*ParsedRow, []string) {
	var problems []string

	row := &ParsedRow{
		Name:        field(record, index, ColName),
		Type:        strings.ToLower(field(record, index, ColType)),
		Grade:       field(record, index, ColGrade),
		Description: field(record, index, ColDescription),
		UnitLabel:   field(record, index, ColUnitLabel),
		ImageURL:    field(record, index, ColImageURL),
		VideoURL:    field(record, index, ColVideoURL),
		SizeCode:    field(record, index, ColSizeCode),
		SizeMeta:    field(record, index, ColSizeMeta),
	}
	if row.SizeCode == "" {
		row.SizeCode = DefaultSizeCode
	}

	switch {
	case row.Name == "":
		problems = append(problems, "name is required")
	case len([]rune(row.Name)) > 120:
		problems = append(problems, "name must be at most 120 characters")
	}

	if row.Type == "" {
		problems = append(problems, "type is required")
	} else if _, ok := productTypes[row.Type]; !ok {
		problems = append(problems,
			"type must be one of fruit, vegetable, microgreen, other")
	}

	if row.UnitLabel == "" {
		problems = append(problems, "unit_label is required")
	} else if len([]rune(row.UnitLabel)) > 40 {
		problems = append(problems, "unit_label must be at most 40 characters")
	}

	rawWeight := field(record, index, ColWeightGrams)
	switch {
	case rawWeight == "":
		problems = append(problems, "weight_grams is required")
	default:
		// Spreadsheets emit "5000.0" for an integer cell; accept it when the
		// fraction is zero rather than failing a file that is semantically fine.
		normalised := strings.TrimSuffix(strings.TrimSuffix(rawWeight, ".0"), ".00")
		normalised = strings.ReplaceAll(normalised, ",", "")
		weight, err := strconv.Atoi(normalised)
		switch {
		case err != nil:
			problems = append(problems, "weight_grams must be a whole number")
		case weight <= 0:
			problems = append(problems, "weight_grams must be greater than zero")
		case weight > 1_000_000:
			problems = append(problems, "weight_grams must be at most 1000000 (1000 kg)")
		default:
			row.WeightGrams = int32(weight)
		}
	}

	rawPrice := field(record, index, ColPriceRupees)
	if rawPrice == "" {
		problems = append(problems, "price_rupees is required")
	} else {
		// money.ParseRupees is the single rupee->paise conversion for the whole
		// platform: it rejects >2 decimals and never goes through a float.
		paise, err := money.ParseRupees(rawPrice)
		switch {
		case err != nil:
			problems = append(problems,
				"price_rupees must be a number with at most 2 decimal places")
		case paise <= 0:
			problems = append(problems, "price_rupees must be greater than zero")
		default:
			row.PricePaise = paise.Int64()
		}
	}

	if len([]rune(row.SizeCode)) > 20 {
		problems = append(problems, "size_code must be at most 20 characters")
	}
	if len([]rune(row.SizeMeta)) > 80 {
		problems = append(problems, "size_meta must be at most 80 characters")
	}

	if row.ImageURL != "" {
		if err := validateMediaURL(ColImageURL, row.ImageURL); err != nil {
			problems = append(problems, err.Error())
		}
	}

	if row.VideoURL != "" {
		if err := validateMediaURL(ColVideoURL, row.VideoURL); err != nil {
			problems = append(problems, err.Error())
		}
	}

	if len(problems) > 0 {
		return nil, problems
	}
	return row, nil
}

// addMedia appends one media URL to a draft, skipping blanks and URLs the
// draft already carries. seen is the draft's own set of URLs.
//
// Images are kept ahead of videos so the first entry is always the cover if
// the product has any image at all, whatever order the rows were written in.
func addMedia(draft *DraftSizeCode, seen map[string]struct{}, kind, raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	if _, done := seen[raw]; done {
		return
	}
	seen[raw] = struct{}{}

	item := DraftMedia{Kind: kind, URL: raw}
	if kind != "image" {
		draft.Media = append(draft.Media, item)
		return
	}
	// Insert after the last image, before the first video.
	at := 0
	for at < len(draft.Media) && draft.Media[at].Kind == "image" {
		at++
	}
	draft.Media = append(draft.Media, DraftMedia{})
	copy(draft.Media[at+1:], draft.Media[at:])
	draft.Media[at] = item
}

// validateMediaURL checks that an image_url or video_url is a fetchable
// http(s) address.
//
// Only the shape is checked here. The URL is fetched and re-uploaded to our
// own storage at commit time (CLAUDE.md §6.5), which is where size and
// content-type limits apply.
func validateMediaURL(column, raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return errors.New(column + " is not a valid URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New(column + " must start with http:// or https://")
	}
	if parsed.Host == "" {
		return errors.New(column + " is missing a host")
	}
	return nil
}

// TemplateCSV is the downloadable starter file offered in the supplier UI
// (CLAUDE.md §6.5).
//
// It ships two rows for the same product at different weights, because the
// name+grade collapsing rule is the part suppliers most often get wrong and
// an example teaches it faster than help text.
const TemplateCSV = `name,type,grade,description,unit_label,weight_grams,price_rupees,image_url,video_url
Tomato,vegetable,A,Vine ripened and hand picked,1 kg,1000,45.00,,
Tomato,vegetable,A,Vine ripened and hand picked,3 kg,3000,130.50,,
Sunflower Microgreens,microgreen,Premium,Harvested the morning of delivery,100 g,100,120.00,,
Alphonso Mango,fruit,Export,Ratnagiri Alphonso,1 box,2500,1200.50,,
`
