# PostgreSQL Setup Guide

TokenHub supports PostgreSQL as a production database. This guide explains how to configure and deploy the PostgreSQL setup.

## Why PostgreSQL?

- **Production environments** - PostgreSQL is an enterprise-grade relational database suited for high-concurrency scenarios
- **Data integrity** - Stronger transaction support and concurrency control
- **Scalability** - Supports horizontal scaling and primary-replica replication
- **Backup and recovery** - A mature ecosystem of backup tools

SQLite remains the default choice, suitable for:
- Development and test environments
- Small deployments (<1000 users)
- Simple deployment needs

## Quick Start

### Using Docker Compose

1. **Copy the environment variable configuration**

```bash
cp deploy/.env.example deploy/.env
```

2. **Edit the .env file to set the PostgreSQL password**

```bash
POSTGRES_PASSWORD=your-secure-password
TOKENHUB_SECRET_KEY=your-secret-key
TOKENHUB_ADMIN_TOKEN=your-admin-token
```

3. **Start the services**

```bash
docker compose --env-file deploy/.env -f deploy/docker-compose.postgres.yml up -d
```

4. **Access the application**

- Frontend: http://localhost:3000
- Backend API: http://localhost:8080
- Health check: http://localhost:8080/healthz

Default administrator account:
- Username: `admin`
- Password: `admin123456`

**⚠️ Change the default password immediately!**

### Manual PostgreSQL Installation

1. **Install PostgreSQL**

macOS:
```bash
brew install postgresql@16
brew services start postgresql@16
```

Ubuntu/Debian:
```bash
sudo apt install postgresql-16
sudo systemctl start postgresql
```

2. **Create the database and user**

```bash
sudo -u postgres psql
```

```sql
CREATE USER tokenhub WITH PASSWORD 'your-password';
-- Make tokenhub the database owner so it can create tables in the public schema.
-- On PostgreSQL 15/16, GRANT ALL PRIVILEGES ON DATABASE alone does NOT grant
-- CREATE on the public schema, which causes GORM AutoMigrate to fail with
-- "permission denied for schema public".
CREATE DATABASE tokenhub OWNER tokenhub;
GRANT ALL PRIVILEGES ON DATABASE tokenhub TO tokenhub;
\q
```

If the database already exists and is owned by another role (for example `postgres`),
connect to it and grant schema privileges explicitly instead:

```sql
\c tokenhub
GRANT ALL ON SCHEMA public TO tokenhub;
ALTER SCHEMA public OWNER TO tokenhub;
\q
```

3. **Configure environment variables**

Set the following in `backend/.env`:

```bash
TOKENHUB_DATABASE_URL=postgresql://tokenhub:your-password@localhost:5432/tokenhub?sslmode=disable
TOKENHUB_DB_MAX_OPEN_CONNS=25
TOKENHUB_DB_MAX_IDLE_CONNS=5
TOKENHUB_DB_CONN_MAX_LIFETIME_MINUTES=30
```

4. **Start the backend**

```bash
cd backend
go run ./cmd/tokenhub
```

## Connection Pool Configuration

PostgreSQL supports connection pool configuration; tune it according to your load:

| Environment variable | Default | Description |
|---------|--------|------|
| `TOKENHUB_DB_MAX_OPEN_CONNS` | 25 | Maximum number of open connections |
| `TOKENHUB_DB_MAX_IDLE_CONNS` | 5 | Maximum number of idle connections |
| `TOKENHUB_DB_CONN_MAX_LIFETIME_MINUTES` | 30 | Maximum connection lifetime (minutes) |

**Recommended configurations:**

- **Small scale (<100 users)**: MaxOpenConns=10, MaxIdleConns=2
- **Medium scale (100-1000 users)**: MaxOpenConns=25, MaxIdleConns=5 (default)
- **Large scale (>1000 users)**: MaxOpenConns=50, MaxIdleConns=10

## Database Connection String Format

```
postgresql://[user[:password]@][host][:port][/dbname][?param1=value1&...]
```

Examples:

