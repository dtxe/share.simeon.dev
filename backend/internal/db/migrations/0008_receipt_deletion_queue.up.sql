CREATE TABLE receipt_deletion_queue (
    id BIGSERIAL PRIMARY KEY,
    path TEXT NOT NULL CHECK (path <> '' AND path NOT LIKE '/%' AND path NOT LIKE '%..%' AND path NOT LIKE '%\\%'),
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processing_until TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX receipt_deletion_queue_path_uq ON receipt_deletion_queue (path);

CREATE INDEX receipt_deletion_queue_due_idx
    ON receipt_deletion_queue (next_attempt_at, processing_until);
