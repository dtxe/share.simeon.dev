-- 0007: Normalize extraction telemetry.
--
-- Renames the existing extraction_runs table to extraction_attempts and
-- creates a new parent extraction_runs table. Each historical row becomes a
-- parent run with one child attempt, using the original row's UUID for both.
--
-- Order of operations:
--  1. Rename existing table to extraction_attempts.
--  2. Rename the old index.
--  3. Create new parent extraction_runs.
--  4. Add nullable run_id FK to extraction_attempts.
--  5. Backfill: one parent per historical attempt (same UUID).
--  6. Set run_id NOT NULL.
--  7. Add UNIQUE(run_id, attempt).
--  8. Index extraction_attempts by run_id.
--  9. Drop normalized-out columns from extraction_attempts.

-- (Step 1)
ALTER TABLE extraction_runs RENAME TO extraction_attempts;

-- (Step 2)
ALTER INDEX extraction_runs_pkey RENAME TO extraction_attempts_pkey;
ALTER INDEX idx_extraction_runs_session RENAME TO idx_extraction_attempts_session;

-- (Step 3)
CREATE TABLE extraction_runs (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id               UUID NOT NULL REFERENCES bill_sessions(id) ON DELETE CASCADE,
    strategy                 TEXT NOT NULL,
    status                   TEXT NOT NULL CHECK (status IN ('running', 'success', 'error', 'rejected')),
    error_message            TEXT,
    subtotal_matched         BOOLEAN,
    subtotal_diff_cents      BIGINT,
    max_calls                INT NOT NULL CHECK (max_calls > 0),
    receipt_cap_cents        INT NOT NULL CHECK (receipt_cap_cents >= 0),
    reserved_cents           INT NOT NULL CHECK (reserved_cents >= 0),
    reservation_accepted     BOOLEAN NOT NULL DEFAULT false,
    known_actual_cost_cents  INT CHECK (known_actual_cost_cents >= 0),
    accounted_cost_cents     INT CHECK (accounted_cost_cents >= 0),
    attempt_count            INT NOT NULL CHECK (attempt_count >= 0),
    spend_reconciled         BOOLEAN NOT NULL DEFAULT false,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at             TIMESTAMPTZ
);

CREATE INDEX idx_extraction_runs_session ON extraction_runs(session_id);

-- (Step 4) nullable FK initially for backfill
ALTER TABLE extraction_attempts ADD COLUMN run_id UUID REFERENCES extraction_runs(id);
ALTER TABLE extraction_attempts ALTER COLUMN subtotal_diff_cents TYPE BIGINT;

-- (Step 5) backfill: one parent per historical attempt, reusing the same UUID
INSERT INTO extraction_runs (
    id, session_id, strategy, status, error_message,
    subtotal_matched, subtotal_diff_cents, max_calls,
    receipt_cap_cents, reserved_cents, known_actual_cost_cents,
    accounted_cost_cents, attempt_count, spend_reconciled,
    created_at, completed_at
)
SELECT
    ea.id,
    ea.session_id,
    ea.strategy,
    CASE WHEN ea.status = 'error' THEN 'error' ELSE 'success' END,
    ea.error_message,
    ea.subtotal_matched,
    ea.subtotal_diff_cents,
    1,
    0,     -- receipt_cap_cents unknown
    0,     -- reserved_cents unknown
    ea.cost_cents,
    ea.cost_cents,
    1,     -- each historical row is a single-attempt run
    true,
    ea.created_at,
    ea.created_at
FROM extraction_attempts ea;

UPDATE extraction_attempts SET run_id = id;

-- (Step 6)
ALTER TABLE extraction_attempts ALTER COLUMN run_id SET NOT NULL;

-- (Step 7)
ALTER TABLE extraction_attempts ADD CONSTRAINT extraction_attempts_run_id_attempt_key UNIQUE (run_id, attempt);

-- (Step 8)
CREATE INDEX idx_extraction_attempts_run ON extraction_attempts(run_id);

-- (Step 9) drop columns now owned by the parent
ALTER TABLE extraction_attempts DROP COLUMN session_id;
ALTER TABLE extraction_attempts DROP COLUMN strategy;

ALTER TABLE extraction_attempts ADD CONSTRAINT extraction_attempts_cost_cents_nonnegative
    CHECK (cost_cents IS NULL OR cost_cents >= 0);
