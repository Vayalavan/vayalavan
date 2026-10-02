# Storefront photography

The pictures behind the customer app's hero, its category tiles and the "Why
choose Vayalavan?" panel. The last of those is a field with farmers working in
it, which is not decoration but the name: a *vayalavan* is the one who works
the field. They are **decoration, not product media** —
produce photographs belong to a grower's listing and live in MinIO
(CLAUDE.md §5.2). These are part of the brand, so they live with the logo and
are synced into the apps by `make -C infra sync-brand`.

## Licence

The stock ones are **CC0 / public domain**: free for commercial use, no
attribution required, no share-alike obligation. That was the filter, not a
happy accident — a shop front is the last place to inherit a licence that
could be revoked.

`why-master.jpg` is the exception and it is **NOT CLEARED FOR USE**. It is
supplied by the Vayalavan team and its origin has not been stated. The file it
replaced came from an Instagram post, which is a useful thing to remember:
a photograph published on social media is all-rights-reserved to whoever took
it unless they have said otherwise, and nothing about it being public licenses
a shop to put it on its home page.

Before this goes anywhere near production, one of these has to be true:

1. written permission from the account holder / photographer to use it
   commercially — an email or a DM saved somewhere findable, not a memory of a
   conversation; or
2. the file is replaced with something licensed, in which case whatever that
   licence obliges (a credit, usually) goes back on the About page.

It replaced a CC BY 4.0 photograph, and the credit that licence required came
off the About page with it — a credit naming a photographer whose work the
site no longer shows is worse than none.

Sources are recorded for all of them, because "where did this come from" is a
question somebody will ask a year from now and nobody will remember.

| File | Subject | Creator | Source |
|---|---|---|---|
| `hero-master.jpg` | Carrots on weathered wood | Markus Spiske | rawpixel.com/image/432830 |
| `why-master.jpg` | A farmer spraying a paddy field | unknown — see the warning above | supplied by the Vayalavan team, source unstated (**rights not cleared**) |
| `cat-all-master.jpg` | Mixed vegetables in a basket | — | rawpixel.com/image/5965842 |
| `cat-fruit-master.jpg` | Pomegranate on dark ground | Markus Spiske | rawpixel.com/image/527325 |
| `cat-vegetable-master.jpg` | Red peppers | U.S. Department of Agriculture | rawpixel.com/image/8731896 |
| `cat-microgreen-master.jpg` | Sunflower microgreens | — | flickr.com (public domain mark) |
| `cat-other-master.jpg` | Potted herbs on a board | — | rawpixel.com/image/5965371 |

Each was opened and looked at before being committed, which is how a candidate
carrying a visible stock-library watermark was caught and dropped. Do the same
with any replacement: a title from a search API describes what someone typed,
not what is in the frame — one of these was filed as "heirloom tomatoes" and is
a pile of peppers.

## Replacing one

Drop the new master in with the same filename, then run
`make -C infra sync-brand`. Every app picks it up at the size it needs; nothing
references these files by any other path.
