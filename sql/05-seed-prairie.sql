/*
  05-seed-prairie.sql — reference data for Prairie Valley Credit Union's Core (database Core_prairie).

  A second, fully separate tenant: its own SQL Server, its own customers, users, and accounts.
  All names are fictional. Money is integer cents.
*/
SET NOCOUNT ON;

INSERT dbo.Customers (CustomerId, Name, CompanyCode) VALUES
  ('harvest-coop', N'Harvest Growers Co-op', 'HARVEST');

INSERT dbo.Users (UserId, CustomerId, DisplayName, Role, ApprovalLimitCents) VALUES
  ('riley', 'harvest-coop', N'Riley Chen',                     'Initiator', NULL),
  ('casey', 'harvest-coop', N'Casey Ortiz',                    'Approver',  1000000000),  -- $10,000,000.00
  ('jamie', 'harvest-coop', N'Jamie Fields',                   'Approver',  1000000000),  -- $10,000,000.00
  ('devon', NULL,           N'Devon Brooks, Member Operations', 'BankOps',  NULL);        -- credit union staff

INSERT dbo.Accounts (AccountNumber, CustomerId, Nickname, Status, HoldReason, DailyWireLimitCents, AvailableBalanceCents) VALUES
  ('7702140011', 'harvest-coop', N'Harvest Operating', 'Active', NULL, 1000000000, 612044000),
  ('7702140029', 'harvest-coop', N'Harvest Payroll',   'Active', NULL, 1000000000, 488091525);

INSERT dbo.BlockedBeneficiaries (Name) VALUES
  (N'Northwind Shell Trading'),
  (N'Blue Harbor Exports Ltd'),
  (N'Granite Peak Holdings SA');
