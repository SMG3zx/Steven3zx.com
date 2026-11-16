namespace Vinci.Memory;

/// The memory API and its consolidation worker, run inside the vinci process for as
/// long as the chat runs. Nothing is started if the API already answers (the
/// separate `vinci memory serve` and `vinci memory worker` processes still work), if
/// it points at another machine, or if MEMORY_EMBEDDED=0.
public sealed class EmbeddedMemory : IAsyncDisposable
{
    private readonly CancellationTokenSource stop = new();
    private readonly List<Task> tasks = [];

    public bool Started => tasks.Count > 0;

    public static async Task<EmbeddedMemory> StartIfNeeded(
        string baseUrl,
        TextWriter output,
        TextWriter errors
    )
    {
        var embedded = new EmbeddedMemory();
        if (Env.Get("MEMORY_EMBEDDED") == "0" || !IsLoopback(baseUrl))
            return embedded;
        var status = await ProviderStatus.Probe(baseUrl, path: "/health");
        if (status.State is "ready" or "reachable")
            return embedded;

        embedded.tasks.Add(Supervise("api", errors, () => MemoryCommands.ServeAsync(embedded.stop.Token, TextWriter.Null)));
        embedded.tasks.Add(Supervise("worker", errors, () => MemoryCommands.WorkAsync(embedded.stop.Token, TextWriter.Null)));

        var deadline = DateTime.UtcNow.AddSeconds(20);
        while (DateTime.UtcNow < deadline)
        {
            if ((await ProviderStatus.Probe(baseUrl, path: "/health")).State == "ready")
            {
                await output.WriteLineAsync(
                    $"Memory service started inside vinci at {baseUrl} (it stops when the chat does)."
                );
                return embedded;
            }
            if (embedded.tasks.Any(t => t.IsCompleted))
                break;
            await Task.Delay(500);
        }
        await output.WriteLineAsync(
            "Memory service did not come up inside vinci; the chat carries on without memory."
        );
        return embedded;
    }

    /// Runs one part of the memory system and reports, rather than throws, if it stops.
    private static Task Supervise(string name, TextWriter errors, Func<Task> body) =>
        Task.Run(async () =>
        {
            try
            {
                await body();
            }
            catch (Exception e) when (e is not OperationCanceledException)
            {
                await errors.WriteLineAsync($"vinci: embedded memory {name} stopped: {e.Message}");
            }
        });

    public async ValueTask DisposeAsync()
    {
        if (!Started)
            return;
        stop.Cancel();
        try
        {
            await Task.WhenAll(tasks).WaitAsync(TimeSpan.FromSeconds(20));
        }
        catch (Exception e) when (e is TimeoutException or OperationCanceledException)
        {
            // Stopped late; the process exits anyway.
        }
        stop.Dispose();
    }

    private static bool IsLoopback(string baseUrl) =>
        new Uri(baseUrl).Host is "127.0.0.1" or "localhost" or "::1" or "[::1]";
}
