/*
  02-types.sql — Table-Valued Parameter types.

  A TVP sends a whole set of rows to a stored procedure in ONE round trip, and the procedure handles them
  set-based (one INSERT ... SELECT) instead of row by row. core-api builds these with
  IEnumerable<SqlDataRecord> whose metadata must match the column order below EXACTLY
  (core/CoreApi/Data/Tvp.cs; a unit test reads this file to check the order).

  Re-runnable only before procedures reference the types: init.sh runs this once, on database creation.
*/
SET NOCOUNT ON;
GO

IF TYPE_ID(N'dbo.WireDraftTvp') IS NULL
CREATE TYPE dbo.WireDraftTvp AS TABLE (
  ExternalWireId     varchar(64)   NOT NULL PRIMARY KEY,
  RowNumber          int           NOT NULL,
  OriginatingAccount varchar(17)   NOT NULL,
  ProcessDate        date          NOT NULL,
  AmountCents        bigint        NOT NULL,
  Currency           char(3)       NOT NULL,
  BeneficiaryName    nvarchar(70)  NOT NULL,
  BeneficiaryAccount varchar(17)   NOT NULL,
  BeneficiaryRouting char(9)       NOT NULL,
  BeneficiaryBank    nvarchar(70)  NULL,
  Reference          nvarchar(140) NULL,
  Subsidiary         nvarchar(70)  NULL
);
GO

IF TYPE_ID(N'dbo.AccountNumberTvp') IS NULL
CREATE TYPE dbo.AccountNumberTvp AS TABLE (
  AccountNumber varchar(17) NOT NULL PRIMARY KEY
);
GO
