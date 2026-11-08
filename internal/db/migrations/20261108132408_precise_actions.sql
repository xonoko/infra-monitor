-- Modify "actions" table
ALTER TABLE "actions" ADD COLUMN "prepared" jsonb NULL, ADD COLUMN "payload_hash" character varying NOT NULL DEFAULT '', ADD COLUMN "requires_approval" boolean NOT NULL DEFAULT false, ADD COLUMN "superseded_by" bigint NULL;
