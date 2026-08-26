ALTER TABLE bill_sessions
  ADD COLUMN notes TEXT CHECK (notes IS NULL OR char_length(notes) <= 500);
