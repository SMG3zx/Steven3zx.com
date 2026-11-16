using System.Net;
using System.Text;
using System.Text.RegularExpressions;

namespace Vinci.Chat;

/// Plain text from a file or a web page, cut into passages for memory. The read tool
/// saves these so that recall can find them, each with the title and source it came from.
public static partial class Reading
{
    public const int PassageChars = 1200;
    public const int MaxBytes = 1_000_000;
    public const int MaxPassages = 500;
    private static readonly string[] TextExtensions = [".txt", ".md", ".markdown"];

    public static bool IsTextFile(string path) =>
        TextExtensions.Contains(Path.GetExtension(path), StringComparer.OrdinalIgnoreCase);

    /// Cuts text at blank lines into passages of at most PassageChars. A paragraph
    /// longer than that is cut into pieces of its own.
    public static List<string> Passages(string text)
    {
        var passages = new List<string>();
        var current = new StringBuilder();
        foreach (
            var paragraph in Blank()
                .Split(text.Replace("\r\n", "\n"))
                .Select(p => p.Trim())
                .Where(p => p.Length > 0)
        )
        {
            for (var start = 0; start < paragraph.Length; start += PassageChars)
            {
                var part = paragraph.Substring(start, Math.Min(PassageChars, paragraph.Length - start));
                if (current.Length > 0 && current.Length + 2 + part.Length > PassageChars)
                {
                    passages.Add(current.ToString());
                    current.Clear();
                }
                if (current.Length > 0)
                    current.Append("\n\n");
                current.Append(part);
            }
        }
        if (current.Length > 0)
            passages.Add(current.ToString());
        return passages;
    }

    /// The readable text of an HTML page: scripts and styles dropped, tags replaced by
    /// line breaks, entities decoded.
    public static string FromHtml(string html) =>
        WebUtility.HtmlDecode(Tag().Replace(Hidden().Replace(html, " "), "\n"));

    public static string? HtmlTitle(string html) =>
        Title().Match(html) is { Success: true } m
            ? WebUtility.HtmlDecode(m.Groups[1].Value).Trim()
            : null;

    [GeneratedRegex(@"\n\s*\n")]
    private static partial Regex Blank();

    [GeneratedRegex(@"<(script|style)\b[^>]*>.*?</\1\s*>", RegexOptions.IgnoreCase | RegexOptions.Singleline)]
    private static partial Regex Hidden();

    [GeneratedRegex(@"<[^>]+>")]
    private static partial Regex Tag();

    [GeneratedRegex(@"<title[^>]*>(.*?)</title\s*>", RegexOptions.IgnoreCase | RegexOptions.Singleline)]
    private static partial Regex Title();
}
