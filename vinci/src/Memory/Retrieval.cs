using System.Globalization;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;

namespace Vinci.Memory;

public sealed record RetrievalPlan(
    string Complexity,
    int CandidateMultiplier,
    int GraphLimit,
    int RerankLimit
);

/// Null Reason means there is enough evidence to answer.
public sealed record Abstention(bool Abstain, string? Reason = null);

public sealed record EvidenceAssessment(
    int ResultCount,
    int ScoredResultCount,
    double MaxScore,
    bool NeedsCorrection
);

/// Decides how a question should be looked up and whether what came back is
/// enough to answer from.
public static partial class Retrieval
{
    [GeneratedRegex(@"\b(and|both|between|depend|affect|why|compare|related)\b")]
    private static partial Regex MultiHopWords();

    [GeneratedRegex(
        @"\b(when|current|currently|before|after|last|previous|recent|latest|used to)\b"
    )]
    private static partial Regex TemporalWords();

    [GeneratedRegex(
        @"\b(how do i|how did we|steps|procedure|runbook|recover|deploy|configure|fix)\b"
    )]
    private static partial Regex ProceduralWords();

    [GeneratedRegex(
        @"\b(who|which|what depends|related|connected|relationship|linked|associated|affect)\b"
    )]
    private static partial Regex GraphWords();

    [GeneratedRegex("[a-z0-9][a-z0-9_-]{2,}")]
    private static partial Regex Term();

    [GeneratedRegex("[0-9]|[-_]")]
    private static partial Regex Distinctive();

    private static readonly HashSet<string> StopWords =
    [
        .. "a an and are about after again all any at be been before between by can did do does during for from had has have how i if in into is it its me my no not of on or our out over so some than that the their then there these this those to under up used we what when where which while who why with your also".Split(
            ' '
        ),
    ];

    /// "semantic", "graph", "temporal" or "procedural".
    public static string RouteQuery(string query)
    {
        var normalized = query.ToLowerInvariant();
        if (TemporalWords().IsMatch(normalized))
            return "temporal";
        if (ProceduralWords().IsMatch(normalized))
            return "procedural";
        if (GraphWords().IsMatch(normalized))
            return "graph";
        return "semantic";
    }

    /// "simple", "temporal", "procedural" or "multi-hop".
    public static string ClassifyQueryComplexity(string query, string? route = null)
    {
        route ??= RouteQuery(query);
        if (route == "temporal")
            return "temporal";
        if (route == "procedural")
            return "procedural";
        if (route == "graph" || MultiHopWords().IsMatch(query.ToLowerInvariant()))
            return "multi-hop";
        return "simple";
    }

    /// A harder question gets a wider net.
    public static RetrievalPlan Plan(string query, string? route = null)
    {
        var complexity = ClassifyQueryComplexity(query, route ?? RouteQuery(query));
        return new RetrievalPlan(
            complexity,
            CandidateMultiplier: complexity == "simple" ? 2
                : complexity == "multi-hop" ? 6
                : 4,
            GraphLimit: complexity == "simple" ? 4
                : complexity == "multi-hop" ? 20
                : 10,
            RerankLimit: complexity == "simple" ? 8
                : complexity == "multi-hop" ? 24
                : 16
        );
    }

    /// MemoryBank-inspired retention signal used only for ranking, never deletion.
    public static double RetentionScore(
        JsonNode? occurredAt,
        JsonNode? confidence,
        JsonNode? category,
        JsonNode? reinforcementCount,
        DateTimeOffset? now = null
    )
    {
        var ageDays =
            occurredAt is not null
            && DateTimeOffset.TryParse(
                Json.Str(occurredAt),
                CultureInfo.InvariantCulture,
                DateTimeStyles.AssumeUniversal,
                out var at
            )
                ? Math.Max(0, ((now ?? DateTimeOffset.UtcNow) - at).TotalDays)
                : 0;
        var confident = Math.Min(1, Math.Max(0, OrZero(Json.Num(confidence, 1))));
        var reinforced = Math.Max(0, OrZero(Json.Num(reinforcementCount)));
        var halfLifeDays = Json.Str(category) switch
        {
            "working" => 3,
            "episode" => 14,
            "procedure" => 180,
            _ => 90,
        };
        var reinforcement = 1 + Math.Min(1.5, Math.Log(1 + reinforced) * 0.35);
        return confident * Math.Pow(0.5, ageDays / (halfLifeDays * reinforcement));
    }

