ALTER TABLE bill_sessions ADD COLUMN tax_cents BIGINT CHECK (tax_cents IS NULL OR tax_cents BETWEEN 0 AND 500000000);
ALTER TABLE dishes ADD COLUMN taxable BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE extraction_runs ADD COLUMN tax_matched BOOLEAN, ADD COLUMN tax_diff_cents BIGINT, ADD COLUMN grand_total_matched BOOLEAN, ADD COLUMN grand_total_diff_cents BIGINT, ADD COLUMN tax_source TEXT, ADD COLUMN multiple_tax_rates_detected BOOLEAN;
ALTER TABLE extraction_attempts ADD COLUMN tax_matched BOOLEAN, ADD COLUMN tax_diff_cents BIGINT, ADD COLUMN grand_total_matched BOOLEAN, ADD COLUMN grand_total_diff_cents BIGINT, ADD COLUMN tax_source TEXT, ADD COLUMN multiple_tax_rates_detected BOOLEAN;

ALTER TABLE extraction_runs
  ADD CONSTRAINT extraction_runs_tax_diff_nonnegative CHECK (tax_diff_cents IS NULL OR tax_diff_cents >= 0),
  ADD CONSTRAINT extraction_runs_grand_total_diff_nonnegative CHECK (grand_total_diff_cents IS NULL OR grand_total_diff_cents >= 0),
  ADD CONSTRAINT extraction_runs_tax_source_valid CHECK (tax_source IS NULL OR tax_source IN ('printed', 'rate_inferred', 'total_inferred'));
ALTER TABLE extraction_attempts
  ADD CONSTRAINT extraction_attempts_tax_diff_nonnegative CHECK (tax_diff_cents IS NULL OR tax_diff_cents >= 0),
  ADD CONSTRAINT extraction_attempts_grand_total_diff_nonnegative CHECK (grand_total_diff_cents IS NULL OR grand_total_diff_cents >= 0),
  ADD CONSTRAINT extraction_attempts_tax_source_valid CHECK (tax_source IS NULL OR tax_source IN ('printed', 'rate_inferred', 'total_inferred'));
