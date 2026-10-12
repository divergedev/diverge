-- Baseline orders table for checkout-api
CREATE TABLE IF NOT EXISTS orders (
  id UUID PRIMARY KEY,
  amount_cents INT NOT NULL,
  currency TEXT NOT NULL DEFAULT 'USD',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
