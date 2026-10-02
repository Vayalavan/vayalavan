module github.com/vayal-mikrogreenz/vm-events-consumer-api

go 1.25.7

// The shared package is developed in-tree. go.work handles this for local
// builds; the replace keeps the module buildable on its own (e.g. inside a
// Docker build context that has no workspace file).
replace github.com/vayal-mikrogreenz/vm-go-common => ../packages/vm-go-common

require (
	github.com/go-chi/chi/v5 v5.3.1
	github.com/google/uuid v1.6.0
	github.com/jackc/pglogrepl v0.0.0-20260824121319-4ae5c490f7ce
	github.com/jackc/pgx/v5 v5.10.0
	github.com/vayal-mikrogreenz/vm-go-common v0.0.0-00010101000000-000000000000
)

require (
	github.com/jackc/pgio v1.0.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/pressly/goose/v3 v3.27.3 // indirect
	github.com/sethvargo/go-retry v0.4.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)
