/*
  03-procs.sql — every Core read and write path, as stored procedures.

  core-api is a thin layer: one endpoint calls one procedure, with a 30-second command timeout. All data
  logic, idempotency, and business rules (account status, daily limits, screening, determination, dual
  control) live here, in the system of record.

  Error contract: business-rule violations THROW 51001–51022 with the message '{CODE}: {human message}'.
  core-api splits on the first ':' and maps the number to an HTTP status (core/CoreApi/Data/SqlErrorMapper.cs). Messages
  never contain a full account number (only 'ending 0033').

  Transactions: every explicit transaction runs inside BEGIN TRY ... BEGIN CATCH ROLLBACK; THROW; END CATCH.
  XACT_ABORT dooms a transaction on any error; the CATCH rolls it back immediately and re-raises the
  ORIGINAL error (same number and message), so core-api's error mapping still sees 51001, 1205, -2, etc.

  Idempotency is the theme of this file. Every procedure an Activity calls can be executed any number of
  times with the same arguments and leaves the same end state, because Temporal retries an Activity
  whenever it cannot be sure the previous attempt finished (timeouts, lost responses, crashed workers).
*/
SET NOCOUNT ON;
GO

/* ---------------------------------------------------------------------------------------------------
   Helpers
--------------------------------------------------------------------------------------------------- */

-- Masks an account number for messages and logs: '4401220033' -> 'ending 0033'.
CREATE OR ALTER FUNCTION dbo.fn_MaskAccount (@AccountNumber varchar(17))
RETURNS varchar(20)
AS
BEGIN
  RETURN 'ending ' + RIGHT(@AccountNumber, 4);
END;
GO

-- Batch header plus wire counts by status. Shared by usp_SetGenTxBatchStatus and usp_GetGenTxBatchByExternalId
-- so the UI and the workflow see the same shape.
CREATE OR ALTER FUNCTION dbo.fn_GenTxBatchSummary (@GenTxBatchId bigint)
RETURNS TABLE
AS
RETURN
  SELECT b.GenTxBatchId, b.ExternalBatchId, b.CustomerId, b.InitiatedBy, b.ExpectedRowCount,
         b.ExpectedAmountCents, b.Status, b.StatusReason, b.CreatedAt, b.UpdatedAt,
         DraftedCount    = ISNULL(SUM(CASE WHEN w.Status = 'Drafted'    THEN 1 ELSE 0 END), 0),
         AuthorizedCount = ISNULL(SUM(CASE WHEN w.Status = 'Authorized' THEN 1 ELSE 0 END), 0),
         RejectedCount   = ISNULL(SUM(CASE WHEN w.Status = 'Rejected'   THEN 1 ELSE 0 END), 0),
         ExpiredCount    = ISNULL(SUM(CASE WHEN w.Status = 'Expired'    THEN 1 ELSE 0 END), 0),
         DeniedCount     = ISNULL(SUM(CASE WHEN w.Status = 'Denied'     THEN 1 ELSE 0 END), 0),
         CancelledCount  = ISNULL(SUM(CASE WHEN w.Status = 'Cancelled'  THEN 1 ELSE 0 END), 0),
         WireCount       = COUNT(w.WireId)
  FROM dbo.GenTxBatches b
  LEFT JOIN dbo.Wires w ON w.GenTxBatchId = b.GenTxBatchId
  WHERE b.GenTxBatchId = @GenTxBatchId
  GROUP BY b.GenTxBatchId, b.ExternalBatchId, b.CustomerId, b.InitiatedBy, b.ExpectedRowCount,
           b.ExpectedAmountCents, b.Status, b.StatusReason, b.CreatedAt, b.UpdatedAt;
GO

/* ---------------------------------------------------------------------------------------------------
   dbo.usp_ValidateAccounts — staging (bulkwire-service, synchronous upload path); TVP; read-only.

   One call per uploaded file validates every distinct originating account at once (TVP), instead of
   one round trip per account. Returns one row per input account. Status is only revealed for accounts
   the customer owns.
--------------------------------------------------------------------------------------------------- */
CREATE OR ALTER PROCEDURE dbo.usp_ValidateAccounts
  @CustomerId varchar(64),
  @Accounts   dbo.AccountNumberTvp READONLY
AS
BEGIN
  SET NOCOUNT ON; SET XACT_ABORT ON;

  SELECT t.AccountNumber,
         [Exists]        = CAST(CASE WHEN a.AccountNumber IS NULL THEN 0 ELSE 1 END AS bit),
         OwnedByCustomer = CAST(CASE WHEN a.CustomerId = @CustomerId THEN 1 ELSE 0 END AS bit),
         Status          = CASE WHEN a.CustomerId = @CustomerId THEN a.Status END
  FROM @Accounts t
  LEFT JOIN dbo.Accounts a ON a.AccountNumber = t.AccountNumber
  ORDER BY t.AccountNumber;
END;
GO

/* ---------------------------------------------------------------------------------------------------
   dbo.usp_EnsureGenTxBatch — Activity EnsureGenTxBatch; idempotent by ExternalBatchId.

   Creates Core's batch header for a bulkwire batch, or returns the existing one. A retried Activity (or
   an attempt whose response was lost) gets the SAME GenTxBatchId back, so the workflow never creates two
   Core batches for one file. Created = 1 only on the call that inserted the row.
--------------------------------------------------------------------------------------------------- */
CREATE OR ALTER PROCEDURE dbo.usp_EnsureGenTxBatch
  @ExternalBatchId varchar(40),
  @CustomerId      varchar(64),
  @InitiatedBy     varchar(64),
  @RowCount        int,
  @AmountCents     bigint
