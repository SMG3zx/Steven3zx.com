using System.Net;
using System.Text;
using System.Text.Json;

string mode = args[0];
switch (mode)
{
    case "entities":
        foreach (var name in File.ReadAllLines(args[1]))
        {
            var withSemi = WebUtility.HtmlDecode("&" + name + ";");
            var bare = WebUtility.HtmlDecode("&" + name);
            Console.WriteLine($"{name}\t{Hex(withSemi)}\t{Hex(bare)}");
        }
        break;
    case "numeric":
        foreach (var s in File.ReadAllLines(args[1]))
        {
            var text = s.Replace("\\n", "\n");
            Console.WriteLine($"{Esc(text)}\t{Hex(WebUtility.HtmlDecode(text))}");
        }
        break;
    case "encode":
        for (int c = 0; c < 0x10000; c++)
        {
            var s = ((char)c).ToString();
            var e = WebUtility.HtmlEncode(s);
            if (e != s) Console.WriteLine($"{c:X4}\t{e}");
        }
        foreach (var s in new[] { "\U0001F600", "\U00010000", "a\u2028b", "\uFFFD" })
            Console.WriteLine($"astral {Hex(s)}\t{WebUtility.HtmlEncode(s)}");
        break;
    case "lower":
        for (int c = 0; c <= 0x10FFFF; c++)
        {
            if (c >= 0xD800 && c <= 0xDFFF) continue;
            var s = char.ConvertFromUtf32(c);
            var l = s.ToLowerInvariant();
            if (l != s) Console.WriteLine($"{c:X}\t{Hex(l)}");
        }
        break;
    case "uri":
        foreach (var s in File.ReadAllLines(args[1]))
        {
            var ok = Uri.TryCreate(s, UriKind.Absolute, out var u);
            if (!ok) { Console.WriteLine($"{Esc(s)}\tno"); continue; }
            Console.WriteLine($"{Esc(s)}\tyes\t{u!.Scheme}\t{u.Host}\t{u.Port}\t{u.AbsolutePath}\t{u.PathAndQuery}");
        }
        break;
    case "target":
        foreach (var s in File.ReadAllLines(args[1]))
        {
            var ok = Uri.TryCreate("http://127.0.0.1:4000" + s, UriKind.Absolute, out var u);
            Console.WriteLine($"{Esc(s)}\t{(ok ? u!.AbsolutePath : "BAD")}");
        }
        break;
    case "json":
        foreach (var f in Directory.GetFiles(args[1]).OrderBy(x => x, StringComparer.Ordinal))
        {
            var text = File.ReadAllText(f);
            string r;
            try { using var d = JsonDocument.Parse(text); r = "ok " + d.RootElement.ValueKind; }
            catch (JsonException e) { r = "error " + e.GetType().Name; }
            Console.WriteLine($"{Path.GetFileName(f)}\t{r}");
        }
        break;
    case "depth":
        for (int n = 60; n <= 70; n++)
        {
            var text = new string('[', n) + new string(']', n);
            try { using var d = JsonDocument.Parse(text); Console.WriteLine($"depth {n} ok"); }
            catch (JsonException) { Console.WriteLine($"depth {n} error"); }
        }
        break;
    case "escapedkey":
        using (var d = JsonDocument.Parse("[{\"tit\\u006ce\":\"via escape\"}]"))
            Console.WriteLine("escaped key: " + (d.RootElement[0].TryGetProperty("title", out var ev) ? ev.GetString() : "none"));
        break;
    case "dup":
        using (var d = JsonDocument.Parse("[{\"title\":\"a\",\"title\":\"b\"}]"))
            Console.WriteLine("dup " + d.RootElement[0].GetProperty("title").GetString());
        using (var d = JsonDocument.Parse("[{\"title\":\"a\",\"title\":\"b\"}]"))
            Console.WriteLine("dup tryget " + (d.RootElement[0].TryGetProperty("title", out var v) ? v.GetString() : "none"));
        break;
    case "surrogate":
        foreach (var s in new[] { "[\"\\uD800\"]", "[\"\\uDC00\"]", "[\"\\uD83D\\uDE00\"]", "[\"\\uD83Dx\"]" })
        {
            try { using var d = JsonDocument.Parse(s); Console.WriteLine($"{s} ok {Hex(d.RootElement[0].GetString()!)}"); }
            catch (JsonException e) { Console.WriteLine($"{s} error {e.Message}"); }
        }
        break;
}

static string Hex(string s) => string.Join(" ", s.EnumerateRunes().Select(r => r.Value.ToString("X")));
static string Esc(string s) => s.Replace("\\", "\\\\").Replace("\t", "\\t").Replace("\n", "\\n");
