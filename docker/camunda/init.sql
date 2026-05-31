-- Create dedicated user for Sequin (as per documentation)
CREATE USER sequin_user WITH PASSWORD 'ktNyE6d9kDvXpFzzWCYDKeLzBYTZf7z88/UkZvHzuF8=';

-- Grant connect permission
GRANT CONNECT ON DATABASE "process-engine" TO sequin_user;

-- Grant select permission on all tables in schema public
GRANT SELECT ON ALL TABLES IN SCHEMA public TO sequin_user;

-- Grant replication permission
ALTER USER sequin_user WITH REPLICATION;

-- Create publication for CDC (must be created before the slot)
CREATE PUBLICATION sequin_pub FOR ALL TABLES WITH (publish_via_partition_root = true);

-- Create replication slot (Sequin will use this for logical replication)
SELECT pg_create_logical_replication_slot('sequin_slot', 'pgoutput');

-- Set replica identity to FULL for all tables to include changes in CDC payloads
-- Note: Commented out because these tables are created by Camunda on startup and do not exist during PostgreSQL initialization.
-- ALTER TABLE "public"."act_ge_bytearray" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_ge_property" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_ge_schema_log" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_hi_actinst" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_hi_attachment" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_hi_batch" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_hi_caseactinst" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_hi_caseinst" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_hi_comment" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_hi_dec_in" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_hi_dec_out" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_hi_decinst" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_hi_detail" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_hi_ext_task_log" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_hi_identitylink" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_hi_incident" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_hi_job_log" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_hi_op_log" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_hi_procinst" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_hi_taskinst" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_hi_varinst" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_id_group" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_id_info" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_id_membership" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_id_tenant" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_id_tenant_member" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_id_user" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_re_camformdef" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_re_case_def" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_re_decision_def" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_re_decision_req_def" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_re_deployment" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_re_procdef" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_ru_authorization" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_ru_batch" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_ru_case_execution" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_ru_case_sentry_part" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_ru_event_subscr" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_ru_execution" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_ru_ext_task" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_ru_filter" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_ru_identitylink" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_ru_incident" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_ru_job" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_ru_jobdef" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_ru_meter_log" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_ru_task" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_ru_task_meter_log" REPLICA IDENTITY FULL;
-- ALTER TABLE "public"."act_ru_variable" REPLICA IDENTITY FULL;