AS
BEGIN
  SET NOCOUNT ON; SET XACT_ABORT ON;
  DECLARE @Id bigint, @Created bit = 0;

  BEGIN TRY
    BEGIN TRAN;
      -- WHY UPDLOCK+HOLDLOCK: two concurrent first attempts (rare, but possible after a lost response)
      -- serialize on the ExternalBatchId key range instead of both inserting.
      SELECT @Id = GenTxBatchId FROM dbo.GenTxBatches WITH (UPDLOCK, HOLDLOCK)
       WHERE ExternalBatchId = @ExternalBatchId;

      IF @Id IS NULL
      BEGIN
        INSERT dbo.GenTxBatches (ExternalBatchId, CustomerId, InitiatedBy, ExpectedRowCount, ExpectedAmountCents, Status)
        VALUES (@ExternalBatchId, @CustomerId, @InitiatedBy, @RowCount, @AmountCents, 'Drafting');
        SET @Id = SCOPE_IDENTITY(); SET @Created = 1;
      END
    COMMIT;
  END TRY
  BEGIN CATCH
    IF @@TRANCOUNT > 0 ROLLBACK;
    THROW;
  END CATCH;

  SELECT GenTxBatchId = @Id, Created = @Created,
         Status = (SELECT Status FROM dbo.GenTxBatches WHERE GenTxBatchId = @Id);
END;
GO

/* ---------------------------------------------------------------------------------------------------
   dbo.usp_CheckGroupLimit — Activity CheckGroupPolicy; read-only.

   Evaluates the daily wire limit for one group (originating account + process date). A DENY is a
   business outcome returned as a row, not an error: the workflow writes that group's wires as Rejected
   (DAILY_LIMIT_EXCEEDED) so Core remains the complete record.

   It does NOT check account status: holds are enforced where money is written (usp_InsertDraftWires,
   usp_AuthorizeChunk), so a hold parks the affected chunks, not the policy step.
   Usage counts wires Drafted or Authorized on the same account and date in OTHER batches, so re-running
   this check for the same batch gives the same answer.

   PRODUCTION NOTE: a real core would reserve the limit atomically; this demo evaluates it.
--------------------------------------------------------------------------------------------------- */
CREATE OR ALTER PROCEDURE dbo.usp_CheckGroupLimit
  @GenTxBatchId          bigint,
  @OriginatingAccount varchar(17),
  @ProcessDate        date,
  @AmountCents        bigint
AS
BEGIN
  SET NOCOUNT ON; SET XACT_ABORT ON;
  DECLARE @LimitCents bigint, @UsedCents bigint, @msg nvarchar(2048);

  IF NOT EXISTS (SELECT 1 FROM dbo.GenTxBatches WHERE GenTxBatchId = @GenTxBatchId)
  BEGIN
    SET @msg = CONCAT(N'GENTX_BATCH_NOT_FOUND: Core batch ', @GenTxBatchId, N' does not exist');
    THROW 51011, @msg, 1;
  END

  SELECT @LimitCents = DailyWireLimitCents FROM dbo.Accounts WHERE AccountNumber = @OriginatingAccount;
  IF @LimitCents IS NULL
  BEGIN
    SET @msg = CONCAT(N'ACCOUNT_NOT_FOUND: account ', dbo.fn_MaskAccount(@OriginatingAccount), N' is not known to the bank');
    THROW 51003, @msg, 1;
  END

  SELECT @UsedCents = ISNULL(SUM(AmountCents), 0)
  FROM dbo.Wires
  WHERE OriginatingAccount = @OriginatingAccount
    AND ProcessDate = @ProcessDate
    AND Status IN ('Drafted', 'Authorized')
    AND GenTxBatchId <> @GenTxBatchId;

  SELECT Decision       = CASE WHEN @UsedCents + @AmountCents > @LimitCents THEN 'DENY' ELSE 'ALLOW' END,
         Code           = CASE WHEN @UsedCents + @AmountCents > @LimitCents THEN 'DAILY_LIMIT_EXCEEDED' END,
         LimitCents     = @LimitCents,
         UsedCents      = @UsedCents,
         RequestedCents = @AmountCents;
END;
GO

/* ---------------------------------------------------------------------------------------------------
   dbo.usp_InsertDraftWires — Activity InsertDraftChunk; TVP; idempotent. THE central procedure.
   PATTERN: Idempotent Activity — https://temporal.io/blog/idempotency-and-durable-execution

   Writes one chunk (up to 1,000 wires, normally 250) of a batch in one set-based statement.

   Called by: Activity InsertDraftChunk, once per chunk, with up to `concurrency` chunks of the same
   batch in flight at once, and possibly MORE THAN ONCE for the same chunk (Temporal retries an attempt
   that timed out, whose worker crashed, or whose HTTP response was lost after this procedure committed).

   Idempotency guarantee: calling this any number of times with the same chunk leaves exactly one row per
   ExternalWireId, and every call returns the same CHUNK-LEVEL totals. Three mechanisms provide it:
     1. UQ_Wires_Batch_External (GenTxBatchId, ExternalWireId): a duplicate can never be stored.
     2. INSERT ... WHERE NOT EXISTS with UPDLOCK, HOLDLOCK: rows already present are skipped, and a
        concurrent duplicate attempt waits on the key-range lock instead of racing to insert.
     3. One transaction with XACT_ABORT: a call either writes the whole chunk or nothing.

   Outcomes:
     - Account not Active (hold, closed, unknown)  -> THROW 51001/51002/51003, nothing written.
                                                      The Activity fails non-retryably; the workflow parks
                                                      the chunk until bank operations resolves it.
     - @ForceRejectCode set                         -> every new row stored as Rejected with that code
                                                      (group policy denial, or an operator's REJECT_CHUNK).
     - Beneficiary on the screening list            -> that row stored as Rejected BENEFICIARY_BLOCKED.
     - Otherwise                                    -> stored as Drafted.
   Rejections are business outcomes returned in the result, never errors.

   Result sets:
     1. One row, CHUNK-level totals (not call-level): ChunkDrafted, ChunkRejected, InsertedThisCall,
        AlreadyPresent. WHY chunk-level: if a previous attempt committed but its response was lost, the
        retry inserts nothing (AlreadyPresent = all) yet still reports the true totals, so the workflow's
        state is correct without any reconciliation step.
     2. This chunk's rejected wires: RowNumber, ExternalWireId, RejectCode, RejectMessage.
--------------------------------------------------------------------------------------------------- */
CREATE OR ALTER PROCEDURE dbo.usp_InsertDraftWires
  @GenTxBatchId          bigint,
  @ChunkId            varchar(16),
  @Actor              varchar(64),
  @ForceRejectCode    varchar(40)   = NULL,
  @ForceRejectMessage nvarchar(200) = NULL,
  @Wires              dbo.WireDraftTvp READONLY
