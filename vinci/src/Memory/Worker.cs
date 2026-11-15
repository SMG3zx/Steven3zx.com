using System.Text.Json;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;

namespace Vinci.Memory;

/// Does the queued work: turns an event into durable knowledge, reflects on
/// a finished session, and re-checks a memory against the others. A job that
/// fails is retried with a growing delay and then left failed.
public sealed partial class ConsolidationWorker(Neo4jStore store, IConsolidator consolidator)
{
    [GeneratedRegex(
        @"\b(error|failed|failure|exception|timeout|timed out|could not|unable to)\b",
        RegexOptions.IgnoreCase
    )]
    private static partial Regex FailureWords();

    [GeneratedRegex(@"\s+")]
    private static partial Regex Spaces();

    /// An event that reports something going wrong becomes an episode memory
    /// that points back at it, so the failure can be recalled next time
    /// (after Reflexion). Null when the event is not a failure.
    public static CuratedMemory? FailureMemoryFromEvent(MemoryEvent e)
    {
        var metadata = e.Metadata ?? [];
        var status = (metadata["status"] ?? metadata["success"]) is { } node
            ? Json.Str(node).ToLowerInvariant()
            : "";
        var failed =
            e.Kind == "error"
            || status is "failed" or "error" or "false"
            || FailureWords().IsMatch(e.Content);
        if (!failed)
            return null;
        return new CuratedMemory
        {
            Content = $"Failure feedback from {e.Kind}: {e.Content}",
            Category = "episode",
            Confidence = e.Kind == "error" ? 0.85 : 0.7,
            Metadata = new JsonObject
            {
                ["memoryKind"] = "reflexion_failure",
                ["eventKind"] = e.Kind,
                ["originalMetadata"] = metadata.DeepClone(),
            },
            SourceEventIds = e.Id is null ? [] : [e.Id],
            UserId = e.UserId,
            ProjectId = e.ProjectId,
            SessionId = e.SessionId,
        };
    }

    /// Runs one job, or the next job for one target. Returns the job with
    /// what was done to it, or null when nothing is due.
    public async Task<JsonObject?> ProcessOnce(string? targetId = null)
    {
        // With no model endpoint, extraction jobs stay queued instead of being
        // leased and retried over and over in a retrieval-only deployment.
        string[] skipKinds =
            Env.Get("MODEL_BASE_URL") is null && consolidator is OpenAICompatibleConsolidator
                ? ["consolidate_event", "reflect_session"]
                : [];
        var job = await store.ClaimNextJob(
            targetId,
            skipKinds,
            Env.Get("CONSOLIDATION_REQUIRE_APPROVAL") == "1"
        );
        if (job is null)
            return null;
        var result = (JsonObject)JsonSerializer.SerializeToNode(job, Json.Options)!;
        try
        {
            switch (job.Kind)
            {
                case "reconsolidate_memory":
                    result["links"] = await store.ReconsolidateMemory(job.TargetId);
                    break;
                case "reflect_session":
                    await Reflect(job, result);
                    break;
                case "consolidate_event":
                    await Consolidate(job, result);
                    break;
                default:
                    result["skipped"] = true;
                    break;
            }
            await store.CompleteJob(job.Id);
            return result;
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            result["error"] = e.Message;
            result["retry"] = await store.RetryJob(job.Id, e.Message);
            return result;
        }
    }

    private async Task Reflect(MemoryJob job, JsonObject result)
    {
        var events = await store.GetSessionEvents(job.TargetId);
        if (events.Count == 0)
        {
            result["skipped"] = true;
            result["reason"] = "session has no events";
            return;
        }
        var reflection = await consolidator.Reflect(events);
        var sourceEventIds = events.Where(e => e.Id is not null).Select(e => e.Id!).ToList();
        var session = events.Find(e => e.SessionId == job.TargetId);
        (string Content, string Category)[] memories =
        [
            (reflection.Reflection, "reflection"),
            .. reflection.Lessons.Select(lesson => (lesson, "belief")),
            .. reflection.Failures.Select(failure => (failure, "failure")),
        ];
        var saved = new JsonArray();
        foreach (var (content, category) in memories)
            saved.Add(
                await store.CreateMemory(
                    new CuratedMemory
                    {
                        Content = content,
                        Category = category,
                        SourceEventIds = sourceEventIds,
                        UserId = session?.UserId,
                        ProjectId = session?.ProjectId,
                        SessionId = job.TargetId,
                        Confidence = category == "failure" ? 0.85 : 0.8,
                    }
                )
            );
        result["reflection"] = JsonSerializer.SerializeToNode(reflection, Json.Options);
        result["memories"] = saved;
    }

