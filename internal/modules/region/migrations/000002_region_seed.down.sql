-- Tohum verisinin geri alınması.
--
-- Yalnızca tohumun EKLEDİĞİ kodlar silinir; operatörün sonradan eklediği bir
-- para birimi ya da ülke satırı yerinde kalır.
--
-- KULLANIMDAKİ tohum satırları da yerinde kalır: bir bölge hâlâ tohum para
-- birimine bağlıysa o para birimi SİLİNMEZ (aşağıdaki NOT EXISTS koşulu).
--
-- Koşulsuz bir silme burada foreign key ile PATLAR ve golang-migrate'in sürüm
-- defterini "dirty" bırakır; cmd/server her açılışta modül başına Migrate
-- çağırdığı için modül o noktadan sonra bir daha AÇILAMAZ. Modülün tek silme
-- yolu SOFT delete olduğundan operatörün FK'yi serbest bırakmasının
-- DESTEKLENEN bir yolu da yoktur.

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

-- KULLANIMDAKİ para birimleri ATLANIR.
--
-- region.currency_code bu tabloya FK ile bağlıdır ve modülün tek silme yolu
-- SOFT delete'tir: operatör API'den tüm bölgeleri silse bile satırlar tabloda
-- kalır, dolayısıyla koşulsuz bir DELETE 23503 ile PATLAR. Patlayan bir down,
-- golang-migrate'in sürüm defterini "dirty" bırakır; cmd/server her açılışta
-- modül başına Migrate çağırdığı için sunucu bir daha AÇILMAZ ve ancak elle
-- "force version" ile kurtarılır.
--
-- Kullanılmayan tohum satırları temizlenir, kullanımdakiler yerinde kalır:
-- 000001'in down'ı zaten region tablosunu düşüreceği için kalıntı bırakmaz.
DELETE FROM currency WHERE NOT EXISTS (
    SELECT 1 FROM region r WHERE r.currency_code = currency.code
) AND code IN (
    'AED', 'AUD', 'BHD', 'BRL', 'CAD', 'CHF', 'CLP', 'CNY', 'CZK', 'DKK', 'EUR', 'GBP',
    'HUF', 'IDR', 'ILS', 'INR', 'ISK', 'JOD', 'JPY', 'KRW', 'KWD', 'MXN', 'MYR', 'NOK',
    'NZD', 'OMR', 'PLN', 'QAR', 'RON', 'RUB', 'SAR', 'SEK', 'SGD', 'THB', 'TND', 'TRY',
    'TWD', 'UAH', 'USD', 'VND', 'ZAR'
);
