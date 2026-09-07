-- Rolling the seed data back.
--
-- Only the codes the seed ITSELF inserted are deleted; a currency or country row
-- the operator added later stays where it is.
--
-- Seed rows that are IN USE also stay where they are: if a region is still bound
-- to a seeded currency, that currency is NOT DELETED (the NOT EXISTS condition
-- below).
--
-- An unconditional delete BLOWS UP here on the foreign key and leaves
-- golang-migrate's version ledger "dirty"; because cmd/server calls Migrate per
-- module on every start, from that point on the module can never be OPENED
-- again. And since the module's only delete path is a SOFT delete, there is no
-- SUPPORTED way for the operator to release the FK either.

DELETE FROM country WHERE iso_2 IN (
    'AD', 'AE', 'AF', 'AG', 'AI', 'AL', 'AM', 'AO', 'AQ', 'AR', 'AS', 'AT',
    'AU', 'AW', 'AX', 'AZ', 'BA', 'BB', 'BD', 'BE', 'BF', 'BG', 'BH', 'BI',
    'BJ', 'BL', 'BM', 'BN', 'BO', 'BQ', 'BR', 'BS', 'BT', 'BV', 'BW', 'BY',
    'BZ', 'CA', 'CC', 'CD', 'CF', 'CG', 'CH', 'CI', 'CK', 'CL', 'CM', 'CN',
    'CO', 'CR', 'CU', 'CV', 'CW', 'CX', 'CY', 'CZ', 'DE', 'DJ', 'DK', 'DM',
    'DO', 'DZ', 'EC', 'EE', 'EG', 'EH', 'ER', 'ES', 'ET', 'FI', 'FJ', 'FK',
    'FM', 'FO', 'FR', 'GA', 'GB', 'GD', 'GE', 'GF', 'GG', 'GH', 'GI', 'GL',
    'GM', 'GN', 'GP', 'GQ', 'GR', 'GS', 'GT', 'GU', 'GW', 'GY', 'HK', 'HM',
    'HN', 'HR', 'HT', 'HU', 'ID', 'IE', 'IL', 'IM', 'IN', 'IO', 'IQ', 'IR',
    'IS', 'IT', 'JE', 'JM', 'JO', 'JP', 'KE', 'KG', 'KH', 'KI', 'KM', 'KN',
    'KP', 'KR', 'KW', 'KY', 'KZ', 'LA', 'LB', 'LC', 'LI', 'LK', 'LR', 'LS',
    'LT', 'LU', 'LV', 'LY', 'MA', 'MC', 'MD', 'ME', 'MF', 'MG', 'MH', 'MK',
    'ML', 'MM', 'MN', 'MO', 'MP', 'MQ', 'MR', 'MS', 'MT', 'MU', 'MV', 'MW',
    'MX', 'MY', 'MZ', 'NA', 'NC', 'NE', 'NF', 'NG', 'NI', 'NL', 'NO', 'NP',
    'NR', 'NU', 'NZ', 'OM', 'PA', 'PE', 'PF', 'PG', 'PH', 'PK', 'PL', 'PM',
    'PN', 'PR', 'PS', 'PT', 'PW', 'PY', 'QA', 'RE', 'RO', 'RS', 'RU', 'RW',
    'SA', 'SB', 'SC', 'SD', 'SE', 'SG', 'SH', 'SI', 'SJ', 'SK', 'SL', 'SM',
    'SN', 'SO', 'SR', 'SS', 'ST', 'SV', 'SX', 'SY', 'SZ', 'TC', 'TD', 'TF',
    'TG', 'TH', 'TJ', 'TK', 'TL', 'TM', 'TN', 'TO', 'TR', 'TT', 'TV', 'TW',
    'TZ', 'UA', 'UG', 'UM', 'US', 'UY', 'UZ', 'VA', 'VC', 'VE', 'VG', 'VI',
    'VN', 'VU', 'WF', 'WS', 'YE', 'YT', 'ZA', 'ZM', 'ZW'
);

-- Currencies IN USE are SKIPPED.
--
-- region.currency_code is bound to this table by an FK and the module's only
-- delete path is a SOFT delete: even if the operator deletes every region
-- through the API the rows stay in the table, so an unconditional DELETE BLOWS
-- UP with 23503. A down that blows up leaves golang-migrate's version ledger
-- "dirty"; because cmd/server calls Migrate per module on every start, the
-- server never OPENS again and can only be recovered by a manual "force
-- version".
--
-- The unused seed rows are cleaned up and the ones in use stay where they are:
-- 000001's down will drop the region table anyway, so nothing is left behind.
DELETE FROM currency WHERE NOT EXISTS (
    SELECT 1 FROM region r WHERE r.currency_code = currency.code
) AND code IN (
    'AED', 'AUD', 'BHD', 'BRL', 'CAD', 'CHF', 'CLP', 'CNY', 'CZK', 'DKK', 'EUR', 'GBP',
    'HUF', 'IDR', 'ILS', 'INR', 'ISK', 'JOD', 'JPY', 'KRW', 'KWD', 'MXN', 'MYR', 'NOK',
    'NZD', 'OMR', 'PLN', 'QAR', 'RON', 'RUB', 'SAR', 'SEK', 'SGD', 'THB', 'TND', 'TRY',
    'TWD', 'UAH', 'USD', 'VND', 'ZAR'
);
