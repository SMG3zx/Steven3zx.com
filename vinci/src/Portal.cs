using System.Collections.Concurrent;
using System.Diagnostics;
using System.Globalization;
using System.Net;
using System.Text;
using System.Text.Json;
using System.Text.RegularExpressions;

namespace Vinci;

/// One card on the portal page. Discovered links were found by asking this
/// machine what it serves, rather than written down by anyone.
public sealed record Link(string Title, string Url, string? Description, IReadOnlyList<string> Tags, bool Discovered = false)
{
    /// The name of the screenshot. A discovered title is whatever the page
    /// calls itself, so its port names the picture instead.
    public string Preview => Discovered ? $"local-{Portal.LocalPortOf(Url)}" : Portal.Slug(Title);
}

/// The portal.json files a repository tracks, as full paths.
public delegate IReadOnlyList<string> PortalFiles(string repository);

/// The page of saved links: what goes on it and how it is drawn.
public static partial class Portal
{
    /// File-safe name from a title: "Qwik Docs" becomes "qwik-docs".
    public static string Slug(string title) => NotSlug().Replace(title.ToLowerInvariant(), "-").Trim('-');

    [GeneratedRegex("[^a-z0-9]+")]
    private static partial Regex NotSlug();

    [GeneratedRegex(@"<title[^>]*>([^<]*)</title>", RegexOptions.IgnoreCase)]
    private static partial Regex TitleTag();

    [GeneratedRegex(@"\s+")]
    private static partial Regex Spaces();

    /// The links in the text of a portal.json: an array of
    /// { title, url, description?, tags? }. Entries that are not a titled
    /// http link are left out rather than stopping the page.
    public static IReadOnlyList<Link> Parse(string json)
    {
        var links = new List<Link>();
        try
        {
            using var doc = JsonDocument.Parse(json);
            if (doc.RootElement.ValueKind != JsonValueKind.Array) return links;
            foreach (var e in doc.RootElement.EnumerateArray())
            {
                if (e.ValueKind != JsonValueKind.Object) continue;
                var title = Text(e, "title")?.Trim();
                var url = Text(e, "url");
                if (string.IsNullOrEmpty(title) || !IsWebAddress(url)) continue;
                var tags = e.TryGetProperty("tags", out var t) && t.ValueKind == JsonValueKind.Array
                    ? t.EnumerateArray().Where(x => x.ValueKind == JsonValueKind.String).Select(x => x.GetString()!).ToList()
                    : [];
                links.Add(new Link(title, url!, Text(e, "description"), tags));
            }
        }
        catch (JsonException)
        {
            // A broken file contributes nothing.
        }
        return links;

        static string? Text(JsonElement e, string name) =>
            e.TryGetProperty(name, out var v) && v.ValueKind == JsonValueKind.String ? v.GetString() : null;
    }

    public static bool IsWebAddress(string? url) =>
        Uri.TryCreate(url, UriKind.Absolute, out var parsed) && parsed.Scheme is "http" or "https";

    /// Every link somebody wrote down: the bookmarks in the journal, then the
    /// portal.json of every project in each repository an idea lives in.
    /// A project lists its own links by keeping that file at its root.
    /// The first link to a URL wins. cold-storage folders are never read.
    public static IReadOnlyList<Link> Saved(State s, PortalFiles files)
    {
        var links = s.Bookmarks.Select(b => new Link(b.Title, b.Url, b.Why, b.Tags)).ToList();
        var repositories = s.Ideas.Where(i => i.Home is not null && i.Stage != Stage.Retired)
            .Select(i => Tracker.RepositoryOf(i.Home!)).OfType<string>().Distinct();
        foreach (var file in repositories.SelectMany(r => files(r)).Order(StringComparer.Ordinal))
        {
            if (file.Split(Path.DirectorySeparatorChar).Contains("cold-storage") || !File.Exists(file)) continue;
            links.AddRange(Parse(File.ReadAllText(file)));
        }
        return [.. links.DistinctBy(l => l.Url)];
    }

