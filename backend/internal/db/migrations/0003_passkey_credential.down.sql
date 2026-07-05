ALTER TABLE webauthn_credentials
  DROP COLUMN credential_json,
  ADD COLUMN public_key BYTEA NOT NULL DEFAULT '\x00',
  ADD COLUMN sign_count BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN transports TEXT[];

ALTER TABLE webauthn_credentials ALTER COLUMN public_key DROP DEFAULT;
