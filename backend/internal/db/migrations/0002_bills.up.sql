CREATE TABLE bill_sessions (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_user_id       UUID REFERENCES users(id) ON DELETE SET NULL,
  title               TEXT,
  restaurant_name     TEXT,
  bill_date           DATE,
  subtotal_cents      BIGINT NOT NULL DEFAULT 0,
  total_paid_cents    BIGINT CHECK (total_paid_cents IS NULL OR total_paid_cents >= 0),
  receipt_image_path  TEXT,
  view_token_hash     BYTEA UNIQUE,
  extract_count       INT NOT NULL DEFAULT 0,
  expires_at          TIMESTAMPTZ NOT NULL DEFAULT (now() + interval '60 days'),
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_bill_sessions_owner ON bill_sessions(owner_user_id);
CREATE INDEX idx_bill_sessions_expires_at ON bill_sessions(expires_at);

CREATE TABLE people (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id  UUID NOT NULL REFERENCES bill_sessions(id) ON DELETE CASCADE,
  name        TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 50),
  sort_order  INT NOT NULL DEFAULT 0,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_people_session ON people(session_id);

CREATE TABLE dishes (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id  UUID NOT NULL REFERENCES bill_sessions(id) ON DELETE CASCADE,
  name        TEXT NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
  unit_price_cents BIGINT NOT NULL CHECK (unit_price_cents BETWEEN 0 AND 100000000),
  quantity    NUMERIC(10,2) NOT NULL DEFAULT 1 CHECK (quantity > 0),
  sort_order  INT NOT NULL DEFAULT 0,
  source      TEXT NOT NULL DEFAULT 'manual' CHECK (source IN ('manual', 'llm_extracted')),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_dishes_session ON dishes(session_id);

CREATE TABLE portions (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  dish_id     UUID NOT NULL REFERENCES dishes(id) ON DELETE CASCADE,
  person_id   UUID NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  shares      NUMERIC(6,2) NOT NULL DEFAULT 0 CHECK (shares >= 0 AND shares <= 100),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (dish_id, person_id)
);
CREATE INDEX idx_portions_dish ON portions(dish_id);
CREATE INDEX idx_portions_person ON portions(person_id);

CREATE TABLE extraction_runs (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  session_id    UUID NOT NULL REFERENCES bill_sessions(id) ON DELETE CASCADE,
  provider      TEXT NOT NULL,
  model         TEXT NOT NULL,
  raw_response  JSONB,
  status        TEXT NOT NULL CHECK (status IN ('success', 'error')),
  error_message TEXT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_extraction_runs_session ON extraction_runs(session_id);
