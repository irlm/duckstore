using System.Text;

namespace DuckStore.Analytics.Etl;

/// <summary>A statement of a SQL script, with the label of its "-- step:" comment.</summary>
public sealed record SqlStatement(string Sql, string? Label);

// Splits a SQL script into statements on ';', like "GO" separates batches in SSMS.
// A ';' inside quotes, comments or $$dollar-quoted$$ strings does not split.
public static class SqlScript
{
    public static IReadOnlyList<SqlStatement> Split(string script)
    {
        var result = new List<SqlStatement>();
        var start = 0;
        var i = 0;
        while (i < script.Length)
        {
            var c = script[i];
            if (c is '\'' or '"')
            {
                i = SkipQuoted(script, i, c);
            }
            else if (c == '-' && Peek(script, i + 1) == '-')
            {
                while (i < script.Length && script[i] != '\n') i++;
            }
            else if (c == '/' && Peek(script, i + 1) == '*')
            {
                var end = script.IndexOf("*/", i + 2, StringComparison.Ordinal);
                i = end < 0 ? script.Length : end + 2;
            }
            else if (c == '$')
            {
                i = SkipDollarQuoted(script, i);
            }
            else if (c == ';')
            {
                Add(result, script[start..i]);
                start = ++i;
            }
            else
            {
                i++;
            }
        }
        Add(result, script[start..]);
        return result;
    }

    private static char Peek(string s, int i) => i < s.Length ? s[i] : '\0';

    private static int SkipQuoted(string s, int i, char quote)
    {
        i++;
        while (i < s.Length)
        {
            if (s[i] == quote)
            {
                if (Peek(s, i + 1) == quote) { i += 2; continue; } // escaped '' or ""
                return i + 1;
            }
            i++;
        }
        return i;
    }

    // $$...$$ or $tag$...$tag$. A '$' that does not start a tag ($1 parameters) is one character.
    private static int SkipDollarQuoted(string s, int i)
    {
        var j = i + 1;
        while (j < s.Length && (s[j] == '_' || char.IsAsciiLetter(s[j]) || (j > i + 1 && char.IsAsciiDigit(s[j])))) j++;
        if (j >= s.Length || s[j] != '$') return i + 1;
        var tag = s[i..(j + 1)];
        var end = s.IndexOf(tag, j + 1, StringComparison.Ordinal);
        return end < 0 ? s.Length : end + tag.Length;
    }

    private static void Add(List<SqlStatement> result, string piece)
    {
        string? label = null;
        var hasCode = false;
        foreach (var raw in piece.Split('\n'))
        {
            var line = raw.Trim();
            if (!hasCode && line.StartsWith("-- step:", StringComparison.Ordinal))
            {
                label = line["-- step:".Length..].Trim();
            }
            if (line.Length > 0 && !line.StartsWith("--", StringComparison.Ordinal))
            {
                hasCode = true;
            }
        }
        if (hasCode && StripComments(piece).Trim().Length > 0)
        {
            result.Add(new SqlStatement(piece.Trim(), label));
        }
    }

    private static string StripComments(string s)
    {
        var sb = new StringBuilder();
        var i = 0;
        while (i < s.Length)
        {
            if (s[i] is '\'' or '"')
            {
                var j = SkipQuoted(s, i, s[i]);
                sb.Append(s, i, j - i);
                i = j;
            }
            else if (s[i] == '-' && Peek(s, i + 1) == '-')
            {
                while (i < s.Length && s[i] != '\n') i++;
            }
            else if (s[i] == '/' && Peek(s, i + 1) == '*')
            {
                var end = s.IndexOf("*/", i + 2, StringComparison.Ordinal);
                if (end < 0) break;
                i = end + 2;
            }
            else
            {
                sb.Append(s[i++]);
            }
        }
        return sb.ToString();
    }
}
