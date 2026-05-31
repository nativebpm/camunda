-- Set replica identity to FULL for act_ru_ext_task to ensure WAL contains old row state
ALTER TABLE "public"."act_ru_ext_task" REPLICA IDENTITY FULL;

-- Grant select permission on all existing tables in schema public to sequin_user
GRANT SELECT ON ALL TABLES IN SCHEMA public TO sequin_user;

-- Configure default privileges for future tables created by camunda
ALTER DEFAULT PRIVILEGES FOR ROLE camunda IN SCHEMA public GRANT SELECT ON TABLES TO sequin_user;