    /// Ports listening on localhost, from the text of /proc/net/tcp or tcp6.
    public static IReadOnlyList<int> ListeningPorts(string procNetTcp)
    {
        // Loopback and "any", for IPv4 and IPv6: what a browser here reaches as localhost.
        string[] local = ["0100007F", "00000000", "00000000000000000000000001000000", "00000000000000000000000000000000"];
        var ports = new SortedSet<int>();
        foreach (var line in procNetTcp.Split('\n').Skip(1))
        {
            var fields = line.Split((char[]?)null, StringSplitOptions.RemoveEmptyEntries);
            // 0A is the socket state "listening".
            if (fields.Length < 4 || fields[3] != "0A") continue;
            var address = fields[1].Split(':');
            if (address.Length == 2 && local.Contains(address[0]) && int.TryParse(address[1], NumberStyles.HexNumber, CultureInfo.InvariantCulture, out var port))
                ports.Add(port);
        }
        return [.. ports];
    }

    /// The text of the first title tag, entities decoded and spaces collapsed.
    public static string? TitleOf(string html)
    {
        var match = TitleTag().Match(html);
        if (!match.Success) return null;
        var title = Spaces().Replace(WebUtility.HtmlDecode(match.Groups[1].Value), " ").Trim();
        return title.Length > 0 ? title : null;
    }

    /// The port a link points at on this machine, if it points here at all.
    public static int? LocalPortOf(string url) =>
        Uri.TryCreate(url, UriKind.Absolute, out var parsed) && parsed.Host is "localhost" or "127.0.0.1" or "[::1]"
            ? parsed.Port
            : null;

    /// Saved links, then the discovered ones that no saved link already covers.
    public static IReadOnlyList<Link> Merge(IReadOnlyList<Link> saved, IReadOnlyList<Link> discovered, int ownPort)
    {
        var covered = saved.Select(l => LocalPortOf(l.Url)).OfType<int>().Append(ownPort).ToHashSet();
        return [.. saved, .. discovered.Where(l => !covered.Contains(LocalPortOf(l.Url)!.Value))];
    }

    public static string Page(IReadOnlyList<Link> links)
    {
        static string H(string? s) => WebUtility.HtmlEncode(s ?? "");
        var html = new StringBuilder();
        html.Append(CultureInfo.InvariantCulture, $"""
            <!doctype html>
            <html lang="en">
            <head>
            <meta charset="utf-8">
            <meta name="viewport" content="width=device-width, initial-scale=1">
            <meta name="description" content="Saved links, searchable and filterable by tag.">
            <title>Portal</title>
            <style>{Css}</style>
            </head>
            <body>
            <main class="portal">
            <header><h1>Portal</h1><p class="count"><span id="shown">{links.Count}</span> of {links.Count} links</p></header>
            <input class="search" id="search" type="search" placeholder="Search links, tags, descriptions…" aria-label="Search links">
            <nav class="tags" aria-label="Filter by tag"><button type="button" class="tag active" data-tag="">all</button>
            """);
        foreach (var tag in links.SelectMany(l => l.Tags).Distinct().Order(StringComparer.OrdinalIgnoreCase))
            html.Append(CultureInfo.InvariantCulture, $"""<button type="button" class="tag" data-tag="{H(tag)}">{H(tag)}</button>""");
        html.Append("""</nav><p class="empty" id="empty" hidden>No links match.</p><ul class="links">""");
        foreach (var link in links)
        {
            var words = string.Join(' ', [link.Title, link.Url, link.Description ?? "", .. link.Tags]).ToLowerInvariant();
            html.Append(CultureInfo.InvariantCulture, $"""
                <li class="card" data-text="{H(words)}" data-tags="{H(string.Join('\n', link.Tags))}">
                <a href="{H(link.Url)}" target="_blank" rel="noopener noreferrer"><span class="title">{H(link.Title)}</span><span class="host">{H(HostOf(link.Url))}</span></a>
                <img class="preview" src="/previews/{H(link.Preview)}.png" alt="Screenshot of {H(link.Title)}" width="1200" height="750" loading="lazy">
                """);
            if (link.Description is { Length: > 0 }) html.Append(CultureInfo.InvariantCulture, $"""<p class="description">{H(link.Description)}</p>""");
            if (link.Tags.Count > 0)
                html.Append(CultureInfo.InvariantCulture, $"""<ul class="card-tags">{string.Concat(link.Tags.Select(t => $"<li>{H(t)}</li>"))}</ul>""");
            html.Append("</li>");
        }
        html.Append(CultureInfo.InvariantCulture, $"</ul></main><script>{Script}</script></body></html>\n");
        return html.ToString();
    }

