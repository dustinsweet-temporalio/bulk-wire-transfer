// Tvp: builds Table-Valued Parameters for dbo.WireDraftTvp and dbo.AccountNumberTvp.
//
// A TVP streams a whole chunk (up to 1,000 wires) to a stored procedure in ONE round trip, and the procedure
// handles it set-based. The SqlMetaData arrays below must match sql/02-types.sql column for column, in
// order: SQL Server binds TVP columns by POSITION, not by name. CoreApi.Tests reads 02-types.sql to check it.
// (A DataTable also works; IEnumerable<SqlDataRecord> streams rows without materializing a table.)
using System.Data;
using CoreApi.Contracts;
using Microsoft.Data.SqlClient;
using Microsoft.Data.SqlClient.Server;

namespace CoreApi.Data;

public static class Tvp
{
    public static readonly SqlMetaData[] WireDraftColumns =
    [
        new("ExternalWireId", SqlDbType.VarChar, 64),
        new("RowNumber", SqlDbType.Int),
        new("OriginatingAccount", SqlDbType.VarChar, 17),
        new("ProcessDate", SqlDbType.Date),
        new("AmountCents", SqlDbType.BigInt),
        new("Currency", SqlDbType.Char, 3),
        new("BeneficiaryName", SqlDbType.NVarChar, 70),
        new("BeneficiaryAccount", SqlDbType.VarChar, 17),
        new("BeneficiaryRouting", SqlDbType.Char, 9),
        new("BeneficiaryBank", SqlDbType.NVarChar, 70),
        new("Reference", SqlDbType.NVarChar, 140),
        new("Subsidiary", SqlDbType.NVarChar, 70),
    ];

    public static readonly SqlMetaData[] AccountNumberColumns =
    [
        new("AccountNumber", SqlDbType.VarChar, 17),
    ];

    public static SqlParameter WireDraft(string name, IReadOnlyCollection<WireDto> wires) =>
        new(name, SqlDbType.Structured) { TypeName = "dbo.WireDraftTvp", Value = WireDraftRows(wires) };

    public static SqlParameter AccountNumbers(string name, IReadOnlyCollection<string> accounts) =>
        new(name, SqlDbType.Structured) { TypeName = "dbo.AccountNumberTvp", Value = AccountRows(accounts) };

    // One SqlDataRecord is reused for every row: SqlClient copies each row as it is enumerated.
    // Callers must not pass an empty collection (SqlClient rejects an empty record enumeration).
    private static IEnumerable<SqlDataRecord> WireDraftRows(IEnumerable<WireDto> wires)
    {
        var record = new SqlDataRecord(WireDraftColumns);
        foreach (var w in wires)
        {
            record.SetString(0, w.ExternalWireId);
            record.SetInt32(1, w.RowNumber);
            record.SetString(2, w.OriginatingAccount);
            record.SetDateTime(3, w.ProcessDate.ToDateTime(TimeOnly.MinValue));
            record.SetInt64(4, w.AmountCents);
            record.SetString(5, w.Currency);
            record.SetString(6, w.BeneficiaryName);
            record.SetString(7, w.BeneficiaryAccount);
            record.SetString(8, w.BeneficiaryRouting);
            SetOptional(record, 9, w.BeneficiaryBank);
            SetOptional(record, 10, w.Reference);
            SetOptional(record, 11, w.Subsidiary);
            yield return record;
        }
    }

    private static IEnumerable<SqlDataRecord> AccountRows(IEnumerable<string> accounts)
    {
        var record = new SqlDataRecord(AccountNumberColumns);
        foreach (var a in accounts)
        {
            record.SetString(0, a);
            yield return record;
        }
    }

    // Empty optional CSV fields are stored as NULL.
    private static void SetOptional(SqlDataRecord record, int ordinal, string? value)
    {
        if (string.IsNullOrEmpty(value)) record.SetDBNull(ordinal);
        else record.SetString(ordinal, value);
    }
}
