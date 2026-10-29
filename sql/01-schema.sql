/*
  01-schema.sql — Core (core banking system of record) tables for one FI.

  Core owns customers, users and their approval roles, accounts and holds, the batch header ("GenTxBatch"),
  every wire, the wire audit trail, and approvals (determination, dual control). The bulk wire workflow
  never stores wires itself: it writes them here through stored procedures (03-procs.sql) and keeps only
  counts. Identical schema on every FI's server; only the seed data differs.

  Re-runnable: every object is created only if missing.
*/
SET NOCOUNT ON;
GO

IF OBJECT_ID(N'dbo.Customers', N'U') IS NULL
CREATE TABLE dbo.Customers (
  CustomerId   varchar(64)   NOT NULL PRIMARY KEY,
  Name         nvarchar(120) NOT NULL,
  CompanyCode  varchar(20)   NOT NULL UNIQUE          -- Company ID shown at sign-in (display only)
);
GO

IF OBJECT_ID(N'dbo.Users', N'U') IS NULL
CREATE TABLE dbo.Users (
  UserId             varchar(64)   NOT NULL PRIMARY KEY,
  CustomerId         varchar(64)   NULL REFERENCES dbo.Customers(CustomerId), -- NULL for bank staff
  DisplayName        nvarchar(120) NOT NULL,
  Role               varchar(20)   NOT NULL CHECK (Role IN ('Initiator','Approver','BankOps')),
  ApprovalLimitCents bigint        NULL
);
GO

IF OBJECT_ID(N'dbo.Accounts', N'U') IS NULL
CREATE TABLE dbo.Accounts (
  AccountNumber         varchar(17)   NOT NULL PRIMARY KEY,
  CustomerId            varchar(64)   NOT NULL REFERENCES dbo.Customers(CustomerId),
  Nickname              nvarchar(80)  NOT NULL,
  Status                varchar(10)   NOT NULL CHECK (Status IN ('Active','OnHold','Closed')),
  HoldReason            nvarchar(200) NULL,
  DailyWireLimitCents   bigint        NOT NULL,
  AvailableBalanceCents bigint        NOT NULL   -- for the dashboard widget only
);
GO

IF OBJECT_ID(N'dbo.BlockedBeneficiaries', N'U') IS NULL
CREATE TABLE dbo.BlockedBeneficiaries (       -- DEMO-ONLY stand-in for sanctions screening
  Name nvarchar(70) COLLATE SQL_Latin1_General_CP1_CI_AS NOT NULL PRIMARY KEY  -- case-insensitive exact match
);
GO

IF OBJECT_ID(N'dbo.GenTxBatches', N'U') IS NULL
CREATE TABLE dbo.GenTxBatches (
  GenTxBatchId           bigint IDENTITY(100000,1) NOT NULL PRIMARY KEY,
  -- The bulkwire batchId (ULID). Unique, so EnsureGenTxBatch can be retried without creating a second batch.
  ExternalBatchId     varchar(40)  NOT NULL CONSTRAINT UQ_GenTxBatches_External UNIQUE,
  CustomerId          varchar(64)  NOT NULL REFERENCES dbo.Customers(CustomerId),
  InitiatedBy         varchar(64)  NOT NULL,
  ExpectedRowCount    int          NOT NULL,
  ExpectedAmountCents bigint       NOT NULL,
  Status              varchar(20)  NOT NULL CHECK (Status IN
     ('Drafting','Drafted','Authorizing','Authorized','Expired','Denied','Cancelled','Rejected')),
  StatusReason        nvarchar(200) NULL,
  CreatedAt           datetime2(3) NOT NULL DEFAULT SYSUTCDATETIME(),
  UpdatedAt           datetime2(3) NOT NULL DEFAULT SYSUTCDATETIME()
);
GO

