// SQL Server binds TVP columns by POSITION. If Tvp.cs and sql/02-types.sql ever disagree on order, wires are
// written with fields in the wrong columns, so the order is checked against the SQL file itself.
using System.Text.RegularExpressions;
using CoreApi.Data;

namespace CoreApi.Tests;

public sealed partial class TvpTests
{
    [Theory]
    [InlineData("dbo.WireDraftTvp")]
    [InlineData("dbo.AccountNumberTvp")]
    public void Metadata_matches_the_sql_type_definition(string typeName)
    {
        var expected = ColumnsOf(typeName);
        var actual = (typeName == "dbo.WireDraftTvp" ? Tvp.WireDraftColumns : Tvp.AccountNumberColumns)
            .Select(c => c.Name).ToArray();
        Assert.Equal(expected, actual);
    }

    private static string[] ColumnsOf(string typeName)
    {
        var sql = File.ReadAllText(Path.Combine(AppContext.BaseDirectory, "sql", "02-types.sql"));
        var start = sql.IndexOf($"CREATE TYPE {typeName} AS TABLE (", StringComparison.Ordinal);
        Assert.True(start >= 0, $"{typeName} not found in 02-types.sql");
        var body = sql[(start + sql[start..].IndexOf('(') + 1)..];
        body = body[..body.IndexOf(");", StringComparison.Ordinal)];
        return body.Split('\n')
            .Select(l => ColumnName().Match(l))
            .Where(m => m.Success)
            .Select(m => m.Groups[1].Value)
            .ToArray();
    }

    [GeneratedRegex(@"^\s*([A-Za-z]+)\s+(varchar|nvarchar|char|int|bigint|date)\b")]
    private static partial Regex ColumnName();
}