AS
BEGIN
  SET NOCOUNT ON; SET XACT_ABORT ON;
  DECLARE @BatchStatus varchar(20), @msg nvarchar(2048);

  -- 1. The batch must exist and still be Drafting.
  -- WHY no lock on the header row: chunks of the same batch run concurrently, and locking the header
  -- would serialize them. The workflow guarantees no status transition happens while chunk inserts are
  -- in flight (it moves the batch to Drafted only after every chunk has settled).
  SELECT @BatchStatus = Status FROM dbo.GenTxBatches WHERE GenTxBatchId = @GenTxBatchId;
  IF @BatchStatus IS NULL
  BEGIN
    SET @msg = CONCAT(N'GENTX_BATCH_NOT_FOUND: Core batch ', @GenTxBatchId, N' does not exist');
    THROW 51011, @msg, 1;
  END
  IF @BatchStatus <> 'Drafting'
  BEGIN
    SET @msg = CONCAT(N'GENTX_BATCH_CLOSED: Core batch ', @GenTxBatchId, N' is ', @BatchStatus, N' and no longer accepts draft wires');
    THROW 51010, @msg, 1;
  END

  -- 2. Every originating account in the chunk must be Active, unless the chunk is being force-rejected
  -- (a rejected wire moves no money, so a hold must not block recording the rejection).
  -- A failure here fails the WHOLE chunk with nothing written: that is the structural failure the
  -- workflow parks for a human (e.g., bank operations releases the hold, then retries the chunk).
  IF @ForceRejectCode IS NULL
  BEGIN
    DECLARE @BadAccount varchar(17), @BadStatus varchar(10), @HoldReason nvarchar(200);

    SELECT TOP (1) @BadAccount = w.OriginatingAccount,
                   @BadStatus  = ISNULL(a.Status, 'Unknown'),
                   @HoldReason = a.HoldReason
    FROM (SELECT DISTINCT OriginatingAccount FROM @Wires) w
    LEFT JOIN dbo.Accounts a ON a.AccountNumber = w.OriginatingAccount
    WHERE a.AccountNumber IS NULL OR a.Status <> 'Active'
    ORDER BY w.OriginatingAccount;

    IF @BadStatus = 'OnHold'
    BEGIN
      SET @msg = CONCAT(N'ACCOUNT_ON_HOLD: account ', dbo.fn_MaskAccount(@BadAccount), N' is on hold',
                        CASE WHEN @HoldReason IS NOT NULL THEN CONCAT(N' (', @HoldReason, N')') END);
      THROW 51001, @msg, 1;
    END
    IF @BadStatus = 'Closed'
    BEGIN
      SET @msg = CONCAT(N'ACCOUNT_CLOSED: account ', dbo.fn_MaskAccount(@BadAccount), N' is closed');
      THROW 51002, @msg, 1;
    END
    IF @BadStatus = 'Unknown'
    BEGIN
      SET @msg = CONCAT(N'ACCOUNT_NOT_FOUND: account ', dbo.fn_MaskAccount(@BadAccount), N' is not known to the bank');
      THROW 51003, @msg, 1;
    END
  END

  -- 3. Insert every wire of this chunk that is not already present, deciding its status set-based.
  DECLARE @Inserted TABLE (WireId bigint NOT NULL, Status varchar(20) NOT NULL, RejectCode varchar(40) NULL);

  BEGIN TRY
    BEGIN TRAN;
      INSERT dbo.Wires (GenTxBatchId, ExternalWireId, ChunkId, RowNumber, OriginatingAccount, ProcessDate,
                        AmountCents, Currency, BeneficiaryName, BeneficiaryAccount, BeneficiaryRouting,
                        BeneficiaryBank, Reference, Subsidiary, Status, RejectCode, RejectMessage)
      OUTPUT inserted.WireId, inserted.Status, inserted.RejectCode INTO @Inserted
      SELECT @GenTxBatchId, w.ExternalWireId, @ChunkId, w.RowNumber, w.OriginatingAccount, w.ProcessDate,
             w.AmountCents, w.Currency, w.BeneficiaryName, w.BeneficiaryAccount, w.BeneficiaryRouting,
             w.BeneficiaryBank, w.Reference, w.Subsidiary,
             Status        = CASE WHEN @ForceRejectCode IS NOT NULL THEN 'Rejected'
                                  WHEN bb.Name IS NOT NULL         THEN 'Rejected'
                                  ELSE 'Drafted' END,
             RejectCode    = CASE WHEN @ForceRejectCode IS NOT NULL THEN @ForceRejectCode
                                  WHEN bb.Name IS NOT NULL         THEN 'BENEFICIARY_BLOCKED' END,
             RejectMessage = CASE WHEN @ForceRejectCode IS NOT NULL THEN @ForceRejectMessage
                                  WHEN bb.Name IS NOT NULL         THEN N'Beneficiary matches the bank''s screening list' END
      FROM @Wires w
      LEFT JOIN dbo.BlockedBeneficiaries bb
             ON bb.Name = w.BeneficiaryName COLLATE SQL_Latin1_General_CP1_CI_AS
      -- WHY UPDLOCK, HOLDLOCK here: key-range locks only on THIS chunk's keys. Concurrent chunks of the
      -- same batch do not block each other, and a duplicate concurrent attempt of the same chunk waits and
      -- then sees the rows as present, so it cannot double-insert.
      WHERE NOT EXISTS (SELECT 1 FROM dbo.Wires x WITH (UPDLOCK, HOLDLOCK)
                        WHERE x.GenTxBatchId = @GenTxBatchId AND x.ExternalWireId = w.ExternalWireId);

      -- 4. Audit rows for exactly the wires this call inserted.
      INSERT dbo.WireAudit (WireId, GenTxBatchId, Action, Actor, Detail)
      SELECT i.WireId, @GenTxBatchId, i.Status, @Actor,
             CONCAT(N'chunk ', @ChunkId, CASE WHEN i.RejectCode IS NOT NULL THEN CONCAT(N' · ', i.RejectCode) END)
      FROM @Inserted i;
    COMMIT;
  END TRY
  BEGIN CATCH
    IF @@TRANCOUNT > 0 ROLLBACK;
    THROW;
  END CATCH;

  -- 5a. Chunk-level totals (see header: why chunk-level, not call-level).
  DECLARE @InsertedThisCall int = (SELECT COUNT(*) FROM @Inserted);
  SELECT ChunkDrafted     = ISNULL(SUM(CASE WHEN Status = 'Drafted'  THEN 1 ELSE 0 END), 0),
         ChunkRejected    = ISNULL(SUM(CASE WHEN Status = 'Rejected' THEN 1 ELSE 0 END), 0),
         InsertedThisCall = @InsertedThisCall,
         AlreadyPresent   = (SELECT COUNT(*) FROM @Wires) - @InsertedThisCall
  FROM dbo.Wires
  WHERE GenTxBatchId = @GenTxBatchId AND ChunkId = @ChunkId;

  -- 5b. This chunk's rejected wires (business outcomes), in file order.
  SELECT RowNumber, ExternalWireId, RejectCode, RejectMessage
  FROM dbo.Wires
  WHERE GenTxBatchId = @GenTxBatchId AND ChunkId = @ChunkId AND Status = 'Rejected'
  ORDER BY RowNumber;
