-- Rollback of the auth schema. The order is the reverse of the foreign key
-- dependencies: the dependent tables drop first.
DROP INDEX IF EXISTS api_key_sales_channel_channel_idx;
DROP TABLE IF EXISTS api_key_sales_channel;

DROP INDEX IF EXISTS api_key_type_idx;
DROP INDEX IF EXISTS api_key_token_hash_uniq;
DROP TABLE IF EXISTS api_key;

DROP INDEX IF EXISTS sales_channel_name_uniq;
DROP TABLE IF EXISTS sales_channel;

DROP INDEX IF EXISTS auth_identity_user_provider_uniq;
DROP INDEX IF EXISTS auth_identity_provider_uniq;
DROP TABLE IF EXISTS auth_identity;

DROP INDEX IF EXISTS auth_user_email_uniq;
DROP TABLE IF EXISTS auth_user;
