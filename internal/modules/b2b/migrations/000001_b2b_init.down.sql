-- Rolling the b2b schema back. The order is the reverse of the foreign key
-- dependencies: the employee table falls first, then the company.
--
-- The link table ("link_b2b_employee_customer") is NOT DROPPED HERE, and that
-- is deliberate: the link schema is the product of the declaration made at
-- startup, not of a migration (ADR 0005), and core/link owns it. For this file
-- to drop it would be one module's migration deleting another subsystem's
-- table — and even after b2b has been rolled back the rows in that table are
-- harmless, because the employee ids they point at are never minted again.
DROP INDEX IF EXISTS b2b_company_employee_company_idx;
DROP TABLE IF EXISTS b2b_company_employee;

DROP INDEX IF EXISTS b2b_company_email_idx;
DROP TABLE IF EXISTS b2b_company;
