-- PR #42 migration: add instant payouts column to orders
ALTER TABLE orders ADD COLUMN IF NOT EXISTS payout_speed TEXT NOT NULL DEFAULT 'standard';
