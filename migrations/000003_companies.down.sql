DROP TRIGGER IF EXISTS memberships_preserve_owner ON companies.memberships;
DROP TRIGGER IF EXISTS companies_require_owner ON companies.companies;
DROP FUNCTION IF EXISTS companies.assert_active_company_has_owner();
DROP TABLE IF EXISTS companies.memberships;
DROP TABLE IF EXISTS companies.companies;
DROP SCHEMA IF EXISTS companies;
