-- Modify "investigations" table
ALTER TABLE "investigations" ADD COLUMN "cost_usd" double precision NOT NULL DEFAULT 0;
-- Modify "messages" table
ALTER TABLE "messages" ADD COLUMN "model" character varying NOT NULL DEFAULT '', ADD COLUMN "prompt_tokens" bigint NOT NULL DEFAULT 0, ADD COLUMN "completion_tokens" bigint NOT NULL DEFAULT 0, ADD COLUMN "cost_usd" double precision NOT NULL DEFAULT 0;