    private async Task Consolidate(MemoryJob job, JsonObject result)
    {
        var e = await store.GetEvent(job.TargetId)
            ?? throw new InvalidOperationException($"event {job.TargetId} was not found");
        if (FailureMemoryFromEvent(e) is { } failure)
            await store.CreateMemory(failure);
        // A proposal that a person approved is used as it stands; otherwise the model is asked.
        var extraction = job.Proposal is null
            ? await consolidator.Extract(e)
            : Consolidation.ParseExtraction(JsonNode.Parse(job.Proposal));

        var entityIds = new Dictionary<string, string>();
        foreach (var entity in extraction.Entities)
        {
            var saved = await store.UpsertEntity(
                new Entity
                {
                    Name = entity.Name,
                    Type = entity.Type,
                    Aliases = entity.Aliases,
                    Description = entity.Description,
                    UserId = e.UserId,
                    ProjectId = e.ProjectId,
                    SessionId = e.SessionId,
                }
            );
            entityIds[Normalize(entity.Name)] = Json.Str(saved["id"]);
            foreach (var alias in entity.Aliases)
                entityIds[Normalize(alias)] = Json.Str(saved["id"]);
        }
        foreach (var memory in extraction.Memories)
            await store.CreateMemory(
                new CuratedMemory
                {
                    Content = memory.Content,
                    Category = memory.Category,
                    Confidence = memory.Confidence,
                    ValidFrom = memory.ValidFrom,
                    ValidUntil = memory.ValidUntil,
                    UserId = e.UserId,
                    ProjectId = e.ProjectId,
                    SessionId = e.SessionId,
                    SourceEventIds = [e.Id!],
                    EntityIds =
                    [
                        .. memory
                            .EntityNames.Select(Normalize)
                            .Where(entityIds.ContainsKey)
                            .Select(name => entityIds[name]),
                    ],
                }
            );
        foreach (var claim in extraction.Claims)
        {
            if (!entityIds.TryGetValue(Normalize(claim.SubjectName), out var subject))
                continue;
            await store.CreateClaim(
                new ClaimInput
                {
                    Statement = claim.Statement,
                    Predicate = claim.Predicate,
                    SubjectEntityId = subject,
                    ObjectEntityId =
                        claim.ObjectName is not null
                        && entityIds.TryGetValue(Normalize(claim.ObjectName), out var target)
                            ? target
                            : null,
                    Confidence = claim.Confidence,
                    UserId = e.UserId,
                    ProjectId = e.ProjectId,
                    SessionId = e.SessionId,
                    SourceEventIds = [e.Id!],
                }
            );
        }
        foreach (var procedure in extraction.Procedures)
            await store.UpsertProcedure(
                new ProcedureInput
                {
                    Title = procedure.Title,
                    Goal = procedure.Goal,
                    Steps = procedure.Steps,
                    Preconditions = procedure.Preconditions,
                    KnownFailures = procedure.KnownFailures,
                    Confidence = procedure.Confidence,
                    UserId = e.UserId,
                    ProjectId = e.ProjectId,
                    SessionId = e.SessionId,
                    SourceEventIds = [e.Id!],
                }
            );
        result["extracted"] = JsonSerializer.SerializeToNode(extraction, Json.Options);
    }

    private static string Normalize(string value) =>
        Spaces().Replace(value.Trim().ToLowerInvariant(), " ");

    /// Works through the queue until told to stop, resting when it is empty.
    public async Task Run(CancellationToken stop, TextWriter log)
    {
        await store.EnsureSchema();
        var pollMs = (int)Env.Number("WORKER_POLL_MS", 1000);
        log.WriteLine("Memory consolidation worker started");
        while (!stop.IsCancellationRequested)
        {
            var result = await ProcessOnce();
            if (result is not null)
                log.WriteLine(result.ToJsonString(Json.Options));
            else
                try
                {
                    await Task.Delay(pollMs, stop);
                }
                catch (OperationCanceledException)
                {
                    break;
                }
        }
        log.WriteLine("Memory consolidation worker stopped");
    }
}
