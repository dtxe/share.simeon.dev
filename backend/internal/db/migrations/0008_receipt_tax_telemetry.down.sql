ALTER TABLE extraction_attempts
  DROP CONSTRAINT extraction_attempts_tax_source_valid,
  DROP CONSTRAINT extraction_attempts_grand_total_diff_nonnegative,
  DROP CONSTRAINT extraction_attempts_tax_diff_nonnegative,
  DROP COLUMN multiple_tax_rates_detected, DROP COLUMN tax_source,
  DROP COLUMN grand_total_diff_cents, DROP COLUMN grand_total_matched,
  DROP COLUMN tax_diff_cents, DROP COLUMN tax_matched;
ALTER TABLE extraction_runs
  DROP CONSTRAINT extraction_runs_tax_source_valid,
  DROP CONSTRAINT extraction_runs_grand_total_diff_nonnegative,
  DROP CONSTRAINT extraction_runs_tax_diff_nonnegative,
  DROP COLUMN multiple_tax_rates_detected, DROP COLUMN tax_source,
  DROP COLUMN grand_total_diff_cents, DROP COLUMN grand_total_matched,
  DROP COLUMN tax_diff_cents, DROP COLUMN tax_matched;
ALTER TABLE dishes DROP COLUMN taxable;
ALTER TABLE bill_sessions DROP COLUMN tax_cents;
