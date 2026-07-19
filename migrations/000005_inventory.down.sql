DROP TRIGGER IF EXISTS movements_are_immutable ON inventory.movements;
DROP TRIGGER IF EXISTS movements_preserve_commodity ON inventory.movements;
DROP FUNCTION IF EXISTS inventory.reject_movement_change();
DROP FUNCTION IF EXISTS inventory.assert_movement_compatible();
DROP TABLE IF EXISTS inventory.movements;
DROP TABLE IF EXISTS inventory.holdings;
DROP TABLE IF EXISTS inventory.commodities;
DROP TABLE IF EXISTS inventory.locations;
DROP SCHEMA IF EXISTS inventory;
