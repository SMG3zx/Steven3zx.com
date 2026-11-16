using System.Runtime.InteropServices;
using System.Text.Json;
using Vinci.Memory;

namespace Vinci.Memory;

/// `vinci memory <command>`: the processes of the memory system. They are
/// kept apart from the journal commands because they need a running
/// database and run for a long time instead of answering and exiting.
public static class MemoryCommands
{
    /// The REST API and web pages, until `stop` is cancelled.
    public static async Task ServeAsync(CancellationToken stop, TextWriter output)
    {
        if (MemoryServer.Production && Env.Get("MEMORY_API_KEY") is null)
            throw new InvalidOperationException("MEMORY_API_KEY is required in production");
        await using var store = new Neo4jStore();
        var audit = new AuditRegistry();
        audit.Load();
        await store.EnsureSchema();
        await new MemoryServer(
            new MemoryService(store, audit, new MetricsRegistry(), Consolidation.Create())
        ).Run(stop, output);
    }

    /// The consolidation worker, until `stop` is cancelled.
    public static async Task WorkAsync(CancellationToken stop, TextWriter output)
    {
        await using var store = new Neo4jStore();
        await new ConsolidationWorker(store, Consolidation.Create()).Run(stop, output);
    }

    public const string Help = """
        vinci memory: durable memory for agents, kept in Neo4j.

          serve      the REST API and web pages (MEMORY_HOST, MEMORY_PORT; default 127.0.0.1:4781)
          worker     consolidates queued events into memories, reflects on sessions
          mcp        the tools for an agent, over standard input and output
                     (MEMORY_API_URL, MEMORY_API_KEY)
          backup     writes a snapshot of the whole graph to BACKUP_DIR (default ./backups)
          restore <snapshot.json>
                     writes a snapshot back. RESTORE_DRY_RUN=1 only checks it;
                     a real restore needs RESTORE_CONFIRM=RESTORE_PERSONAL_MEMORY

        Measuring it (against a running API; MEMORY_API_URL, MEMORY_API_KEY):
          seed       writes the small world the bundled questions ask about
          benchmark  asks the bundled questions (or BENCHMARK_CASES_FILE) and reports the rates;
                     BENCHMARK_STRICT=1 fails on any miss
          ablation   the same, with each way of retrieving on its own (ABLATION_MODES)
          simulate   plays an invented history and probes it (SIMULATION_SEED, SIMULATION_LIVE=1)
          longmemeval
                     recall over LONGMEMEVAL_FILE, a LongMemEval release you have downloaded

        The database is set by NEO4J_URI, NEO4J_USERNAME and NEO4J_PASSWORD.
        """;

    public static async Task<int> Run(
        string[] args,
        TextReader input,
        TextWriter output,
        TextWriter errors
    )
    {
        using var stop = new CancellationTokenSource();
        // Ctrl-C and a stop from systemd both end the long-running commands cleanly.
        Console.CancelKeyPress += (_, e) =>
        {
            e.Cancel = true;
            stop.Cancel();
        };
        using var terminate = PosixSignalRegistration.Create(PosixSignal.SIGTERM, context =>
        {
            context.Cancel = true;
            stop.Cancel();
        });
        try
        {
            switch (args)
            {
                case ["mcp"]:
                    await new McpServer(
                        new HttpClient(),
                        Env.Get("MEMORY_API_URL") ?? "http://127.0.0.1:4781",
                        Env.Get("MEMORY_API_KEY")
                    ).Run(input, output, stop.Token);
                    return 0;
                case ["serve"]:
                    await ServeAsync(stop.Token, output);
                    return 0;
                case ["worker"]:
                    await WorkAsync(stop.Token, output);
                    return 0;
                case ["seed" or "benchmark" or "ablation" or "simulate" or "longmemeval"]:
                    return await Evaluate(args[0], output);
                case ["backup"]:
                    await using (var store = new Neo4jStore())
                    {
                        await store.Verify();
                        var snapshot = await store.ExportSnapshot();
                        var directory = Env.Get("BACKUP_DIR") ?? "./backups";
                        Directory.CreateDirectory(directory);
                        var path = Path.Combine(
                            directory,
                            $"personal-memory-{snapshot.ExportedAt.Replace(':', '-').Replace('.', '-')}.json"
                        );
                        await File.WriteAllTextAsync(
                            path,
                            JsonSerializer.Serialize(
                                snapshot,
                                new JsonSerializerOptions(Json.Options) { WriteIndented = true }
                            )
                        );
                        await output.WriteLineAsync(
                            JsonSerializer.Serialize(
                                new
                                {
                                    path,
                                    nodes = snapshot.Nodes.Count,
                                    relationships = snapshot.Relationships.Count,
                                }
                            )
                        );
                    }
                    return 0;
                case ["restore", var file]:
                    var restoring = JsonSerializer.Deserialize<MemorySnapshot>(
                        await File.ReadAllTextAsync(file),
                        Json.Options
                    ) ?? throw new FormatException("snapshot is empty");
                    var (nodes, relationships) = restoring.Validate();
                    if (Env.Get("RESTORE_DRY_RUN") == "1")
                    {
                        await output.WriteLineAsync(
                            JsonSerializer.Serialize(
                                new
                                {
                                    dryRun = true,
                                    file,
                                    nodes,
                                    relationships,
                                }
                            )
                        );
                        return 0;
                    }
                    if (Env.Get("RESTORE_CONFIRM") != "RESTORE_PERSONAL_MEMORY")
                        throw new InvalidOperationException(
                            "Restore is disabled without RESTORE_CONFIRM=RESTORE_PERSONAL_MEMORY"
                        );
                    await using (var store = new Neo4jStore())
                    {
                        await store.Verify();
                        await store.Restore(restoring);
                    }
                    await output.WriteLineAsync(
                        JsonSerializer.Serialize(
                            new
                            {
                                restoredNodes = nodes,
                                restoredRelationships = relationships,
                                file,
                            }
                        )
                    );
                    return 0;
                case [] or ["help"] or ["--help"]:
                    await output.WriteAsync(Help);
                    return 0;
                default:
                    await errors.WriteLineAsync(
                        $"vinci: unknown memory command '{string.Join(' ', args)}'. Try: vinci memory help"
                    );
                    return 1;
            }
        }
        catch (Exception e)
            when (e
                    is InvalidOperationException
                        or FormatException
                        or IOException
                        or JsonException
                        or Neo4j.Driver.Neo4jException
                        or HttpRequestException
            )
        {
            await errors.WriteLineAsync($"vinci: {e.Message}");
            return 1;
        }
    }

