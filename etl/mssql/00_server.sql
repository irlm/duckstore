-- Server settings before the load, so the comparison is on purpose and not by accident.
--
-- The SQL Server 2025 container ignores MSSQL_MEMORY_LIMIT_MB, so the memory budget is set
-- here instead: {{MAX_MEMORY_MB}} MB, next to Postgres (shared_buffers 3 GB plus the OS page
-- cache) and DuckDB (memory_limit 6 GB). Without it SQL Server takes the whole machine, which
-- is realistic for a dedicated server and not for a laptop shared with two other engines.
EXEC sp_configure 'show advanced options', 1;
GO
RECONFIGURE;
GO
EXEC sp_configure 'max server memory (MB)', {{MAX_MEMORY_MB}};
GO
RECONFIGURE;
GO