    /// The lowest cosine similarity at which a vector hit counts as evidence. Calibrated on the
    /// paraphrase suite: unanswerable questions top out at 0.41, and answerable ones start at 0.40.
    public const double VectorEvidenceCosine = 0.41;

    public static Abstention ShouldAbstain(IReadOnlyList<JsonObject> results, string? query = null)
    {
        if (results.Count == 0)
            return new(true, "No relevant evidence was retrieved.");
        var scored = results.Select(r => Json.Num(r["score"])).Where(double.IsFinite).ToList();
        const string Weak = "Retrieved evidence did not meet the confidence threshold.";
        if (string.IsNullOrEmpty(query))
            return scored.Count > 0 && scored.Max() < 0.18 ? new(true, Weak) : new(false);

        var meaningful = Terms(query);
        bool Mentions(JsonObject result, string term) =>
            Json.Str(result["content"]).ToLowerInvariant().Contains(term, StringComparison.Ordinal);
        // A vector hit is evidence only when it is close enough in meaning. Nearest neighbours always
        // exist, so counting every one of them would make the store never abstain.
        bool Close(JsonObject result) =>
            result["vectorCosine"] is { }
            && Json.Num(result["vectorCosine"]) >= VectorEvidenceCosine;
        // One shared word ("library", "Tuesday") is not evidence for a question with several content
        // words: the results together must cover at least half of them.
        var covered = meaningful.Count(term => results.Any(r => Mentions(r, term)));
        var hasLexicalEvidence =
            covered * 2 >= meaningful.Count && covered > 0 || results.Any(Close);
        var distinctive = meaningful
            .Where(term => Distinctive().IsMatch(term) || term.Length >= 14)
            .ToList();
        if (!distinctive.All(term => results.Any(r => Mentions(r, term))))
            return new(true, "No retrieved evidence contains the query's distinctive identifiers.");
        if (meaningful.Count > 0 && !hasLexicalEvidence)
            return new(true, "Retrieved evidence did not contain query-linked terms.");
        // Reciprocal-rank fusion produces small scores that do not depend on the
        // method, so term-linked evidence counts for more than a score threshold.
        if (scored.Count > 0 && scored.Max() < 0.18 && !hasLexicalEvidence)
            return new(true, Weak);
        return new(false);
    }

    /// The question reduced to its meaningful words, for a second attempt.
    /// Null when that would be the same question.
    public static string? CorrectiveQuery(string query)
    {
        var fallback = string.Join(' ', Terms(query).Distinct().Take(10));
        return fallback.Length > 0 && fallback != query.Trim().ToLowerInvariant() ? fallback : null;
    }

    public static EvidenceAssessment AssessEvidence(IReadOnlyList<JsonObject> results)
    {
        var scored = results.Select(r => Json.Num(r["score"])).Where(double.IsFinite).ToList();
        var maxScore = scored.Count > 0 ? scored.Max() : 0;
        return new(results.Count, scored.Count, maxScore, results.Count == 0 || maxScore < 0.25);
    }

    /// How a candidate ranks for a route: its fused score, a boost for the
    /// kind of evidence the route wants, then retention and proven usefulness.
    public static double RouteScore(
        JsonObject item,
        string route,
        DateTimeOffset? now = null,
        double scale = 1
    )
    {
        var type = Json.Str(item["type"]);
        var method = Json.Str(item["retrievalMethod"]);
        var boost =
            route == "procedural" && type == "procedure" ? 0.35
            : route == "graph" && (type == "claim" || method == "graph") ? 0.25
            : route == "temporal" && type is "event" or "memory" ? 0.1
            : route == "semantic" && (method == "vector" || type is "memory" or "chunk") ? 0.1
            : 0;
        var retention = RetentionScore(
            item["occurredAt"],
            item["confidence"],
            item["kind"],
            item["reinforcementCount"],
            now
        );
        var utility = Math.Min(1, Math.Max(0, OrZero(Json.Num(item["utilityScore"]))));
        return Json.Num(item["score"]) / scale
            + boost
            + retention * (route == "temporal" ? 0.08 : 0.025)
            + utility * 0.08;
    }