```bash
# Local development
postgresql://tokenhub:password@localhost:5432/tokenhub?sslmode=disable

# Production (SSL enabled)
postgresql://tokenhub:password@db.example.com:5432/tokenhub?sslmode=require

# Connection pool parameters
postgresql://user:pass@host:5432/db?pool_max_conns=25&pool_min_conns=5
```

## Backup and Recovery

TokenHub's built-in backup feature only supports SQLite. For PostgreSQL, use `pg_dump` and `pg_restore`.

### Backing Up the Database

```bash
pg_dump -h localhost -U tokenhub -d tokenhub -F c -f tokenhub_backup_$(date +%Y%m%d).dump
```

### Restoring the Database

```bash
pg_restore -h localhost -U tokenhub -d tokenhub -c tokenhub_backup_20260721.dump
```

### Automated Backups (Cron)

Create a backup script at `/usr/local/bin/backup-tokenhub.sh`:

```bash
#!/bin/bash
BACKUP_DIR="/var/backups/tokenhub"
DATE=$(date +%Y%m%d_%H%M%S)
mkdir -p $BACKUP_DIR

pg_dump -h localhost -U tokenhub -d tokenhub -F c -f $BACKUP_DIR/tokenhub_$DATE.dump

# Keep backups from the last 7 days
find $BACKUP_DIR -name "tokenhub_*.dump" -mtime +7 -delete
```

Add it to crontab (backup daily at 2 AM):

```bash
0 2 * * * /usr/local/bin/backup-tokenhub.sh
```

## Migrating from SQLite to PostgreSQL

The current version of TokenHub does not include an automatic migration tool. Migration steps:

1. **Export SQLite data as SQL**

```bash
sqlite3 data/tokenhub.db .dump > tokenhub_sqlite.sql
```

2. **Convert the SQL syntax**

Manually edit `tokenhub_sqlite.sql` to adjust SQLite-specific syntax to PostgreSQL-compatible syntax.

3. **Import into PostgreSQL**

```bash
psql -h localhost -U tokenhub -d tokenhub -f tokenhub_sqlite.sql
```

**Note**: A data migration tool is planned for a future release.

## Performance Tuning

### Index Optimization

TokenHub automatically creates the necessary indexes, but you can add extra indexes based on your query patterns:

```sql
-- If you frequently query projects by cost_center
CREATE INDEX idx_projects_cost_center ON projects(cost_center);

-- If you frequently query usage records within a specific time range
CREATE INDEX idx_usage_records_created_at ON usage_records(created_at);
```

### Query Performance Monitoring

Enable the PostgreSQL slow query log:

```sql
ALTER SYSTEM SET log_min_duration_statement = 1000;  -- Log queries taking longer than 1 second
SELECT pg_reload_conf();
```

View slow queries:

```bash
tail -f /var/log/postgresql/postgresql-16-main.log | grep "duration:"
```

## Troubleshooting

### Connection Failures

1. **Check whether PostgreSQL is running**

```bash
pg_isready -h localhost -U tokenhub
```

2. **Check the firewall**

```bash
sudo ufw allow 5432/tcp
```

3. **Check pg_hba.conf**

Ensure connections from the application are allowed:

```
# IPv4 local connections:
host    tokenhub    tokenhub    127.0.0.1/32    md5
```

### Connection Pool Exhaustion

If you see "too many connections" errors, reduce the connection pool size:

```bash
TOKENHUB_DB_MAX_OPEN_CONNS=10
```

### Performance Issues

1. **Run VACUUM**

```sql
VACUUM ANALYZE;
```

2. **Check table bloat**

```sql
SELECT schemaname, tablename, pg_size_pretty(pg_total_relation_size(schemaname||'.'||tablename)) AS size
FROM pg_tables
WHERE schemaname = 'public'
ORDER BY pg_total_relation_size(schemaname||'.'||tablename) DESC;
```

## References

- [PostgreSQL Official Documentation](https://www.postgresql.org/docs/)
- [GORM PostgreSQL Driver](https://github.com/go-gorm/postgres)
- [pgx Driver Documentation](https://github.com/jackc/pgx)
