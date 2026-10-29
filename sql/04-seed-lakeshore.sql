/*
  04-seed-lakeshore.sql — reference data for Lakeshore Community Bank's Core (database Core_lakeshore).

  Customers, users (with Core roles and approval limits), accounts, and the demo screening list.
  bulkwire-service's demo sign-in directory mirrors these users;
  Core remains the authority for roles, limits, and approval decisions. All names are fictional.
  Money is integer cents.
*/
SET NOCOUNT ON;

INSERT dbo.Customers (CustomerId, Name, CompanyCode) VALUES
  ('summit-logistics',  N'Summit Logistics, Inc.',  'SUMMIT'),
  ('northline-medical', N'Northline Medical Group', 'NORTHLINE');

INSERT dbo.Users (UserId, CustomerId, DisplayName, Role, ApprovalLimitCents) VALUES
  ('jordan', 'summit-logistics',  N'Jordan Ellis',                   'Initiator', NULL),
  ('alex',   'summit-logistics',  N'Alex Rivera, Controller',        'Approver',  3000000000),  -- $30,000,000.00
  ('sam',    'summit-logistics',  N'Sam Patel, Treasurer',           'Approver',  5000000000),  -- $50,000,000.00
  ('taylor', 'northline-medical', N'Taylor Brooks',                  'Initiator', NULL),
  ('quinn',  'northline-medical', N'Quinn Murphy, CFO',              'Approver',  4000000000),  -- $40,000,000.00
  ('avery',  'northline-medical', N'Avery Collins, Controller',      'Approver',  4000000000),  -- $40,000,000.00
  ('morgan', NULL,                N'Morgan Lee, Wire Operations',    'BankOps',   NULL);        -- bank staff

INSERT dbo.Accounts (AccountNumber, CustomerId, Nickname, Status, HoldReason, DailyWireLimitCents, AvailableBalanceCents) VALUES
  ('4401220017', 'summit-logistics',  N'Summit Freight Operating',    'Active', NULL, 2500000000, 4825011342),
  ('4401220033', 'summit-logistics',  N'Summit Cold Chain Operating', 'Active', NULL, 1500000000,  980422010),
  ('4401220058', 'summit-logistics',  N'Summit Logistics Payroll',    'Active', NULL, 2500000000, 3144700655),
  ('4409330014', 'northline-medical', N'Northline Operating',         'Active', NULL, 4000000000, 5211840000),
  ('4409330022', 'northline-medical', N'Northline Payroll',           'Active', NULL, 4000000000, 3890211735);

INSERT dbo.BlockedBeneficiaries (Name) VALUES
  (N'Northwind Shell Trading'),
  (N'Blue Harbor Exports Ltd'),
  (N'Granite Peak Holdings SA');
