using System.Text.Json;
using System.Text.Json.Nodes;

namespace Harness;

/// <summary>
/// Turns an Exercism track checkout into harness tasks: the stub and the tests go in the workspace, the
/// instructions become INSTRUCTIONS.md, and the score is whether the tests pass. The reference solution
/// in .meta/ is never copied.
/// </summary>
public static class Exercism
{
    public static int Import(string repoDir, string outDir, string testCommandTemplate)
    {
        var practice = Path.Combine(repoDir, "exercises", "practice");
        var count = 0;
        foreach (var dir in Directory.EnumerateDirectories(practice).Order())
        {
            var slug = Path.GetFileName(dir);
            var configPath = Path.Combine(dir, ".meta", "config.json");
            var instructions = Path.Combine(dir, ".docs", "instructions.md");
            if (!File.Exists(configPath) || !File.Exists(instructions))
                continue;
            var files = JsonNode.Parse(File.ReadAllText(configPath))!["files"]!;
            var solution = files["solution"]!.AsArray().Select(n => n!.GetValue<string>()).ToList();
            var tests = files["test"]!.AsArray().Select(n => n!.GetValue<string>()).ToList();
            if (solution.Count == 0 || tests.Count != 1)
                continue;

            var fixture = Path.Combine(outDir, "fixtures", slug);
            if (Directory.Exists(fixture))
                Directory.Delete(fixture, true);
            Directory.CreateDirectory(fixture);
            foreach (var f in Directory.EnumerateFiles(dir, "*", SearchOption.AllDirectories))
            {
                var rel = Path.GetRelativePath(dir, f);
                if (rel.StartsWith('.') || rel.Contains("/."))
                    continue;
                var target = Path.Combine(fixture, rel);
                Directory.CreateDirectory(Path.GetDirectoryName(target)!);
                File.Copy(f, target);
            }
            var text = File.ReadAllText(instructions);
            var append = Path.Combine(dir, ".docs", "instructions.append.md");
            if (File.Exists(append))
                text += "\n\n" + File.ReadAllText(append);
            File.WriteAllText(Path.Combine(fixture, "INSTRUCTIONS.md"), text);

            var testFile = tests[0];
            var task = new JsonObject
            {
                ["id"] = slug,
                ["prompt"] =
                    $"Implement the exercise described in INSTRUCTIONS.md by editing {string.Join(", ", solution)}. "
                    + $"The tests are in {testFile}; do not edit them. Call run_tests to check your work, "
                    + "and call finish once all tests pass.",
                ["fixture"] = $"../fixtures/{slug}",
                ["checks"] = new JsonArray(),
                ["testCommand"] = testCommandTemplate.Replace(
                    "{test}",
                    Path.GetFileNameWithoutExtension(testFile)
                ),
            };
            Directory.CreateDirectory(Path.Combine(outDir, "tasks"));
            File.WriteAllText(
                Path.Combine(outDir, "tasks", slug + ".json"),
                task.ToJsonString(new JsonSerializerOptions { WriteIndented = true })
            );
            count++;
        }
        return count;
    }
}