    private static string HostOf(string url) =>
        Uri.TryCreate(url, UriKind.Absolute, out var parsed)
            ? (parsed.Host.StartsWith("www.", StringComparison.Ordinal) ? parsed.Host[4..] : parsed.Host)
            : url;

    /// Shown until a link has a screenshot.
    public const string Placeholder = """
        <svg xmlns="http://www.w3.org/2000/svg" width="1200" height="750" viewBox="0 0 1200 750">
          <rect width="1200" height="750" fill="#e6e9ee"/>
          <g fill="none" stroke="#9aa3b0" stroke-width="10" stroke-linecap="round" stroke-linejoin="round" transform="translate(540 255) scale(5)">
            <rect x="2" y="3" width="20" height="18" rx="2"/>
            <path d="M2 8h20"/>
            <path d="M7 3v5M17 3v5"/>
          </g>
          <text x="600" y="520" text-anchor="middle" font-family="system-ui, -apple-system, Segoe UI, Roboto, sans-serif" font-size="40" fill="#646b76">Preview coming soon</text>
        </svg>
        """;

    /// Search and the tag buttons: hide the cards that do not match.
    private const string Script = """
        const cards = [...document.querySelectorAll('.card')], buttons = [...document.querySelectorAll('.tag')];
        const search = document.getElementById('search');
        let tag = '';
        function filter() {
          const q = search.value.trim().toLowerCase();
          let shown = 0;
          for (const card of cards) {
            const show = (!tag || card.dataset.tags.split('\n').includes(tag)) && (!q || card.dataset.text.includes(q));
            card.hidden = !show;
            if (show) shown++;
          }
          document.getElementById('shown').textContent = shown;
          document.getElementById('empty').hidden = shown > 0;
        }
        search.addEventListener('input', filter);
        for (const button of buttons) button.addEventListener('click', () => {
          tag = tag === button.dataset.tag ? '' : button.dataset.tag;
          for (const b of buttons) b.classList.toggle('active', b.dataset.tag === tag);
          filter();
        });
        """;

    private const string Css = """
        :root { --bg: #f6f7f9; --surface: #ffffff; --text: #1c1f24; --muted: #646b76; --border: #d9dde3; --accent: #2563eb; --accent-soft: #e6eefc; color-scheme: light dark; }
        @media (prefers-color-scheme: dark) { :root { --bg: #0f1115; --surface: #181b21; --text: #e6e8ec; --muted: #9aa3b0; --border: #2a2f38; --accent: #7aa7ff; --accent-soft: #1c2740; } }
        *, *::before, *::after { box-sizing: border-box; }
        [hidden] { display: none !important; }
        body { margin: 0; background: var(--bg); color: var(--text); font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; line-height: 1.5; }
        .portal { max-width: 760px; margin: 0 auto; padding: 2rem 16px 4rem; }
        .portal header { display: flex; align-items: baseline; justify-content: space-between; gap: 1rem; flex-wrap: wrap; }
        .portal h1 { margin: 0; font-size: 1.75rem; }
        .count, .host, .description, .empty { color: var(--muted); }
        .count { margin: 0; font-size: 0.9rem; }
        .search { width: 100%; margin-top: 1.25rem; padding: 0.7rem 0.9rem; font: inherit; color: var(--text); background: var(--surface); border: 1px solid var(--border); border-radius: 8px; }
        .search:focus { outline: 2px solid var(--accent); outline-offset: 1px; }
        .tags { display: flex; flex-wrap: wrap; gap: 0.4rem; margin: 1rem 0 1.5rem; }
        .tag { padding: 0.25rem 0.7rem; font: inherit; font-size: 0.85rem; color: var(--muted); background: var(--surface); border: 1px solid var(--border); border-radius: 999px; cursor: pointer; }
        .tag.active { color: var(--accent); background: var(--accent-soft); border-color: var(--accent); }
        .links { list-style: none; margin: 0; padding: 0; display: grid; gap: 0.75rem; }
        .card { padding: 1rem 1.1rem; background: var(--surface); border: 1px solid var(--border); border-radius: 10px; }
        .card a { display: flex; flex-wrap: wrap; align-items: baseline; gap: 0.6rem; color: var(--accent); text-decoration: none; font-weight: 600; }
        .card a:hover .title { text-decoration: underline; }
        .host { font-size: 0.85rem; font-weight: normal; }
        .preview { display: block; width: 100%; height: auto; margin-top: 0.75rem; border: 1px solid var(--border); border-radius: 6px; }
        .description { margin: 0.35rem 0 0; font-size: 0.92rem; }
        .card-tags { display: flex; flex-wrap: wrap; gap: 0.35rem; margin: 0.6rem 0 0; padding: 0; list-style: none; }
        .card-tags li { font-size: 0.75rem; padding: 0.1rem 0.5rem; color: var(--muted); border: 1px solid var(--border); border-radius: 4px; }
        """;
}

