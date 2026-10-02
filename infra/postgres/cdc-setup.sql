-- ---------------------------------------------------------------------------
-- Logical-replication setup for the analytics pipeline (CLAUDE.md §5.4).
--
-- Run by `make cdc-setup` as the superuser, AFTER migrations: it refers to
-- the three event outbox tables, which the profile, catalog and orders
-- migrations create. Idempotent — `make dev` runs it on every start.
--
-- Variables: cdc (EVENTS_CDC_DB_USER), publication (CDC_PUBLICATION),
-- slot (CDC_SLOT_NAME).
--
-- The publication carries INSERTs only. The outbox tables are insert-only by
-- design; the retention DELETE that prunes them must not reach the consumer
-- as anything, and publishing only inserts is what guarantees that.
-- ---------------------------------------------------------------------------

\set ON_ERROR_STOP on

-- Created empty, then given each outbox table it lacks: the same script brings
-- a new database and an existing one (whose publication predates a table) to
-- the same state.
SELECT format('CREATE PUBLICATION %I WITH (publish = %L)', :'publication', 'insert')
 WHERE NOT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = :'publication') \gexec

SELECT format('ALTER PUBLICATION %I ADD TABLE %I.%I', :'publication', t.schema, t.name)
  FROM (VALUES ('profile', 'supplier_events_outbox'),
               ('catalog', 'product_events_outbox'),
               ('orders',  'order_events_outbox')) AS t(schema, name)
 WHERE NOT EXISTS (
       SELECT 1 FROM pg_publication_tables p
        WHERE p.pubname = :'publication' AND p.schemaname = t.schema AND p.tablename = t.name) \gexec

-- The replication slot, created HERE rather than left to the consumer's first
-- start. A slot only sees changes made after it exists: if the consumer were
-- the one to create it, every event written between the migrations and its
-- first start — the analytics backfill on a new machine, the first orders
-- after a deploy — would never reach analytics. The consumer finds it already
-- there and resumes from it.
SELECT 'created slot ' || slot_name
  FROM pg_create_logical_replication_slot(:'slot', 'pgoutput')
 WHERE NOT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = :'slot');

-- The CDC role reads the WAL, never the tables: it is granted nothing on the
-- service schemas. Prove it, so a future grant is caught here rather than
-- discovered.
SELECT set_config('vayal.cdc', :'cdc', false) \gset
DO $$
DECLARE
    leak TEXT;
BEGIN
    SELECT string_agg(current_setting('vayal.cdc') || ' -> ' || s.schema, ', ') INTO leak
    FROM (VALUES ('profile'), ('catalog'), ('orders'), ('analytics')) AS s(schema)
    WHERE EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = s.schema)
      AND (has_schema_privilege(current_setting('vayal.cdc'), s.schema, 'USAGE')
        OR has_schema_privilege(current_setting('vayal.cdc'), s.schema, 'CREATE'));
    IF leak IS NOT NULL THEN
        RAISE EXCEPTION 'the CDC role reaches a schema: %', leak;
    END IF;
END
$$;

SELECT schemaname || '.' || tablename AS published FROM pg_publication_tables
 WHERE pubname = :'publication' ORDER BY 1;