END;
GO

/* ---------------------------------------------------------------------------------------------------
   dbo.usp_AuthorizeChunk — Activity AuthorizeChunk; set-based flip; idempotent.

   After Core's approval process authorizes the batch, flips one chunk's Drafted wires to Authorized and
   writes audit rows. Only Drafted wires are touched, so a retry flips nothing new and reports the same
   chunk-level totals. A hold placed between draft and authorization fails the chunk (51001/51002) with
   nothing written; the workflow parks it in the authorize phase.
--------------------------------------------------------------------------------------------------- */
CREATE OR ALTER PROCEDURE dbo.usp_AuthorizeChunk
  @GenTxBatchId   bigint,
  @ChunkId     varchar(16),
  @ApprovalRef varchar(64),
  @Actor       varchar(64)
AS
BEGIN
  SET NOCOUNT ON; SET XACT_ABORT ON;
  DECLARE @BatchStatus varchar(20), @msg nvarchar(2048);

  SELECT @BatchStatus = Status FROM dbo.GenTxBatches WHERE GenTxBatchId = @GenTxBatchId;
  IF @BatchStatus IS NULL
  BEGIN
    SET @msg = CONCAT(N'GENTX_BATCH_NOT_FOUND: Core batch ', @GenTxBatchId, N' does not exist');
    THROW 51011, @msg, 1;
  END
  IF @BatchStatus <> 'Authorizing'
  BEGIN
    SET @msg = CONCAT(N'GENTX_BATCH_CLOSED: Core batch ', @GenTxBatchId, N' is ', @BatchStatus, N', not Authorizing');
    THROW 51010, @msg, 1;
  END

  DECLARE @BadAccount varchar(17), @BadStatus varchar(10), @HoldReason nvarchar(200);
  SELECT TOP (1) @BadAccount = a.AccountNumber, @BadStatus = a.Status, @HoldReason = a.HoldReason
  FROM dbo.Wires w
  JOIN dbo.Accounts a ON a.AccountNumber = w.OriginatingAccount
  WHERE w.GenTxBatchId = @GenTxBatchId AND w.ChunkId = @ChunkId AND w.Status = 'Drafted' AND a.Status <> 'Active'
  ORDER BY a.AccountNumber;

  IF @BadStatus = 'OnHold'
  BEGIN
    SET @msg = CONCAT(N'ACCOUNT_ON_HOLD: account ', dbo.fn_MaskAccount(@BadAccount), N' is on hold',
                      CASE WHEN @HoldReason IS NOT NULL THEN CONCAT(N' (', @HoldReason, N')') END);
    THROW 51001, @msg, 1;
  END
  IF @BadStatus = 'Closed'
  BEGIN
    SET @msg = CONCAT(N'ACCOUNT_CLOSED: account ', dbo.fn_MaskAccount(@BadAccount), N' is closed');
    THROW 51002, @msg, 1;
  END

  DECLARE @Flipped TABLE (WireId bigint NOT NULL);
  BEGIN TRY
    BEGIN TRAN;
      UPDATE dbo.Wires
         SET Status = 'Authorized', ApprovalRef = @ApprovalRef, UpdatedAt = SYSUTCDATETIME()
      OUTPUT inserted.WireId INTO @Flipped
       WHERE GenTxBatchId = @GenTxBatchId AND ChunkId = @ChunkId AND Status = 'Drafted';

      INSERT dbo.WireAudit (WireId, GenTxBatchId, Action, Actor, ApprovalRef, Detail)
      SELECT f.WireId, @GenTxBatchId, 'Authorized', @Actor, @ApprovalRef, CONCAT(N'chunk ', @ChunkId)
      FROM @Flipped f;
    COMMIT;
  END TRY
  BEGIN CATCH
    IF @@TRANCOUNT > 0 ROLLBACK;
    THROW;
  END CATCH;

  SELECT ChunkAuthorized = ISNULL(SUM(CASE WHEN Status = 'Authorized' THEN 1 ELSE 0 END), 0),
         ChunkRejected   = ISNULL(SUM(CASE WHEN Status = 'Rejected'   THEN 1 ELSE 0 END), 0),
         FlippedThisCall = (SELECT COUNT(*) FROM @Flipped)
  FROM dbo.Wires
  WHERE GenTxBatchId = @GenTxBatchId AND ChunkId = @ChunkId;
