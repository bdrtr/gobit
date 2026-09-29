ALTER TABLE order_addresses
    ALTER COLUMN created_at SET DEFAULT now();
ALTER TABLE order_delivery_changes
    ALTER COLUMN created_at SET DEFAULT now();
