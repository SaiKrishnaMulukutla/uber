-- Create per-service databases.
-- Docker entrypoint runs .sql files via psql, so \gexec works.
SELECT 'CREATE DATABASE users_db'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'users_db')\gexec

SELECT 'CREATE DATABASE drivers_db'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'drivers_db')\gexec

SELECT 'CREATE DATABASE trips_db'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'trips_db')\gexec

SELECT 'CREATE DATABASE notifications_db'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'notifications_db')\gexec

SELECT 'CREATE DATABASE payments_db'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'payments_db')\gexec
