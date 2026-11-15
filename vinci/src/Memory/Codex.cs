using System.Collections.Concurrent;
using System.Diagnostics;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;

namespace Vinci.Memory;

public sealed record ChatResult(string ThreadId, string TurnId, string Text, IReadOnlyList<JsonObject> Events, JsonObject? Usage);

/// A small JSON-RPC client for the local Codex App Server, which it starts
/// as a child process and talks to over standard input and output. Chat
/// answers arrive as a stream of notifications that are gathered per turn.
public sealed partial class CodexAppServerClient : IDisposable
{
    [GeneratedRegex("(token|cost|input|output|cached)", RegexOptions.IgnoreCase)]
    private static partial Regex UsageKey();

    private sealed class Turn
    {
        public string Text = "";
        public readonly List<JsonObject> Events = [];
        /// "completed", "failed" or "interrupted" once the turn has ended.
        public string? Status;
        public string ThreadId = "";
        public TaskCompletionSource<ChatResult>? Waiting;
    }

    private readonly Lock gate = new();
    private readonly ConcurrentDictionary<long, TaskCompletionSource<JsonNode?>> pending = new();
    private readonly Dictionary<string, Turn> turns = [];
    private readonly SemaphoreSlim writing = new(1);
    private Process? process;
    private long nextId;
    private bool initialized;
    private string? threadId;

    private void EnsureProcess()
    {
        lock (gate)
        {
            if (process is not null) return;
            var start = new ProcessStartInfo(Env.Get("CODEX_APP_SERVER_COMMAND") ?? "codex")
            {
                RedirectStandardInput = true, RedirectStandardOutput = true, RedirectStandardError = true,
            };
            start.ArgumentList.Add("app-server");
            start.ArgumentList.Add("--stdio");
            var started = Process.Start(start) ?? throw new InvalidOperationException("Codex App Server could not be started");
            started.ErrorDataReceived += (_, _) => { };
            started.BeginErrorReadLine();
            process = started;
            _ = Task.Run(() => ReadOutput(started));
        }
    }

    private async Task ReadOutput(Process running)
    {
        try
        {
            while (await running.StandardOutput.ReadLineAsync() is { } line)
            {
                if (line.Trim().Length == 0) continue;
                // Anything that is not JSON is a diagnostic line, not a message.
                try { if (JsonNode.Parse(line) is JsonObject message) Handle(message); }
                catch (JsonException) { }
            }
        }
        catch (IOException)
        {
        }
        var stopped = new InvalidOperationException("Codex App Server stopped");
        foreach (var waiting in pending.Values) waiting.TrySetException(stopped);
        pending.Clear();
        lock (gate)
        {
            foreach (var turn in turns.Values) turn.Waiting?.TrySetException(stopped);
            turns.Clear();
            if (process == running) (process, initialized) = (null, false);
        }
    }

    private void Handle(JsonObject message)
    {
        if (message["id"] is { } id && (message["result"] is not null || message["error"] is not null))
        {
            if (!pending.TryRemove((long)Json.Num(id), out var waiting)) return;
            if (message["error"] is { } error) waiting.TrySetException(new InvalidOperationException(Json.Str(error["message"], "Codex App Server request failed")));
            else waiting.TrySetResult(message["result"]?.DeepClone());
            return;
        }
        var method = Json.Str(message["method"]);
        if (method.Length == 0) return;
        var parameters = message["params"] as JsonObject ?? [];
        var turnId = Json.Str(parameters["turnId"] ?? (parameters["turn"] as JsonObject)?["id"]);
        if (turnId.Length == 0) return;
        lock (gate)
        {
            // A turn's first messages can arrive before chat() has learned its id.
            if (!turns.TryGetValue(turnId, out var turn)) turns[turnId] = turn = new Turn();
            turn.Events.Add((JsonObject)message.DeepClone());
            if (method == "item/agentMessage/delta" && parameters["delta"]?.GetValueKind() == JsonValueKind.String) turn.Text += Json.Str(parameters["delta"]);
            if (method is not ("turn/completed" or "turn/failed" or "turn/interrupted")) return;
            if (turn.Text.Length == 0) turn.Text = ExtractText(parameters);
            turn.Status = method["turn/".Length..];
            if (turn.Waiting is null) return;
            Settle(turnId, turn);
        }
    }

    private void Settle(string turnId, Turn turn)
    {
        turns.Remove(turnId);
        if (turn.Status == "completed") turn.Waiting!.TrySetResult(new ChatResult(turn.ThreadId, turnId, turn.Text, turn.Events, UsageFrom(turn.Events)));
        else turn.Waiting!.TrySetException(new InvalidOperationException($"Codex turn ended with {turn.Status}"));
    }

    private async Task Send(JsonObject message)
    {
        EnsureProcess();
        var input = (process ?? throw new InvalidOperationException("Codex App Server stdin is unavailable")).StandardInput;
        await writing.WaitAsync();
        try
        {
            await input.WriteLineAsync(message.ToJsonString(Json.Options));
            await input.FlushAsync();
        }
        finally
        {
            writing.Release();
        }
    }

