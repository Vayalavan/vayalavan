-- A line of detail on a PACK, the way a size code already has one.
--
-- A pack's label is short by intent — "1 kg box", "250 g" — because it is a
-- chip in a selector. That leaves nowhere to say the things a customer asks
-- before buying produce by the box: how many fruit are in it, whether it is
-- the export grade, that it travels in a ventilated carton. Growers were
-- either padding the label until it stopped fitting the chip, or leaving it
-- unsaid.
--
--     size code    L2   meta "260 g +"          <- the GRADE's detail
--       pack       2 kg box  meta "6-8 fruit"   <- THIS pack's detail
--
-- Same shape and same cap as product_size_codes.meta, deliberately: one idea,
-- one name, one length limit, so a supplier meets the same "Detail (optional)"
-- field at both levels and neither can hold a paragraph.
--
-- Free text, never parsed. The weight that decides stock is weight_grams and
-- the money is price_paise; nothing here is read by any calculation.

-- +goose Up

ALTER TABLE product_pack_options
    ADD COLUMN meta TEXT
        CHECK (meta IS NULL OR length(btrim(meta)) <= 80);

COMMENT ON COLUMN product_pack_options.meta IS
    'Optional customer-facing detail for this pack: "6-8 fruit", "ventilated carton". Free text, never parsed.';

-- +goose Down

ALTER TABLE product_pack_options DROP COLUMN meta;