    private static readonly JsonSerializerOptions Pretty = new(Json.Options)
    {
        WriteIndented = true,
    };

    private static async Task<int> Evaluate(string what, TextWriter output)
    {
        using var http = new HttpClient();
        var (api, key) = (
            Env.BaseUrl("MEMORY_API_URL") ?? "http://127.0.0.1:4781",
            Env.Get("MEMORY_API_KEY")
        );
        switch (what)
        {
            case "seed":
                await Evaluation.Benchmark.Seed(http, api, key);
                await output.WriteLineAsync("Seeded project benchmark-suite.");
                return 0;
            case "benchmark":
                await output.WriteLineAsync(
                    (
                        await Evaluation.Benchmark.Run(
                            http,
                            api,
                            key,
                            Env.Get("BENCHMARK_CASES_FILE")
                        )
                    ).ToJsonString(Pretty)
                );
                return 0;
            case "ablation":
                await output.WriteLineAsync(
                    (
                        await Evaluation.Benchmark.Ablate(
                            http,
                            api,
                            key,
                            Env.Get("BENCHMARK_CASES_FILE")
                        )
                    ).ToJsonString(Pretty)
                );
                return 0;
            case "longmemeval":
                var file = Env.Get("LONGMEMEVAL_FILE")
                    ?? throw new InvalidOperationException(
                        "LONGMEMEVAL_FILE must point to a local LongMemEval JSON file. Download an authorized release from https://github.com/xiaowu0162/LongMemEval-V2 and set it."
                    );
                await output.WriteLineAsync(
                    (await Evaluation.LongMemEval.Run(http, api, key, file)).ToJsonString(Pretty)
                );
                return 0;
            default:
                var seed = (int)Env.Number("SIMULATION_SEED", 42);
                var live = Env.Get("SIMULATION_LIVE") == "1";
                var world = Evaluation.Simulator.Create(
                    seed,
                    live ? $"live-{Guid.NewGuid().ToString()[..12]}" : null
                );
                var seeds = (
                    Env.Get("SIMULATION_SEEDS")
                    ?? seed.ToString(System.Globalization.CultureInfo.InvariantCulture)
                )
                    .Split(',')
                    .Select(s => int.TryParse(s, out var n) ? n : (int?)null)
                    .OfType<int>()
                    .ToList();
                var report = live
                    ? await Evaluation.Simulator.RunAgainstApi(world, http, api, key)
                    : null;
                object shown =
                    report is not null ? report
                    : seeds.Count > 1
                        ? new
                        {
                            datasetVersion = world.DatasetVersion,
                            seeds,
                            reports = Evaluation.Simulator.OfflineMatrix(seeds),
                        }
                    : Evaluation.Simulator.SelfTest(seed);
                if (Env.Get("SIMULATION_OUTPUT_FILE") is { } outFile)
                    await File.WriteAllTextAsync(
                        outFile,
                        JsonSerializer.Serialize(new { world, report = shown }, Pretty)
                    );
                if (
                    Env.Get("SIMULATION_TRACE_FILE") is { } traceFile
                    && report?.Traces is { } traces
                )
                    await File.WriteAllTextAsync(
                        traceFile,
                        Evaluation.Simulator.ToOpenTelemetry(traces).ToJsonString(Pretty)
                    );
                await output.WriteLineAsync(
                    JsonSerializer.Serialize(
                        new
                        {
                            world = new
                            {
                                world.Seed,
                                world.UserId,
                                world.ProjectId,
                                events = world.Events.Count,
                                probes = world.Probes.Count,
                            },
                            report = shown,
                        },
                        Pretty
                    )
                );
                return 0;
        }
    }
}