IF OBJECT_ID(N'dbo.Wires', N'U') IS NULL
CREATE TABLE dbo.Wires (
  WireId             bigint IDENTITY(1,1) NOT NULL PRIMARY KEY,
  GenTxBatchId          bigint        NOT NULL REFERENCES dbo.GenTxBatches(GenTxBatchId),
  ExternalWireId     varchar(64)   NOT NULL,   -- "{batchId}-{rowNumber:05d}": the idempotency key
  ChunkId            varchar(16)   NOT NULL,
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
  Subsidiary         nvarchar(70)  NULL,
  Status             varchar(20)   NOT NULL CHECK (Status IN
     ('Drafted','Authorized','Rejected','Expired','Denied','Cancelled')),
  RejectCode         varchar(40)   NULL,
  RejectMessage      nvarchar(200) NULL,
  ApprovalRef        varchar(64)   NULL,
  CreatedAt          datetime2(3)  NOT NULL DEFAULT SYSUTCDATETIME(),
  UpdatedAt          datetime2(3)  NOT NULL DEFAULT SYSUTCDATETIME(),
  -- WHY: makes retries idempotent. A wire can exist at most once per batch, whatever happens to the
  -- response of the call that inserted it.
  CONSTRAINT UQ_Wires_Batch_External UNIQUE (GenTxBatchId, ExternalWireId)
);
GO

IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_Wires_Batch_Chunk_Status')
  CREATE INDEX IX_Wires_Batch_Chunk_Status ON dbo.Wires (GenTxBatchId, ChunkId, Status);
IF NOT EXISTS (SELECT 1 FROM sys.indexes WHERE name = N'IX_Wires_Account_Date_Status')
  CREATE INDEX IX_Wires_Account_Date_Status ON dbo.Wires (OriginatingAccount, ProcessDate, Status) INCLUDE (AmountCents);
GO

IF OBJECT_ID(N'dbo.WireAudit', N'U') IS NULL
CREATE TABLE dbo.WireAudit (               -- "audit tables" written during draft and authorize
  AuditId     bigint IDENTITY(1,1) PRIMARY KEY,
  WireId      bigint        NOT NULL,
  GenTxBatchId   bigint        NOT NULL,
  Action      varchar(20)   NOT NULL,      -- Drafted | Rejected | Authorized | Expired | Denied | Cancelled
  Actor       varchar(64)   NOT NULL,
  ApprovalRef varchar(64)   NULL,
  Detail      nvarchar(200) NULL,
  At          datetime2(3)  NOT NULL DEFAULT SYSUTCDATETIME()
);
GO

IF OBJECT_ID(N'dbo.Approvals', N'U') IS NULL
CREATE TABLE dbo.Approvals (
  ExternalBatchId   varchar(40)   NOT NULL PRIMARY KEY,
  CustomerId        varchar(64)   NOT NULL,
  AmountCents       bigint        NOT NULL,
  RequiredApprovers tinyint       NOT NULL,
  Status            varchar(20)   NOT NULL CHECK (Status IN ('PendingSecond','Approved','Denied')),
  ApprovalRef       varchar(64)   NULL,     -- set when Approved: "APR-{ExternalBatchId}"
  DeniedBy          varchar(64)   NULL,
  DenyReason        nvarchar(200) NULL,
  CreatedAt         datetime2(3)  NOT NULL DEFAULT SYSUTCDATETIME(),
  CompletedAt       datetime2(3)  NULL
);
GO

IF OBJECT_ID(N'dbo.ApprovalSignatures', N'U') IS NULL
CREATE TABLE dbo.ApprovalSignatures (
  ExternalBatchId varchar(40)  NOT NULL REFERENCES dbo.Approvals(ExternalBatchId),
  UserId          varchar(64)  NOT NULL,
  SignedAt        datetime2(3) NOT NULL DEFAULT SYSUTCDATETIME(),
  CONSTRAINT PK_ApprovalSignatures PRIMARY KEY (ExternalBatchId, UserId)   -- one signature per approver
);
GO