END;
GO

/* ---------------------------------------------------------------------------------------------------
   dbo.usp_RejectChunk — Activity RejectChunk; idempotent.

   An operator's REJECT_CHUNK decision for a chunk parked in the authorize phase: flips that chunk's
   Drafted wires to Rejected with the operator's code. Only Drafted wires are touched, so a retry is a
   no-op that reports the same chunk totals. (In the draft phase the same decision is carried out by
   usp_InsertDraftWires with @ForceRejectCode, because the wires do not exist yet.)
--------------------------------------------------------------------------------------------------- */
CREATE OR ALTER PROCEDURE dbo.usp_RejectChunk
  @GenTxBatchId bigint,
  @ChunkId   varchar(16),
  @Code      varchar(40),
  @Message   nvarchar(200),
  @Actor     varchar(64)
AS
BEGIN
  SET NOCOUNT ON; SET XACT_ABORT ON;
  DECLARE @BatchStatus varchar(20), @msg nvarchar(2048);

  SELECT @BatchStatus = Status FROM dbo.GenTxBatches WHERE GenTxBatchId = @GenTxBatchId;
  IF @BatchStatus IS NULL
  BEGIN
    SET @msg = CONCAT(N'GENTX_BATCH_NOT_FOUND: Core batch ', @GenTxBatchId, N' does not exist');
    THROW 51011, @msg, 1;
  END
  IF @BatchStatus NOT IN ('Drafted', 'Authorizing')
  BEGIN
    SET @msg = CONCAT(N'GENTX_BATCH_CLOSED: Core batch ', @GenTxBatchId, N' is ', @BatchStatus, N'; chunks can no longer be rejected');
    THROW 51010, @msg, 1;
  END

  DECLARE @Flipped TABLE (WireId bigint NOT NULL);
  BEGIN TRY
    BEGIN TRAN;
      UPDATE dbo.Wires
         SET Status = 'Rejected', RejectCode = @Code, RejectMessage = @Message, UpdatedAt = SYSUTCDATETIME()
      OUTPUT inserted.WireId INTO @Flipped
       WHERE GenTxBatchId = @GenTxBatchId AND ChunkId = @ChunkId AND Status = 'Drafted';

      INSERT dbo.WireAudit (WireId, GenTxBatchId, Action, Actor, Detail)
      SELECT f.WireId, @GenTxBatchId, 'Rejected', @Actor, CONCAT(N'chunk ', @ChunkId, N' · ', @Code)
      FROM @Flipped f;
    COMMIT;
  END TRY
  BEGIN CATCH
    IF @@TRANCOUNT > 0 ROLLBACK;
    THROW;
  END CATCH;

  SELECT ChunkAuthorized = ISNULL(SUM(CASE WHEN Status = 'Authorized' THEN 1 ELSE 0 END), 0),
         ChunkRejected   = ISNULL(SUM(CASE WHEN Status = 'Rejected'   THEN 1 ELSE 0 END), 0),
         FlippedThisCall = (SELECT COUNT(*) FROM @Flipped)
  FROM dbo.Wires
  WHERE GenTxBatchId = @GenTxBatchId AND ChunkId = @ChunkId;
END;
GO

/* ---------------------------------------------------------------------------------------------------
   dbo.usp_SetGenTxBatchStatus — Activity SetGenTxBatchStatus; idempotent.

   Moves Core's batch header through its lifecycle so Core always reflects the workflow's phase:

     From          To
     Drafting      Drafted, Cancelled, Rejected, Denied
     Drafted       Authorizing, Expired, Denied, Cancelled
     Authorizing   Authorized

   Setting the status the batch already has is a successful no-op (a retried Activity must succeed).
   Terminal outcomes Expired, Denied, Cancelled also flip the batch's remaining Drafted wires to that
   status, with audit rows, so no wire is left Drafted on a closed batch. Drafting -> Denied covers a
   denial Core records while chunks are still being drafted (the workflow stops drafting and closes the batch
   as Denied). Any other transition THROWs
   51012. Returns the batch header and wire counts by status.
--------------------------------------------------------------------------------------------------- */
CREATE OR ALTER PROCEDURE dbo.usp_SetGenTxBatchStatus
  @GenTxBatchId bigint,
  @Status    varchar(20),
  @Reason    nvarchar(200) = NULL,
  @Actor     varchar(64)
AS
BEGIN
  SET NOCOUNT ON; SET XACT_ABORT ON;
  DECLARE @From varchar(20), @msg nvarchar(2048);

  BEGIN TRY
    BEGIN TRAN;
      -- Lock the header: transitions are rare and must not interleave.
      SELECT @From = Status FROM dbo.GenTxBatches WITH (UPDLOCK, HOLDLOCK) WHERE GenTxBatchId = @GenTxBatchId;

      IF @From IS NULL
      BEGIN
        SET @msg = CONCAT(N'GENTX_BATCH_NOT_FOUND: Core batch ', @GenTxBatchId, N' does not exist');
        THROW 51011, @msg, 1;
      END

      IF @From <> @Status
      BEGIN
        IF NOT (   (@From = 'Drafting'    AND @Status IN ('Drafted', 'Cancelled', 'Rejected', 'Denied'))
                OR (@From = 'Drafted'     AND @Status IN ('Authorizing', 'Expired', 'Denied', 'Cancelled'))
                OR (@From = 'Authorizing' AND @Status = 'Authorized'))
        BEGIN
          SET @msg = CONCAT(N'INVALID_TRANSITION: Core batch ', @GenTxBatchId, N' cannot move from ', @From, N' to ', @Status);
          THROW 51012, @msg, 1;
        END

        UPDATE dbo.GenTxBatches
           SET Status = @Status, StatusReason = @Reason, UpdatedAt = SYSUTCDATETIME()
         WHERE GenTxBatchId = @GenTxBatchId;

        IF @Status IN ('Expired', 'Denied', 'Cancelled')
        BEGIN
          DECLARE @Closed TABLE (WireId bigint NOT NULL);
          UPDATE dbo.Wires
             SET Status = @Status, UpdatedAt = SYSUTCDATETIME()
          OUTPUT inserted.WireId INTO @Closed
           WHERE GenTxBatchId = @GenTxBatchId AND Status = 'Drafted';

          INSERT dbo.WireAudit (WireId, GenTxBatchId, Action, Actor, Detail)
          SELECT c.WireId, @GenTxBatchId, @Status, @Actor, @Reason FROM @Closed c;
        END
      END
    COMMIT;
  END TRY
  BEGIN CATCH
    IF @@TRANCOUNT > 0 ROLLBACK;
    THROW;
  END CATCH;

  SELECT * FROM dbo.fn_GenTxBatchSummary(@GenTxBatchId);
