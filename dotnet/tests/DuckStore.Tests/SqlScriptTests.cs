using DuckStore.Web.Etl;

namespace DuckStore.Tests;

// Same cases as the Go splitter tests (internal/sqlsplit/sqlsplit_test.go).
public sealed class SqlScriptTests
{
    [Theory]
    [InlineData("SELECT 1; SELECT 2;", new[] { "SELECT 1", "SELECT 2" })]
    [InlineData("SELECT 1;\nSELECT 2", new[] { "SELECT 1", "SELECT 2" })]
    [InlineData("SELECT 'a;b'; SELECT 'it''s; fine'", new[] { "SELECT 'a;b'", "SELECT 'it''s; fine'" })]
    [InlineData("SELECT 1 AS \"x;y\"; SELECT 2", new[] { "SELECT 1 AS \"x;y\"", "SELECT 2" })]
    [InlineData("-- first; comment\nSELECT 1; -- trailing; comment\n", new[] { "-- first; comment\nSELECT 1" })]
    [InlineData("/* a; b */ SELECT 1; SELECT /* ; */ 2", new[] { "/* a; b */ SELECT 1", "SELECT /* ; */ 2" })]
    [InlineData("DO $$ BEGIN PERFORM 1; END $$; SELECT 2", new[] { "DO $$ BEGIN PERFORM 1; END $$", "SELECT 2" })]
    [InlineData("SELECT $fn$ ; $fn$; SELECT $1", new[] { "SELECT $fn$ ; $fn$", "SELECT $1" })]
    [InlineData("-- nothing here;\n/* nor here */;", new string[0])]
    public void Splits_on_semicolons_outside_quotes_and_comments(string script, string[] expected)
    {
        Assert.Equal(expected, SqlScript.Split(script).Select(s => s.Sql));
    }

    [Fact]
    public void Reads_step_labels()
    {
        var statements = SqlScript.Split("-- step: load orders\n-- more text\nCREATE TABLE a AS SELECT 1;\nSELECT 2;");
        Assert.Equal(["load orders", null], statements.Select(s => s.Label));
    }

    [Fact]
    public void Npgsql_connection_string_becomes_libpq()
    {
        var libpq = WarehouseBuilder.ToLibpq("Host=127.0.0.1;Port=55432;Database=store;Username=store;Password=it's secret;SSL Mode=Disable");
        Assert.Equal(@"host=127.0.0.1 port=55432 dbname=store user=store password='it\'s secret' sslmode=disable", libpq);
    }
}
