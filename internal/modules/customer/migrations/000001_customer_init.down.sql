-- customer şemasının geri alınması. Sıra, foreign key bağımlılıklarının
-- tersidir: önce bağımlı tablolar düşer.
DROP INDEX IF EXISTS customer_address_default_billing_uniq;
DROP INDEX IF EXISTS customer_address_default_shipping_uniq;
DROP INDEX IF EXISTS customer_address_customer_idx;
DROP TABLE IF EXISTS customer_address;

DROP INDEX IF EXISTS customer_group_customer_group_idx;
DROP TABLE IF EXISTS customer_group_customer;

DROP INDEX IF EXISTS customer_group_name_uniq;
DROP TABLE IF EXISTS customer_group;

DROP INDEX IF EXISTS customer_email_idx;
DROP INDEX IF EXISTS customer_account_email_uniq;
DROP TABLE IF EXISTS customer;