END;
GO

/* ---------------------------------------------------------------------------------------------------
   Approvals — called by core-api's approval endpoints (the portal), not by Activities.

   Core owns the decision: determination (role, approval limit, segregation of duties), MFA/TAC (checked by
   core-api before calling), and dual control. Only when a batch is fully approved does Core tell the
   orchestration layer, which delivers an `authorize` Update to the batch's workflow.
--------------------------------------------------------------------------------------------------- */

/* dbo.usp_GetApproval — approval row + signatures (the UI's "1 of 2 approvals"). Two result sets;
   the first is empty when nobody has acted on the batch yet. */
CREATE OR ALTER PROCEDURE dbo.usp_GetApproval
  @ExternalBatchId varchar(40)
AS
BEGIN
  SET NOCOUNT ON;

  SELECT ExternalBatchId, CustomerId, AmountCents, RequiredApprovers, Status, ApprovalRef,
         DeniedBy, DenyReason, CreatedAt, CompletedAt
  FROM dbo.Approvals WHERE ExternalBatchId = @ExternalBatchId;

  SELECT s.UserId, DisplayName = ISNULL(u.DisplayName, s.UserId), s.SignedAt
  FROM dbo.ApprovalSignatures s
  LEFT JOIN dbo.Users u ON u.UserId = s.UserId
  WHERE s.ExternalBatchId = @ExternalBatchId
  ORDER BY s.SignedAt, s.UserId;
END;
GO

/* dbo.usp_RecordApprovalSignature — records one approver's signature; idempotent once Approved.

   The amount, row count, and initiator come from Core's own batch record when the batch has been drafted;
   before that (approve-before-draft) they come from the caller (the staging summary).
   PRODUCTION NOTE: a real core computes these itself. */
CREATE OR ALTER PROCEDURE dbo.usp_RecordApprovalSignature
  @ExternalBatchId           varchar(40),
  @CustomerId                varchar(64),
  @UserId                    varchar(64),
  @InitiatedBy               varchar(64),
  @AmountCents               bigint,
  @DualControlThresholdCents bigint
AS
BEGIN
  SET NOCOUNT ON; SET XACT_ABORT ON;
  DECLARE @msg nvarchar(2048), @Role varchar(20), @UserCustomer varchar(64), @Limit bigint;
  DECLARE @GenTxStatus varchar(20), @GenTxCustomer varchar(64), @GenTxInitiatedBy varchar(64), @GenTxAmount bigint;

  BEGIN TRY
    BEGIN TRAN;
      SELECT @GenTxStatus = Status, @GenTxCustomer = CustomerId, @GenTxInitiatedBy = InitiatedBy, @GenTxAmount = ExpectedAmountCents
      FROM dbo.GenTxBatches WHERE ExternalBatchId = @ExternalBatchId;
      IF @GenTxStatus IS NOT NULL
        SELECT @InitiatedBy = @GenTxInitiatedBy, @AmountCents = @GenTxAmount;

      -- A late approval must never reopen a closed batch.
      IF @GenTxStatus IN ('Expired', 'Denied', 'Cancelled', 'Rejected')
      BEGIN
        SET @msg = CONCAT(N'APPROVAL_CLOSED: batch is closed (', @GenTxStatus, N'); it can no longer be approved');
        THROW 51022, @msg, 1;
      END
      IF @GenTxCustomer IS NOT NULL AND @GenTxCustomer <> @CustomerId
      BEGIN
        SET @msg = N'APPROVAL_NOT_ALLOWED: batch belongs to another customer';
        THROW 51020, @msg, 1;
      END

      -- Lock this batch's approval row (or its key range, if it does not exist yet) so two approvers
      -- signing at the same moment are serialized.
      DECLARE @ApprovalStatus varchar(20), @Required tinyint, @ApprovalCustomer varchar(64), @ApprovalAmount bigint;
      SELECT @ApprovalStatus = Status, @Required = RequiredApprovers, @ApprovalCustomer = CustomerId, @ApprovalAmount = AmountCents
      FROM dbo.Approvals WITH (UPDLOCK, HOLDLOCK) WHERE ExternalBatchId = @ExternalBatchId;
      IF @ApprovalStatus IS NOT NULL AND @GenTxStatus IS NULL
        SET @AmountCents = @ApprovalAmount;   -- every approver is checked against the same amount

      -- Determination.
      SELECT @Role = Role, @UserCustomer = CustomerId, @Limit = ApprovalLimitCents FROM dbo.Users WHERE UserId = @UserId;
      IF @Role IS NULL OR @UserCustomer IS NULL OR @UserCustomer <> @CustomerId
         OR (@ApprovalCustomer IS NOT NULL AND @ApprovalCustomer <> @CustomerId)
      BEGIN
        SET @msg = N'APPROVAL_NOT_ALLOWED: you are not an approver for this company';
        THROW 51020, @msg, 1;
      END
      IF @Role <> 'Approver'
      BEGIN
        SET @msg = N'APPROVAL_NOT_ALLOWED: your role does not permit approving wires';
        THROW 51020, @msg, 1;
      END
      IF @UserId = @InitiatedBy
      BEGIN
        SET @msg = N'APPROVAL_NOT_ALLOWED: the person who initiated a batch cannot approve it';
        THROW 51020, @msg, 1;
      END
      IF ISNULL(@Limit, 0) < @AmountCents
      BEGIN
        SET @msg = CONCAT(N'APPROVAL_NOT_ALLOWED: batch total $', FORMAT(@AmountCents / 100.0, 'N2'),
                          N' exceeds your approval limit of $', FORMAT(ISNULL(@Limit, 0) / 100.0, 'N2'));
        THROW 51020, @msg, 1;
      END

      IF @ApprovalStatus = 'Denied'
      BEGIN
        SET @msg = N'APPROVAL_CLOSED: this batch was denied and can no longer be approved';
        THROW 51022, @msg, 1;
      END

      IF @ApprovalStatus IS NULL
      BEGIN
        SET @Required = CASE WHEN @AmountCents >= @DualControlThresholdCents THEN 2 ELSE 1 END;
        INSERT dbo.Approvals (ExternalBatchId, CustomerId, AmountCents, RequiredApprovers, Status)
        VALUES (@ExternalBatchId, @CustomerId, @AmountCents, @Required, 'PendingSecond');
        SET @ApprovalStatus = 'PendingSecond';
      END

      -- Already approved: return it unchanged (core-api re-sends the callback; the Update ID deduplicates).
      IF @ApprovalStatus <> 'Approved'
      BEGIN
        IF EXISTS (SELECT 1 FROM dbo.ApprovalSignatures WHERE ExternalBatchId = @ExternalBatchId AND UserId = @UserId)
        BEGIN
          SET @msg = N'ALREADY_SIGNED: you have already approved this batch; a different approver must sign';
          THROW 51021, @msg, 1;
        END

        INSERT dbo.ApprovalSignatures (ExternalBatchId, UserId) VALUES (@ExternalBatchId, @UserId);

        IF (SELECT COUNT(*) FROM dbo.ApprovalSignatures WHERE ExternalBatchId = @ExternalBatchId) >= @Required
          UPDATE dbo.Approvals
             SET Status = 'Approved', ApprovalRef = 'APR-' + @ExternalBatchId, CompletedAt = SYSUTCDATETIME()
           WHERE ExternalBatchId = @ExternalBatchId;
      END
    COMMIT;
  END TRY
  BEGIN CATCH
    IF @@TRANCOUNT > 0 ROLLBACK;
    THROW;
  END CATCH;

  EXEC dbo.usp_GetApproval @ExternalBatchId = @ExternalBatchId;
