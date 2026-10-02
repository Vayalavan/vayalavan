# mocks/ — development seed data

Everything the development seed needs that is not plain SQL: supplier
catalogues, with the photos and videos their products show.

```
mocks/
  suppliers/
    nilgiri-microfarms/
      catalog.json        products → grades (size codes) → packs, + media file names
      media/              the photos and video those products show
```

## How it is used

`make -C infra seed` (also run by `./scripts/dev-setup.sh` and `--reset`):

1. `infra/seed/*.sql` creates the accounts: the admin, the analyst, the
   customer, and the supplier **Nilgiri Microfarms**, with the fixed id that
   `catalog.json` names.
2. `vm-catalog-api -seed-mocks mocks/` reads every
   `suppliers/*/catalog.json`, uploads each file to MinIO at
   `products/<supplier id>/<file name>`, and creates each product through the
   same code a supplier's own save uses (grades, packs, galleries, and the
   analytics event).

Re-running is safe. A product the supplier already has (same name and grade,
not archived) is left as it is, with any edits, so the seed only fills in
what is missing.

## catalog.json

```jsonc
{
  "supplier": { "id": "<uuid — must match infra/seed/02_identity.sql>", "business_name": "…" },
  "products": [{
    "name": "Pomegranate", "type": "fruit", "grade": "A",
    "description": "…", "status": "active", "markup_bps": 1000,
    "media": [ { "file": "media/pomegranate-1.png", "kind": "image" } ],   // whole-product gallery
    "size_codes": [{
      "code": "M", "meta": "…", "is_active": true, "harvest_share_pct": 20,
      "media": [],                                                          // this grade's own photos
      "packs": [ { "label": "1 Kg Box", "meta": null, "weight_grams": 1000,
                   "price_paise": 25000, "is_active": true } ]
    }]
  }]
}
```

- **Prices** are integer paise, as everywhere else (CLAUDE.md rule 1).
- **Order matters:** file order is gallery order, and the first image is the
  cover (CLAUDE.md §5.2).
- **File types** are PNG, JPEG, WebP, MP4, MOV and WebM.

## Adding to it

- **A product:** add an entry to `catalog.json` and put its files in `media/`.
- **A supplier:** add its account to `infra/seed/02_identity.sql` with a fixed
  id, and a `suppliers/<slug>/` folder whose `catalog.json` uses that id.

Everything here is committed and public. Use produce photos only: no faces,
no home addresses, and nothing with location data (strip EXIF GPS from phone
photos first).
