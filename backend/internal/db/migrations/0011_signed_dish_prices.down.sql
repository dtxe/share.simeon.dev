ALTER TABLE dishes DROP CONSTRAINT dishes_unit_price_cents_check;
ALTER TABLE dishes ADD CONSTRAINT dishes_unit_price_cents_check
    CHECK (unit_price_cents BETWEEN 0 AND 100000000);