END;
GO

/* dbo.usp_RecordDenial — an approver denies the batch. Idempotent once Denied; refused once Approved. */
CREATE OR ALTER PROCEDURE dbo.usp_RecordDenial
  @ExternalBatchId varchar(40),
  @CustomerId      varchar(64),
  @UserId          varchar(64),
  @Reason          nvarchar(200)
AS
BEGIN
  SET NOCOUNT ON; SET XACT_ABORT ON;
  DECLARE @msg nvarchar(2048), @GenTxStatus varchar(20), @GenTxAmount bigint, @ApprovalStatus varchar(20);

  IF NOT EXISTS (SELECT 1 FROM dbo.Users WHERE UserId = @UserId AND CustomerId = @CustomerId AND Role = 'Approver')
  BEGIN
    SET @msg = N'APPROVAL_NOT_ALLOWED: only an approver for this company can deny a batch';
    THROW 51020, @msg, 1;
  END

  BEGIN TRY
    BEGIN TRAN;
      SELECT @GenTxStatus = Status, @GenTxAmount = ExpectedAmountCents FROM dbo.GenTxBatches WHERE ExternalBatchId = @ExternalBatchId;
      IF @GenTxStatus IN ('Expired', 'Cancelled', 'Rejected', 'Authorizing', 'Authorized')
      BEGIN
        SET @msg = CONCAT(N'APPROVAL_CLOSED: batch is ', @GenTxStatus, N' and can no longer be denied');
        THROW 51022, @msg, 1;
      END

      SELECT @ApprovalStatus = Status FROM dbo.Approvals WITH (UPDLOCK, HOLDLOCK) WHERE ExternalBatchId = @ExternalBatchId;
      IF @ApprovalStatus = 'Approved'
      BEGIN
        SET @msg = N'APPROVAL_CLOSED: batch is already approved and can no longer be denied';
        THROW 51022, @msg, 1;
      END

      IF @ApprovalStatus IS NULL
        -- Denied before any signature: the amount is Core's own when the batch was drafted, else unknown (0).
        INSERT dbo.Approvals (ExternalBatchId, CustomerId, AmountCents, RequiredApprovers, Status, DeniedBy, DenyReason, CompletedAt)
        VALUES (@ExternalBatchId, @CustomerId, ISNULL(@GenTxAmount, 0), 1, 'Denied', @UserId, @Reason, SYSUTCDATETIME());
      ELSE IF @ApprovalStatus <> 'Denied'
        UPDATE dbo.Approvals
           SET Status = 'Denied', DeniedBy = @UserId, DenyReason = @Reason, CompletedAt = SYSUTCDATETIME()
         WHERE ExternalBatchId = @ExternalBatchId;
    COMMIT;
  END TRY
  BEGIN CATCH
    IF @@TRANCOUNT > 0 ROLLBACK;
    THROW;
  END CATCH;

  EXEC dbo.usp_GetApproval @ExternalBatchId = @ExternalBatchId;
END;
GO


/* ---------------------------------------------------------------------------------------------------
   Read procedures — the portal's views of the system of record.
--------------------------------------------------------------------------------------------------- */

-- Header + counts by status for a bulkwire batch ID (empty before EnsureGenTxBatch ran).
CREATE OR ALTER PROCEDURE dbo.usp_GetGenTxBatchByExternalId
  @ExternalBatchId varchar(40)
AS
BEGIN
  SET NOCOUNT ON;
  DECLARE @Id bigint = (SELECT GenTxBatchId FROM dbo.GenTxBatches WHERE ExternalBatchId = @ExternalBatchId);
  SELECT * FROM dbo.fn_GenTxBatchSummary(@Id);