    private static List<string> Terms(string query) =>
        [
            .. Term()
                .Matches(query.ToLowerInvariant())
                .Select(m => m.Value)
                .Where(term => !StopWords.Contains(term)),
        ];

    /// The words a keyword search should look for. Full-text search matches any word, so the
    /// question's function words ("did", "we", "over") would match most of the store. Falls back to
    /// the whole question when nothing but function words is left.
    public static string LexicalQuery(string query) =>
        Terms(query) is { Count: > 0 } terms ? string.Join(' ', terms) : query;

    private static double OrZero(double value) => double.IsNaN(value) ? 0 : value;
}

/// Keeps lexical, vector and graph scores apart: raw index scores are not
/// comparable, so each list votes by rank instead.
public static class RankFusion
{
    public sealed record Weights(double Lexical, double Vector, double Graph)
    {
        public double Of(string method) =>
            method switch
            {
                "lexical" => Lexical,
                "vector" => Vector,
                _ => Graph,
            };

        public static Weights For(string route) =>
            route switch
            {
                "graph" => new(0.25, 0.2, 0.55),
                "temporal" => new(0.45, 0.2, 0.35),
                "procedural" => new(0.5, 0.2, 0.3),
                _ => Semantic,
            };

        /// Lexical, vector and graph weights for questions that are not about time, procedure or
        /// relations. Set FUSION_SEMANTIC to "lexical,vector,graph" to try another mix.
        public static Weights Semantic
        {
            get
            {
                var parts = (Env.Get("FUSION_SEMANTIC") ?? "0.4,0.4,0.2").Split(',');
                return
                    parts.Length == 3
                    && parts.All(p =>
                        double.TryParse(
                            p,
                            System.Globalization.NumberStyles.Float,
                            System.Globalization.CultureInfo.InvariantCulture,
                            out _
                        )
                    )
                    ? new(
                        double.Parse(parts[0], System.Globalization.CultureInfo.InvariantCulture),
                        double.Parse(parts[1], System.Globalization.CultureInfo.InvariantCulture),
                        double.Parse(parts[2], System.Globalization.CultureInfo.InvariantCulture)
                    )
                    : new(0.4, 0.4, 0.2);
            }
        }
    }

    public static List<JsonObject> Fuse(
        IReadOnlyDictionary<string, IReadOnlyList<JsonObject>> sources,
        Weights weights,
        int k = 60
    )
    {
        var combined = new Dictionary<string, JsonObject>();
        var order = new List<string>();
        foreach (var method in Vocabulary.RetrievalMethods)
        {
            if (!sources.TryGetValue(method, out var candidates))
                continue;
            var ranked = candidates.OrderByDescending(c => Json.Num(c["score"])).ToList();
            for (var index = 0; index < ranked.Count; index++)
            {
                var item = ranked[index];
                var key = $"{Json.Str(item["type"])}:{Json.Str(item["id"])}";
                var contribution = weights.Of(method) / (k + index + 1);
                if (!combined.TryGetValue(key, out var existing))
                {
                    var first = Json.Clone(item);
                    first["score"] = contribution;
                    first["fusionScore"] = contribution;
                    first["retrievalMethods"] = new JsonArray(method);
                    first["retrievalMethod"] = method;
                    combined[key] = first;
                    order.Add(key);
                    continue;
                }
                var methods = Json.Strings(existing["retrievalMethods"]);
                if (!methods.Contains(method))
                    methods.Add(method);
                var fusionScore =
                    Json.Num(existing["fusionScore"] ?? existing["score"]) + contribution;
                existing["retrievalMethods"] = new JsonArray([.. methods.Select(m => (JsonNode)m)]);
                existing["fusionScore"] = fusionScore;
                existing["score"] = fusionScore;
                existing["retrievalMethod"] = methods.Count > 1 ? "hybrid" : method;
            }
        }
        return [.. order.Select(key => combined[key]).OrderByDescending(c => Json.Num(c["score"]))];
    }
}