/// Serves the portal page on localhost: `vinci serve`. Each request reads the
/// journal again, so a new bookmark shows up on the next reload.
public sealed class PortalServer(Journal journal, int port)
{
    /// How long one scan of local ports is reused before a request starts the next.
    private static readonly TimeSpan ScanEvery = TimeSpan.FromSeconds(30);
    /// A page that could not be photographed is tried again after this long.
    private static readonly TimeSpan RetryCapture = TimeSpan.FromHours(1);
    private static readonly HttpClient Http = new() { Timeout = TimeSpan.FromSeconds(15) };

    private readonly string previews = Path.Combine(Path.GetDirectoryName(Path.GetFullPath(journal.Path))!, "previews");
    private readonly string chromium = Environment.GetEnvironmentVariable("CHROMIUM") ?? "/usr/bin/chromium";
    private readonly ConcurrentDictionary<string, string> addressOf = new();
    private readonly ConcurrentDictionary<string, DateTimeOffset> tried = new();
    private readonly SemaphoreSlim oneCapture = new(1);
    private (DateTimeOffset At, IReadOnlyList<Link> Links)? scan;
    private Task<IReadOnlyList<Link>>? scanning;

    public async Task Run()
    {
        Directory.CreateDirectory(previews);
        using var listener = new HttpListener();
        listener.Prefixes.Add($"http://127.0.0.1:{port}/");
        listener.Start();
        Console.WriteLine($"Portal is at http://127.0.0.1:{port}/");
        while (true)
        {
            var context = await listener.GetContextAsync();
            _ = Task.Run(async () =>
            {
                try { await Answer(context); }
                catch (Exception e) when (e is HttpListenerException or IOException or ObjectDisposedException) { }
                finally { context.Response.Close(); }
            });
        }
    }

    private async Task Answer(HttpListenerContext context)
    {
        var path = context.Request.Url!.AbsolutePath;
        if (path == "/")
        {
            var saved = Portal.Saved(State.From(journal.Read()), Git.PortalFiles);
            var links = Portal.Merge(saved, await Discovered(), port);
            foreach (var link in links) addressOf[link.Preview] = link.Url;
            await Send(context, "text/html; charset=utf-8", Encoding.UTF8.GetBytes(Portal.Page(links)));
        }
        else if (path.StartsWith("/previews/", StringComparison.Ordinal) && path.EndsWith(".png", StringComparison.Ordinal))
        {
            var slug = Portal.Slug(path["/previews/".Length..^4]);
            var file = Path.Combine(previews, slug + ".png");
            if (File.Exists(file))
            {
                await Send(context, "image/png", await File.ReadAllBytesAsync(file));
                return;
            }
            // Take the picture in the background; the next reload shows it.
            if (addressOf.TryGetValue(slug, out var url)) _ = Capture(slug, url, file);
            context.Response.Headers["Cache-Control"] = "no-store";
            await Send(context, "image/svg+xml", Encoding.UTF8.GetBytes(Portal.Placeholder));
        }
        else
        {
            context.Response.StatusCode = 404;
        }
    }

