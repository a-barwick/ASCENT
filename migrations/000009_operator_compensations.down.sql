DROP TRIGGER IF EXISTS operator_compensations_guard_update ON operator_admin.compensations;
DROP TRIGGER IF EXISTS operator_compensation_movements_match
  ON operator_admin.compensation_inventory_movements;
DROP TRIGGER IF EXISTS operator_compensation_journals_match
  ON operator_admin.compensation_journals;
DROP TRIGGER IF EXISTS operator_compensations_require_effects
  ON operator_admin.compensations;
DROP FUNCTION IF EXISTS operator_admin.guard_compensation_update();
DROP FUNCTION IF EXISTS operator_admin.assert_compensation_contract();
DROP TABLE IF EXISTS operator_admin.compensation_inventory_movements;
DROP TABLE IF EXISTS operator_admin.compensation_journals;
DROP TABLE IF EXISTS operator_admin.compensations;
DROP TABLE IF EXISTS operator_admin.grants;
DROP SCHEMA IF EXISTS operator_admin;

DROP TABLE IF EXISTS platform.command_relations;
