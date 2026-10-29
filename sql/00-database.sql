/*
  00-database.sql — creates one FI's Core database on that FI's own SQL Server.

  Run by init.sh with `sqlcmd -v DbName=Core_<fiId>` only when the database does not exist yet.
  Each FI has its own SQL Server container and its own database (Core_lakeshore, Core_prairie): stopping,
  pausing, or slowing one FI's database cannot affect the other FI.
*/
CREATE DATABASE [$(DbName)];
GO