    private async Task<JsonNode?> Request(string method, JsonObject parameters)
    {
        var id = Interlocked.Increment(ref nextId);
        var waiting = new TaskCompletionSource<JsonNode?>(TaskCreationOptions.RunContinuationsAsynchronously);
        pending[id] = waiting;
        await Send(new JsonObject { ["jsonrpc"] = "2.0", ["id"] = id, ["method"] = method, ["params"] = parameters });
        return await waiting.Task;
    }

    private async Task Initialize()
    {
        EnsureProcess();
        if (initialized) return;
        await Request("initialize", new JsonObject
        {
            ["clientInfo"] = new JsonObject { ["name"] = "vinci-memory", ["title"] = "vinci memory", ["version"] = "0.1.0" },
            ["capabilities"] = new JsonObject { ["experimentalApi"] = true },
        });
        await Send(new JsonObject { ["jsonrpc"] = "2.0", ["method"] = "initialized", ["params"] = new JsonObject() });
        initialized = true;
    }

    public async Task<JsonNode?> Account()
    {
        await Initialize();
        return await Request("account/read", new JsonObject { ["refreshToken"] = false });
    }

    public async Task<JsonNode?> BeginChatGPTLogin()
    {
        await Initialize();
        return await Request("account/login/start", new JsonObject { ["type"] = "chatgptDeviceCode" });
    }

    /// Sends one message and waits for the whole answer. A thread is kept
    /// between calls; when the server no longer knows it, a new one is started.
    public async Task<ChatResult> Chat(string message, string? thread = null, string? model = null)
    {
        await Initialize();
        async Task<string> StartThread()
        {
            var parameters = new JsonObject();
            if (!string.IsNullOrEmpty(model)) parameters["model"] = model;
            return Json.Str((await Request("thread/start", parameters))?["thread"]?["id"]);
        }
        Task<JsonNode?> StartTurn(string onThread) => Request("turn/start", new JsonObject
        {
            ["threadId"] = onThread,
            ["input"] = new JsonArray(new JsonObject { ["type"] = "text", ["text"] = message }),
        });

        var current = thread ?? threadId ?? await StartThread();
        if (current.Length == 0) throw new InvalidOperationException("Codex App Server did not return a thread ID");
        threadId = current;
        JsonNode? response;
        try
        {
            response = await StartTurn(current);
        }
        catch (InvalidOperationException e) when (e.Message.Contains("thread not found", StringComparison.OrdinalIgnoreCase))
        {
            threadId = null;
            current = await StartThread();
            if (current.Length == 0) throw new InvalidOperationException("Codex App Server did not return a replacement thread ID");
            threadId = current;
            response = await StartTurn(current);
        }
        var turnId = Json.Str(response?["turn"]?["id"]);
        if (turnId.Length == 0) throw new InvalidOperationException("Codex App Server did not return a turn ID");

        var waiting = new TaskCompletionSource<ChatResult>(TaskCreationOptions.RunContinuationsAsynchronously);
        lock (gate)
        {
            if (!turns.TryGetValue(turnId, out var turn)) turns[turnId] = turn = new Turn();
            (turn.ThreadId, turn.Waiting) = (current, waiting);
            if (turn.Status is not null) Settle(turnId, turn);
        }
        var patience = TimeSpan.FromMilliseconds(Env.Number("CODEX_CHAT_TIMEOUT_MS", 120_000));
        if (await Task.WhenAny(waiting.Task, Task.Delay(patience)) != waiting.Task)
        {
            lock (gate) turns.Remove(turnId);
            throw new TimeoutException("Codex turn timed out");
        }
        return await waiting.Task;
    }

    public void Dispose()
    {
        lock (gate)
        {
            try { process?.Kill(entireProcessTree: true); }
            catch (InvalidOperationException) { }
            (process, initialized) = (null, false);
        }
    }

    /// The first text found under the usual keys, for a turn that ended
    /// without streaming any deltas.
    private static string ExtractText(JsonNode? value)
    {
        switch (value)
        {
            case null: return "";
            case JsonArray list: return string.Concat(list.Select(ExtractText));
            case JsonObject map:
                foreach (var key in (string[])["text", "content", "message", "delta"])
                    if (map.ContainsKey(key) && ExtractText(map[key]) is { Length: > 0 } text) return text;
                return "";
            default: return value.GetValueKind() == JsonValueKind.String ? value.GetValue<string>() : "";
        }
    }

    /// Every number in the events whose key looks like token or cost usage.
    private static JsonObject? UsageFrom(IEnumerable<JsonObject> events)
    {
        var usage = new JsonObject();
        void Visit(JsonNode? value)
        {
            if (value is JsonArray list) { foreach (var item in list) Visit(item); return; }
            if (value is not JsonObject map) return;
            foreach (var (key, raw) in map["usage"] as JsonObject ?? map)
                if (raw?.GetValueKind() == JsonValueKind.Number && UsageKey().IsMatch(key)) usage[key] = raw.DeepClone();
            foreach (var (_, nested) in map) Visit(nested);
        }
        foreach (var e in events) Visit(e);
        return usage.Count > 0 ? usage : null;
    }
}
