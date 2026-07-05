-- Short-lived storage for in-flight WebAuthn ceremonies (the challenge the
-- server generated, awaiting the browser's response). Stored in Postgres
-- rather than in-memory so a hot-reload/restart mid-ceremony doesn't strand
-- the user — these rows are expected to live for well under a minute.
CREATE TABLE webauthn_ceremonies (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  kind         TEXT NOT NULL CHECK (kind IN ('registration', 'login')),
  user_id      UUID REFERENCES users(id) ON DELETE CASCADE, -- null for discoverable login
  session_data JSONB NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_webauthn_ceremonies_expires_at ON webauthn_ceremonies(expires_at);
