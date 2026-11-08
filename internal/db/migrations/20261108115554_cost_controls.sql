-- Modify "incidents" table
ALTER TABLE "incidents" ADD COLUMN "storm_id" bigint NULL, ADD COLUMN "investigation_note" character varying NOT NULL DEFAULT '';
