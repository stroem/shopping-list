-- Reverse of 0001_init.up.sql. Drop in FK-safe order.
DROP TABLE IF EXISTS ean_mappings;
DROP TABLE IF EXISTS food_catalog;
DROP TABLE IF EXISTS check_off_events;
DROP TABLE IF EXISTS store_items;
DROP TABLE IF EXISTS store_aisles;
DROP TABLE IF EXISTS stores;
DROP TABLE IF EXISTS list_items;
DROP TABLE IF EXISTS items;
DROP TABLE IF EXISTS lists;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS households;
DROP EXTENSION IF EXISTS pg_trgm;