    private static async Task Send(HttpListenerContext context, string type, byte[] body)
    {
        context.Response.ContentType = type;
        context.Response.ContentLength64 = body.Length;
        await context.Response.OutputStream.WriteAsync(body);
    }

    /// Web pages served on this machine. Only the first request waits for a
    /// scan; after that the last result is returned at once and a stale one
    /// is refreshed for the next request, so a slow port never holds the page.
    private async Task<IReadOnlyList<Link>> Discovered()
    {
        Task<IReadOnlyList<Link>>? running;
        lock (this)
        {
            if (scan is null || DateTimeOffset.UtcNow - scan.Value.At >= ScanEvery)
                scanning ??= Task.Run(Scan);
            running = scanning;
        }
        return scan?.Links ?? await running!;
    }

    private async Task<IReadOnlyList<Link>> Scan()
    {
        var ports = new[] { "/proc/net/tcp", "/proc/net/tcp6" }
            .SelectMany(file => File.Exists(file) ? Portal.ListeningPorts(File.ReadAllText(file)) : []).Distinct().Order();
        var found = await Task.WhenAll(ports.Select(Probe));
        IReadOnlyList<Link> links = [.. found.OfType<Link>()];
        lock (this)
        {
            scan = (DateTimeOffset.UtcNow, links);
            scanning = null;
        }
        return links;
    }

    /// A port is a web page when its front page answers with HTML.
    private static async Task<Link?> Probe(int port)
    {
        try
        {
            using var patience = new CancellationTokenSource(TimeSpan.FromMilliseconds(1500));
            using var reply = await Http.GetAsync($"http://127.0.0.1:{port}/", HttpCompletionOption.ResponseHeadersRead, patience.Token);
            if (!reply.IsSuccessStatusCode || reply.Content.Headers.ContentType?.MediaType != "text/html") return null;
            var html = await reply.Content.ReadAsStringAsync(patience.Token);
            return new Link(Portal.TitleOf(html) ?? $"localhost:{port}", $"http://localhost:{port}", $"Found running on port {port}.", ["local"], Discovered: true);
        }
        catch (Exception e) when (e is HttpRequestException or OperationCanceledException or IOException)
        {
            return null;
        }
    }

    /// Photographs a page with headless Chromium, one page at a time.
    private async Task Capture(string slug, string url, string file)
    {
        var now = DateTimeOffset.UtcNow;
        if (!File.Exists(chromium) || (tried.TryGetValue(slug, out var last) && now - last < RetryCapture)) return;
        tried[slug] = now;
        await oneCapture.WaitAsync();
        try
        {
            // An error page gets no screenshot.
            using (var reply = await Http.GetAsync(url, HttpCompletionOption.ResponseHeadersRead))
                if (!reply.IsSuccessStatusCode) return;
            var draft = Path.ChangeExtension(file, ".tmp.png");
            var start = new ProcessStartInfo(chromium) { RedirectStandardOutput = true, RedirectStandardError = true };
            string[] args = ["--headless=new", "--disable-gpu", "--hide-scrollbars", "--window-size=1200,750",
                "--virtual-time-budget=8000", $"--screenshot={draft}", url];
            foreach (var arg in args) start.ArgumentList.Add(arg);
            using var browser = Process.Start(start)!;
            browser.OutputDataReceived += (_, _) => { };
            browser.ErrorDataReceived += (_, _) => { };
            browser.BeginOutputReadLine();
            browser.BeginErrorReadLine();
            using var patience = new CancellationTokenSource(TimeSpan.FromSeconds(60));
            try { await browser.WaitForExitAsync(patience.Token); }
            catch (OperationCanceledException) { browser.Kill(entireProcessTree: true); }
            if (File.Exists(draft)) File.Move(draft, file, overwrite: true);
        }
        catch (Exception e) when (e is HttpRequestException or OperationCanceledException or IOException or System.ComponentModel.Win32Exception)
        {
            // No picture this time; the placeholder stays.
        }
        finally
        {
            oneCapture.Release();
        }
    }
}