END;
GO

-- One page of a batch's wires in file order, optionally filtered by status; second set = total matching.
-- @CustomerId (NULL for bank staff) restricts the read to the caller's own batches: another customer's
-- batch is reported as not found.
CREATE OR ALTER PROCEDURE dbo.usp_GetWiresPage
  @GenTxBatchId  bigint,
  @CustomerId varchar(64) = NULL,
  @Status     varchar(20) = NULL,
  @Offset     int,
  @Limit      int
AS
BEGIN
  SET NOCOUNT ON;
  DECLARE @msg nvarchar(2048);

  IF NOT EXISTS (SELECT 1 FROM dbo.GenTxBatches
                 WHERE GenTxBatchId = @GenTxBatchId AND (@CustomerId IS NULL OR CustomerId = @CustomerId))
  BEGIN
    SET @msg = CONCAT(N'GENTX_BATCH_NOT_FOUND: Core batch ', @GenTxBatchId, N' does not exist');
    THROW 51011, @msg, 1;
  END

  SELECT WireId, ExternalWireId, ChunkId, RowNumber, OriginatingAccount, ProcessDate, AmountCents, Currency,
         BeneficiaryName, BeneficiaryAccount, BeneficiaryRouting, BeneficiaryBank, Reference, Subsidiary,
         Status, RejectCode, RejectMessage, ApprovalRef, UpdatedAt
  FROM dbo.Wires
  WHERE GenTxBatchId = @GenTxBatchId AND (@Status IS NULL OR Status = @Status)
  ORDER BY RowNumber
  OFFSET @Offset ROWS FETCH NEXT @Limit ROWS ONLY;

  SELECT Total = COUNT(*) FROM dbo.Wires
  WHERE GenTxBatchId = @GenTxBatchId AND (@Status IS NULL OR Status = @Status);
END;
GO

-- Accounts with balances and holds: one customer's (dashboard widget), or every customer's at this FI
-- when @CustomerId is NULL (bank operations' accounts tab and the Control Center's hold list).
CREATE OR ALTER PROCEDURE dbo.usp_GetCustomerAccounts
  @CustomerId varchar(64) = NULL
AS
BEGIN
  SET NOCOUNT ON;
  SELECT a.AccountNumber, a.CustomerId, CustomerName = c.Name, a.Nickname, a.Status, a.HoldReason,
         a.AvailableBalanceCents, a.DailyWireLimitCents
  FROM dbo.Accounts a
  JOIN dbo.Customers c ON c.CustomerId = a.CustomerId
  WHERE @CustomerId IS NULL OR a.CustomerId = @CustomerId
  ORDER BY c.Name, a.AccountNumber;
END;
GO

/* ---------------------------------------------------------------------------------------------------
   Admin and DEMO-ONLY procedures.
--------------------------------------------------------------------------------------------------- */

-- Places or releases a hold. A legitimate bank-staff action (BankOps users in the portal); the Control
-- Center uses the same procedure through core-api's admin route.
CREATE OR ALTER PROCEDURE dbo.usp_SetAccountHold
  @AccountNumber varchar(17),
  @OnHold        bit,
  @Reason        nvarchar(200) = NULL
AS
BEGIN
  SET NOCOUNT ON; SET XACT_ABORT ON;
  DECLARE @Status varchar(10), @msg nvarchar(2048);

  SELECT @Status = Status FROM dbo.Accounts WHERE AccountNumber = @AccountNumber;
  IF @Status IS NULL
  BEGIN
    SET @msg = CONCAT(N'ACCOUNT_NOT_FOUND: account ', dbo.fn_MaskAccount(@AccountNumber), N' is not known to the bank');
    THROW 51003, @msg, 1;
  END
  IF @Status = 'Closed'
  BEGIN
    SET @msg = CONCAT(N'ACCOUNT_CLOSED: account ', dbo.fn_MaskAccount(@AccountNumber), N' is closed');
    THROW 51002, @msg, 1;
  END

  UPDATE dbo.Accounts
     SET Status = CASE WHEN @OnHold = 1 THEN 'OnHold' ELSE 'Active' END,
         HoldReason = CASE WHEN @OnHold = 1 THEN @Reason END
   WHERE AccountNumber = @AccountNumber;

  SELECT a.AccountNumber, a.CustomerId, CustomerName = c.Name, a.Nickname, a.Status, a.HoldReason,
         a.AvailableBalanceCents, a.DailyWireLimitCents
  FROM dbo.Accounts a
  JOIN dbo.Customers c ON c.CustomerId = a.CustomerId
  WHERE a.AccountNumber = @AccountNumber;
END;
GO

-- DEMO-ONLY: clears every batch, wire, audit row, and approval and reactivates every account, so a
-- presenter can rerun scenarios without daily limits accumulating across runs.
CREATE OR ALTER PROCEDURE dbo.usp_DemoResetTransactions
AS
BEGIN
  SET NOCOUNT ON; SET XACT_ABORT ON;
  BEGIN TRY
    BEGIN TRAN;
      DELETE FROM dbo.WireAudit;
      DELETE FROM dbo.Wires;
      DELETE FROM dbo.ApprovalSignatures;
      DELETE FROM dbo.Approvals;
      DELETE FROM dbo.GenTxBatches;
      UPDATE dbo.Accounts SET Status = 'Active', HoldReason = NULL WHERE Status <> 'Active';
    COMMIT;
  END TRY
  BEGIN CATCH
    IF @@TRANCOUNT > 0 ROLLBACK;
    THROW;
  END CATCH;
END;
GO

-- DEMO-ONLY: used by the `sql-timeout` fault. core-api runs it with the normal 30 s command timeout, so the
-- client gives up first and raises a REAL SqlException timeout (error number -2). Nothing is simulated
-- in application code: this is what a blocked or overloaded database looks like to the API.
CREATE OR ALTER PROCEDURE dbo.usp_SimulateLongRunningLock
AS
BEGIN
  SET NOCOUNT ON;
  WAITFOR DELAY '00:00:35';
END;
GO
