-- atlas:txmode none

-- Create dedicated user for Sequin
CREATE USER sequin_user WITH PASSWORD 'ktNyE6d9kDvXpFzzWCYDKeLzBYTZf7z88/UkZvHzuF8=';

-- Grant connect and schema usage permissions
GRANT CONNECT ON DATABASE "process-engine" TO sequin_user;
GRANT USAGE ON SCHEMA public TO sequin_user;

-- Grant replication permission
ALTER USER sequin_user WITH REPLICATION;

-- Create publication for CDC (must be created before the slot)
CREATE PUBLICATION sequin_pub FOR ALL TABLES WITH (publish_via_partition_root = true);

-- Create replication slot (Sequin will use this for logical replication)
SELECT pg_create_logical_replication_slot('sequin_slot', 'pgoutput');
