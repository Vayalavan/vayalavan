package api

import "github.com/vayal-mikrogreenz/vm-go-common/httpx"

// validation accumulates field errors so a client sees every problem at once,
// rather than one per submission.
type validation struct{ fields map[string]any }

func newValidation() *validation { return &validation{fields: map[string]any{}} }

func (v *validation) add(field string, message any) { v.fields[field] = message }

// err returns a 422 carrying every field problem, or nil when clean.
func (v *validation) err() error {
	if len(v.fields) == 0 {
		return nil
	}
	return httpx.Validation("Some fields need attention.", v.fields)
}
