using Vinci;

namespace Vinci.Tests;

/// Drives vinci the way a person does: one command at a time against a real
/// journal file, with a clock the test moves by hand.
public sealed class VinciTests : IDisposable
{
    private readonly string dir = Directory.CreateTempSubdirectory("vinci-test").FullName;
    private DateTimeOffset now = new(2026, 1, 1, 9, 0, 0, TimeSpan.Zero);
    private string stdin = "";
    /// What the window watcher has seen, how far its record reaches, and whether it answers.
    private readonly List<Activity> windows = [];
    private DateTimeOffset? known;
    private bool reachable = true;

    /// What git holds, oldest first.
    private readonly List<Commit> commits = [];

    private void Committed(string subject, params string[] files) =>
        commits.Add(new Commit($"hash{commits.Count}", now, subject, files));

    /// A folder inside a pretend repository.
    private string Folder(string name)
    {
        Directory.CreateDirectory(Path.Combine(dir, "repo", ".git"));
        return Directory.CreateDirectory(Path.Combine(dir, "repo", name)).FullName;
    }

    private void Window(DateTimeOffset start, double minutes, string window) =>
        windows.Add(new Activity(start, TimeSpan.FromMinutes(minutes), window));

    public void Dispose() => Directory.Delete(dir, recursive: true);

    private string JournalPath => Path.Combine(dir, "journal.jsonl");

    private (int Code, string Out) Try(params string[] args)
    {
        var output = new StringWriter();
        var code = App.Run(args, new Journal(JournalPath), new StringReader(stdin), output, output, now,
            (from, to) => reachable ? new Seen(windows, known ?? to) : null,
            (folder, after) => after is { } a
                ? [.. commits.Where(c => c.At > a && c.Files.Any(f => f.StartsWith(folder + "/")))]
                : [.. commits.Where(c => c.Files.Any(f => f.StartsWith(folder + "/"))).TakeLast(1)]);
        return (code, output.ToString());
    }

    private string Run(params string[] args)
    {
        var (code, output) = Try(args);
        Assert.True(code == 0, $"vinci {string.Join(' ', args)} failed: {output}");
        return output;
    }

    private string Fail(params string[] args)
    {
        var (code, output) = Try(args);
        Assert.Equal(1, code);
        return output;
    }

    private void Wait(TimeSpan t) => now += t;

    private void Seed(string id, string title)
    {
        Run("idea", title);
        Run("for", id, "Leonardo");
        Run("goal", id, "Carry one person 100 m", $"distance-{id}", "at-least", "100");
    }

    /// An idea taken through Prepare, with everything the gate out of it asks for.
    private void Prepare(string id, string title)
    {
        Seed(id, title);
        Run("advance", id);
        Run("note", "Birds gain lift from the shape of the wing", id);
        Run("req", id, "Lifts 80 kg", "Leonardo", "A person has to be on it");
        Run("predict", "A 10 m wing lifts 80 kg", "70", id);
    }

    /// An idea taken as far as Build, by the shortest honest route.
    private void GrowToBuild(string id = "i1", string title = "Flying machine")
    {
        Prepare(id, title);
        Run("advance", id, "2");
        Wait(TimeSpan.FromDays(2));
        Run("advance", id);
    }

    // ---- the gates ----

    [Fact]
    public void An_idea_cannot_skip_a_gate()
    {
        Run("idea", "Flying machine");
        Assert.Contains("for nobody", Fail("advance", "i1"));
        Assert.Contains("ships from Build or Tend", Fail("ship", "i1", "a sketch"));

        Run("for", "i1", "Leonardo");
        Assert.Contains("no goal", Fail("advance", "i1"));

        Run("goal", "i1", "Carry one person 100 m", "distance", "at-least", "100");
        Run("advance", "i1");
        Assert.Contains("no notes", Fail("advance", "i1"));

        Run("note", "Birds gain lift from the shape of the wing", "i1");
        Assert.Contains("no requirements", Fail("advance", "i1"));

        Run("req", "i1", "Lifts 80 kg", "Leonardo", "A person has to be on it");
        Assert.Contains("no prediction", Fail("advance", "i1"));

        Run("predict", "A 10 m wing lifts 80 kg", "70", "i1");
        Run("advance", "i1", "2");
        Wait(TimeSpan.FromDays(1));
        Assert.Contains("Leave it alone", Fail("advance", "i1"));

        Wait(TimeSpan.FromDays(1));
        Assert.Contains("now in Build", Run("advance", "i1"));
    }

    [Fact]
    public void A_goal_must_be_tied_to_a_number()
    {
        Run("idea", "Flying machine");
        Assert.Contains("needs a number", Fail("goal", "i1", "Fly well"));
        Assert.Contains("not at-least, at-most or between", Fail("goal", "i1", "Fly well", "distance", "around", "100"));
        Assert.Contains("low must not be above high", Fail("goal", "i1", "Fly well", "distance", "between", "9", "2"));
    }

    [Fact]
    public void A_cut_requirement_does_not_count_as_a_requirement()
    {
        Seed("i1", "Flying machine");
        Run("advance", "i1");
        Run("note", "Birds gain lift from the shape of the wing", "i1");
        Run("req", "i1", "Flaps like a bird", "Leonardo", "Birds do");
        Assert.Contains("deleted 1 of 1", Run("cut", "r1", "Fixed wings glide; flapping is not the goal"));

        Assert.Contains("no requirements", Fail("advance", "i1"));
        Assert.Contains("CUT Flaps like a bird", Run("show", "i1"));
    }

    [Fact]
    public void Only_three_ideas_may_be_in_Build_at_once()
    {
        foreach (var n in new[] { 1, 2, 3 }) GrowToBuild($"i{n}", $"Machine {n}");
        Prepare("i4", "Machine 4");
        Run("advance", "i4", "0");

        Assert.Contains("3 ideas are already in Build (i1, i2, i3)", Fail("advance", "i4"));

        Run("ship", "i2", "First model");
        Assert.Contains("now in Build", Run("advance", "i4"));
    }

    // ---- the goal ----

    [Fact]
    public void Shipping_grows_the_idea_but_only_a_measurement_meets_the_goal()
    {
        GrowToBuild();
        Wait(TimeSpan.FromDays(1));

        var first = Run("ship", "i1", "Glider model");
        Assert.Contains("Cycle time 3d0h since inception", first);
        Assert.Contains("Goal: distance-i1 not measured yet, needs ≥ 100", first);
        Assert.Contains("Predictions to settle: h1", first);
        Assert.Contains("now in Tend", first);

        Assert.Contains("i1 goal not met: needs ≥ 100", Run("log", "distance-i1", "60"));
        Assert.Contains("not met, distance-i1 60 needs ≥ 100", Run("show", "i1"));
        Assert.Contains("i1 has not met its goal", Run("commit", "Fly further", "i1") + Run("next"));

        Wait(TimeSpan.FromHours(5));
        Assert.Contains("Cycle time 5h0m since the last ship", Run("ship", "i1", "Bigger wing"));
        Assert.Contains("i1 goal met", Run("log", "distance-i1", "120"));
        Assert.Contains("met, distance-i1 120 is ≥ 100. Raise it?", Run("show", "i1"));
        Assert.Contains("tended, not finished", Fail("advance", "i1"));
    }

    // ---- time ----

    [Fact]
    public void Time_in_a_watched_window_is_counted_without_starting_or_stopping_anything()
    {
        Run("idea", "Flying machine");
        Run("idea", "Bridge");
        Assert.Contains("Nothing is watched", Run("sync"));
        Run("watch", "i1", "wing");
        Run("watch", "i2", "- bridge -");
        Assert.Contains("already watched for i1", Fail("watch", "i2", "WING"));

        Window(now, 90, "code Wing.cs - glider - Visual Studio Code");
        Window(now.AddMinutes(90), 20, "code arch.md - bridge - Visual Studio Code");
        Window(now.AddMinutes(110), 30, "brave-browser YouTube");
        Wait(TimeSpan.FromMinutes(140));
        Run("spent", "i2", "10");

        var ideas = Run("ideas").Split('\n');
        Assert.Contains("touch 1h30m", ideas[0]);
        Assert.Contains("touch 30m", ideas[1]);
        // Asking again adds nothing: each stretch is counted once.
        Assert.Contains("No watched window since", Run("sync"));
        Assert.Contains("touch 1h30m", Run("ideas"));
        Assert.Contains("watch   \"wing\"", Run("show", "i1"));
    }

    [Fact]
    public void Only_time_inside_the_known_record_is_counted_and_the_rest_waits()
    {
        Run("idea", "Flying machine");
        Wait(TimeSpan.FromHours(1));
        Run("watch", "i1", "wing");
        // Before the watch existed, and past where the record reaches: neither counts yet.
        Window(now.AddMinutes(-30), 40, "code Wing.cs");
        Window(now.AddMinutes(20), 40, "code Wing.cs");
        Wait(TimeSpan.FromMinutes(60));
        known = now.AddMinutes(-10);

        Assert.StartsWith("i1   +40m", Run("sync"));
        known = null;
        Assert.StartsWith("i1   +10m", Run("sync"));
        Assert.Contains("touch 50m", Run("ideas"));
    }

    [Fact]
    public void A_missing_activity_record_loses_nothing()
    {
        Run("idea", "Flying machine");
        Run("watch", "i1", "wing");
        Window(now, 30, "code Wing.cs");
        Wait(TimeSpan.FromHours(1));

        reachable = false;
        Assert.Contains("did not answer", Run("sync"));
        Assert.Contains("touch 0m", Run("ideas"));
        reachable = true;
        Assert.Contains("touch 30m", Run("ideas"));
    }

    [Fact]
    public void A_watch_can_be_dropped_and_a_retired_idea_is_no_longer_credited()
    {
        Run("idea", "Flying machine");
        Run("idea", "Bridge");
        Run("watch", "i1", "wing");
        Run("watch", "i2", "bridge");
        Assert.Contains("i1   work \"wing\"  Flying machine", Run("watches"));
        Run("unwatch", "i1", "wing");
        Assert.Contains("does not watch", Fail("unwatch", "i1", "wing"));
        Run("retire", "i2", "The river moved");

        Window(now, 30, "code Wing.cs - bridge");
        Wait(TimeSpan.FromHours(1));
        var ideas = Run("ideas").Split('\n');
        Assert.Contains("touch 0m", ideas[0]);
        Assert.Contains("touch 0m", ideas[1]);
    }

    [Fact]
    public void A_home_folder_counts_its_editor_windows_and_its_commits_as_work()
    {
        var glider = Folder("glider");
        Assert.StartsWith("i1 adopted", Run("adopt", glider));
        Assert.Contains("i1 already lives", Run("adopt", glider + "/"));
        Run("idea", "Bridge");
        Assert.Contains("is not a folder", Fail("home", "i2", Path.Combine(dir, "nowhere")));
        Assert.Contains("i1 already lives", Fail("home", "i2", glider));
        Run("home", "i2", Folder("bridge"));
        Assert.EndsWith(" glider", Run("ideas").Split('\n')[0]);

        Window(now, 30, "code Wing.cs - glider - Visual Studio Code");
        Window(now.AddMinutes(30), 30, "code README.md - glider-notes - Visual Studio Code");
        Wait(TimeSpan.FromHours(1));
        Assert.Contains("touch 30m", Run("ideas").Split('\n')[0]);

        Committed("Shape the wing", glider + "/Wing.cs");
        Wait(TimeSpan.FromDays(20));
        Committed("Brace the wing", glider + "/Wing.cs", glider + "/Strut.cs");
        Committed("Survey the river", Path.Combine(dir, "repo", "bridge", "notes.md"));
        Committed("Elsewhere", Path.Combine(dir, "repo", "other", "x.md"));

        var synced = Run("sync");
        // A newly homed idea takes its latest commit only; after that, every new one.
        Assert.Contains("i1   1 commit  glider", synced);
        Assert.Contains("i2   1 commit  Bridge", synced);
        Wait(TimeSpan.FromDays(1));
        Committed("Cover the wing", glider + "/Wing.cs");
        Committed("Cover it again", glider + "/Wing.cs");
        Assert.Contains("i1   2 commits  glider", Run("sync"));
        Assert.DoesNotContain("commit", Run("sync"));
        Assert.Contains("(3 commits, last 2026-01-22)", Run("show", "i1"));
        // The commits are why the idea is not stale.
        Assert.DoesNotContain("i1     untouched", Run("review"));
    }

    [Fact]
    public void The_most_specific_watch_wins_a_window()
    {
        Run("idea", "Flying machine");
        Run("idea", "Wing");
        Run("watch", "i1", "glider");
        Run("watch", "i2", "glider/wing");
        Window(now, 30, "code ~/glider/wing/Rib.cs");
        Wait(TimeSpan.FromHours(1));

        var ideas = Run("ideas").Split('\n');
        Assert.Contains("touch 0m", ideas[0]);
        Assert.Contains("touch 30m", ideas[1]);
    }

    [Fact]
    public void Having_the_shipped_thing_on_screen_counts_as_using_it_once_a_day()
    {
        GrowToBuild();
        Assert.Contains("use\" or nothing", Fail("watch", "i1", "Glider Simulator", "used"));
        Run("watch", "i1", "Glider Simulator", "use");
        Assert.Contains("i1   use  \"Glider Simulator\"", Run("watches"));

        // Not shipped yet, so there is nothing to use.
        Window(now, 10, "brave Glider Simulator");
        Wait(TimeSpan.FromHours(1));
        Assert.DoesNotContain("used", Run("sync"));

        Run("ship", "i1", "Glider model");
        Window(now, 0.5, "brave Glider Simulator");
        Wait(TimeSpan.FromHours(1));
        Assert.DoesNotContain("used", Run("sync"));

        Window(now, 10, "brave Glider Simulator");
        Wait(TimeSpan.FromHours(1));
        Assert.Contains("i1   used  Flying machine", Run("sync"));
        Window(now, 10, "brave Glider Simulator");
        Wait(TimeSpan.FromHours(1));
        Assert.DoesNotContain("used", Run("sync"));

        Wait(TimeSpan.FromDays(1));
        Window(now, 10, "brave Glider Simulator");
        Wait(TimeSpan.FromHours(1));
        Run("sync");
        Assert.Contains("used    2 times", Run("show", "i1"));
        // Use is not work.
        Assert.Contains("touch 0m", Run("ideas"));
    }

    [Fact]
    public void Time_and_work_are_counted_per_stage()
    {
        Seed("i1", "Flying machine");
        Run("spent", "i1", "30");
        Wait(TimeSpan.FromDays(3));
        Run("advance", "i1");
        Run("spent", "i1", "90");
        Wait(TimeSpan.FromDays(5));

        Assert.Contains("stages  Seed 3d0h (touch 30m), Prepare 5d0h (touch 1h30m)", Run("show", "i1"));
    }

    // ---- the pipeline ----

    [Fact]
    public void The_pipeline_shows_every_stage_the_slowest_one_and_what_to_do_next()
    {
        GrowToBuild();
        Run("watch", "i1", "wing");
        Run("idea", "Bridge");
        Run("commit", "Glue the first wing rib", "i1");
        Wait(TimeSpan.FromDays(4));

        var lines = Run().Split('\n');
        Assert.StartsWith("Seed       i2   Bridge  (4d0h here)", lines[0]);
        Assert.StartsWith("Prepare    -", lines[1]);
        Assert.StartsWith("Incubate   -", lines[2]);
        Assert.StartsWith("Build 1/3  i1   Flying machine  (4d0h here, touch 0m; goal distance-i1 not measured", lines[3]);
        Assert.StartsWith("Tend       -", lines[4]);
        // i1: Seed 0d, Build 4d. i2: Seed 4d. So Build averages 4d, Seed 2d.
        Assert.StartsWith("Slowest stage: Build, 4d0h on average", lines[5]);
        Assert.StartsWith("Next: build i1: Flying machine", lines[6]);
        Assert.Contains("vinci ship i1", lines[7]);
    }

    [Fact]
    public void Next_is_the_worst_finding_when_there_is_one()
    {
        Run("idea", "Flying machine");
        Run("issue", "Workshop flooded", "big");
        Run("note", "An orphan");

        Assert.StartsWith("Next: week: no commitment", Run("next"));
        Run("commit", "Dry the workshop");
        Assert.StartsWith("Next: x1: big issue, no root cause yet", Run("next"));
    }

    [Fact]
    public void With_no_ideas_and_no_command_it_explains_itself()
    {
        Assert.StartsWith("vinci: one journal", Run());
    }

    // ---- knowledge ----

    [Fact]
    public void A_note_keeps_where_it_came_from_down_to_the_page()
    {
        Run("idea", "Flying machine");
        Run("source", "Leonardo da Vinci, Codex on the Flight of Birds, 1505", "https://example.org/codex");
        Run("note", "A bird steers by shifting its centre of gravity", "s1:f.7v", "i1");
        Assert.Contains("your own thought", Run("note", "So a pilot could steer by leaning", "n1"));

        Assert.Contains("is not a note, idea, source or prediction", Fail("note", "Unfounded claim", "s9"));
        Assert.Contains("only a source has a place", Fail("note", "Misplaced", "i1:p.3"));

        var cited = Run("show", "n1");
        Assert.Contains("origin   s1 Leonardo da Vinci, Codex on the Flight of Birds, 1505, f.7v <https://example.org/codex>", cited);
        Assert.Contains("n2", cited); // linked from
        Assert.Contains("origin   your own thought", Run("show", "n2"));

        Assert.Contains("n1", Run("show", "s1"));
        Assert.Contains("s1   Leonardo da Vinci", Run("show", "i1")); // credits
    }

    [Fact]
    public void Tags_are_an_index_into_the_notes()
    {
        Run("note", "Water moves fastest in the middle of a river", "+Water", "+flow");
        Run("note", "Air behaves like thin water", "n1", "+flow");

        var index = Run("tags").Split('\n');
        Assert.StartsWith("+flow", index[0]);
        Assert.EndsWith("2", index[0]);
        Assert.StartsWith("+water", index[1]);

        var found = Run("notes", "+water");
        Assert.Contains("n1", found);
        Assert.DoesNotContain("n2", found);
        Assert.Contains("n2", Run("notes", "thin"));
    }

    [Fact]
    public void Review_finds_notes_that_connect_to_nothing()
    {
        Run("note", "Water moves fastest in the middle of a river");
        Assert.Contains("n1     note connects to nothing", Run("review"));

        Run("note", "So silt settles at the banks", "n1");
        Assert.DoesNotContain("connects to nothing", Run("review"));
    }

    // ---- inquiry ----

    [Fact]
    public void A_prediction_is_made_before_the_test_and_settled_after()
    {
        Run("idea", "Flying machine");
        Assert.Contains("1 to 99", Fail("predict", "It flies", "100"));
        Run("predict", "A 10 m wing lifts 80 kg", "90", "i1");
        Assert.Contains("say right or wrong", Fail("result", "h1", "maybe", "Unclear"));

        Assert.Contains("h1 was wrong", Run("result", "h1", "wrong", "Lifted 35 kg"));
        Assert.Contains("already settled", Fail("result", "h1", "right", "Second try"));
        Run("note", "Lift did not scale with span alone", "h1");

        var shown = Run("show", "h1");
        Assert.Contains("wrong  90%  A 10 m wing lifts 80 kg", shown);
        Assert.Contains("outcome  Lifted 35 kg", shown);
        Assert.Contains("n1", shown);
    }

    [Fact]
    public void Predictions_show_how_your_confidence_compares_with_your_record()
    {
        for (var n = 1; n <= 10; n++)
        {
            Run("predict", $"Claim {n}", "90");
            Run("result", $"h{n}", n <= 6 ? "right" : "wrong", "Tested");
        }
        Assert.Contains("Settled 10: right 60%, average confidence 90%. You are overconfident by 30 points.", Run("predictions"));
    }

    [Fact]
    public void Review_finds_predictions_never_tested()
    {
        Run("predict", "Silk is strong enough", "60");
        Wait(TimeSpan.FromDays(31));
        Assert.Contains("h1     predicted 31d0h ago and never tested", Run("review"));
    }

    // ---- neglect and use ----

    [Fact]
    public void Review_finds_neglected_ideas_but_not_resting_or_retired_ones()
    {
        GrowToBuild();
        Run("idea", "Bridge");
        Run("idea", "Canal lock");
        Run("retire", "i3", "Already solved well by others");
        Wait(TimeSpan.FromDays(15));

        var review = Run("review");
        Assert.Contains("i1     untouched for 15d0h", review);
        Assert.Contains("i2     untouched for 15d0h", review);
        Assert.DoesNotContain("i3", review);

        Run("tend", "i2", "Sketched the abutments");
        Assert.DoesNotContain("i2", Run("review"));
    }

    [Fact]
    public void Review_says_when_incubation_is_over()
    {
        Prepare("i1", "Flying machine");
        Run("advance", "i1", "30");
        Run("commit", "Read about silk", "i1");

        Wait(TimeSpan.FromDays(5));
        Assert.Contains("Nothing to fix", Run("review"));
        Wait(TimeSpan.FromDays(25));
        Assert.Contains("incubation is over", Run("review"));
    }

    [Fact]
    public void Review_finds_what_was_shipped_and_never_used()
    {
        GrowToBuild();
        Assert.Contains("has not shipped", Fail("used", "i1"));
        Run("ship", "i1", "Glider model");
        Wait(TimeSpan.FromDays(15));
        Assert.Contains("i1     shipped 15d0h ago and never used", Run("review"));

        Assert.Contains("i1 used 1 times", Run("used", "i1", "Flew 20 m off the hill"));
        Assert.DoesNotContain("never used", Run("review"));
    }

    // ---- getting better ----

    [Fact]
    public void An_issue_is_diagnosed_before_it_becomes_a_principle()
    {
        Run("issue", "Wing tore on the first test");
        Assert.Contains("no root cause yet", Run("review"));
        Assert.Contains("Diagnose before you design", Fail("principle", "Use stronger cloth", "x1"));

        Run("diagnose", "x1", "Never load-tested the cloth before building");
        Assert.Contains("diagnosed but nothing changed", Run("review"));

        Assert.Contains("x1 is closed", Run("principle", "Test each material alone before building with it", "x1"));
        Assert.Contains("Nothing to fix. Last review: 1.", Run("review"));
        Assert.Contains("(from x1)", Run("principles"));
    }

    [Fact]
    public void Big_issues_come_before_small_ones()
    {
        Run("issue", "Ink smudged");
        Run("issue", "Workshop flooded", "big");
        var review = Run("review").Split('\n');
        Assert.StartsWith("x2     big issue", review[0]);
        Assert.StartsWith("x1 ", review[2]);
    }

    [Fact]
    public void Issues_are_counted_by_the_stage_they_happened_in()
    {
        GrowToBuild();
        Run("issue", "Wing tore", "i1");
        Run("issue", "Glue failed", "i1");
        Run("idea", "Bridge");
        Run("issue", "Vague idea", "i2");

        Assert.Contains("By stage: Build 2, Seed 1. Most go wrong in Build.", Run("issues"));
        Assert.Contains("on i1 in Build", Run("show", "x1"));
    }

    [Fact]
    public void A_principle_comes_back_at_the_stage_where_its_issue_happened()
    {
        GrowToBuild();
        Run("issue", "Wing tore on the first test", "i1");
        Run("diagnose", "x1", "Never load-tested the cloth");
        Run("principle", "Test each material alone first", "x1");
        Run("ship", "i1", "Glider");

        Prepare("i2", "Bridge");
        Run("advance", "i2", "0");
        Assert.Contains("Remember p1: Test each material alone first", Run("advance", "i2"));
    }

    [Fact]
    public void A_principle_that_keeps_being_broken_is_flagged()
    {
        Run("issue", "Skipped the load test");
        Run("diagnose", "x1", "Was in a hurry");
        Run("principle", "Always load-test", "x1");

        Run("issue", "Skipped it again", "p1");
        Assert.DoesNotContain("p1 ", Run("review"));
        Run("issue", "And again", "p1");

        Assert.Contains("p1     broken 2 times, so the rule is not working", Run("review"));
        Assert.Contains("broken 2 times", Run("principles"));
        Assert.Contains("broke       p1 Always load-test", Run("show", "x2"));
    }

    // ---- the week ----

    [Fact]
    public void A_commitment_is_answered_for_a_week_later()
    {
        Run("idea", "Flying machine");
        Assert.Contains("week   no commitment for this week", Run("review"));

        Assert.Contains("Due 2026-01-08", Run("commit", "Build the wing frame", "i1"));
        Assert.Contains("already committed", Fail("commit", "Something else"));
        Assert.Contains("Nothing to fix", Run("review"));

        Wait(TimeSpan.FromDays(7));
        Assert.Contains("week   commitment from 2026-01-01 is due: Build the wing frame", Run("review"));
        Assert.Contains("1 of 1 commitments kept", Run("kept"));
        Assert.Contains("no open commitment", Fail("kept"));
    }

    [Fact]
    public void A_missed_commitment_becomes_an_issue()
    {
        Run("idea", "Flying machine");
        Run("commit", "Build the wing frame", "i1");
        Wait(TimeSpan.FromDays(7));

        Assert.Contains("0 of 1 commitments kept. Logged as x1", Run("missed", "Spent the week on the bridge"));
        Assert.Contains("Missed commitment \"Build the wing frame\": Spent the week on the bridge", Run("show", "x1"));
        Assert.Contains("0 of 1 commitments kept", Run("review"));
    }

    // ---- measurement ----

    [Fact]
    public void Each_review_is_itself_measured()
    {
        Run("note", "An orphan");
        Run("review");
        Run("review");
        Assert.Contains("vinci.findings: 2 measurements", Run("stats", "vinci.findings"));
        Assert.Contains("kept by vinci", Fail("log", "vinci.findings", "0"));
    }

    [Fact]
    public void Measurements_get_control_limits()
    {
        foreach (var hours in new[] { "7", "8", "7", "8", "7", "8", "7" })
        {
            Run("log", "sleep", hours);
            Wait(TimeSpan.FromDays(1));
        }
        Assert.Contains("stable", Run("stats", "sleep"));
        Assert.Contains("Nothing to fix", Run("review"));

        Run("log", "sleep", "2", "Deadline night");
        Assert.Contains("special cause: 2 on 2026-01-08", Run("stats", "sleep"));
        Assert.Contains("sleep  out of control", Run("review"));
    }

    [Fact]
    public void A_sigma_level_is_only_given_once_there_is_enough_data()
    {
        Run("spec", "sleep", "7", "9");
        for (var n = 0; n < 29; n++) Run("log", "sleep", n < 3 ? "5" : "8");
        Assert.Contains("3 of 29 outside, too few to give a sigma level (29 of 30)", Run("stats", "sleep"));

        Run("log", "sleep", "8");
        // 3 of 30 is a 90% yield: 1.28 sigma, plus the 1.5 shift.
        Assert.Contains("3 of 30 outside, sigma level 2.8", Run("stats", "sleep"));
    }

    [Fact]
    public void A_metric_may_be_named_like_an_id()
    {
        Run("idea", "Flying machine");
        Wait(TimeSpan.FromDays(20));
        Run("log", "i1", "3");
        Assert.Contains("i1     untouched for 20d0h", Run("review"));
    }

    [Theory]
    [InlineData(3.4, 6.0)]
    [InlineData(233, 5.0)]
    [InlineData(6210, 4.0)]
    [InlineData(66807, 3.0)]
    [InlineData(308538, 2.0)]
    public void Sigma_levels_match_the_published_table(double defectsPerMillion, double sigma)
    {
        Assert.Equal(sigma, Stats.NormalQuantile(1 - defectsPerMillion / 1e6) + 1.5, 2);
    }

    [Fact]
    public void Control_limits_are_the_mean_plus_or_minus_2_66_average_steps()
    {
        var limits = Stats.ControlLimits([10, 12, 11, 13])!;
        // mean 11.5; steps 2, 1, 2, so the average step is 5/3
        Assert.Equal(11.5, limits.Center, 6);
        Assert.Equal(11.5 - 2.66 * 5 / 3, limits.Lower, 6);
        Assert.Equal(11.5 + 2.66 * 5 / 3, limits.Upper, 6);
        Assert.Null(Stats.ControlLimits([10]));
    }

    // ---- as a tool in a pipe ----

    [Fact]
    public void A_dash_reads_the_text_from_standard_input()
    {
        stdin = "  Water finds\n  its own level \n";
        Run("note", "-");
        Assert.Contains("n1   Water finds its own level", Run("notes"));

        stdin = "";
        Assert.Contains("<text> is empty", Fail("note", "-"));
    }

    [Fact]
    public void Path_prints_the_journal_and_the_journal_is_one_json_object_per_line()
    {
        Run("idea", "Flying machine");
        Assert.Equal(JournalPath, Run("path").Trim());
        var line = Assert.Single(File.ReadAllLines(JournalPath));
        Assert.Contains("\"kind\":\"idea\",\"id\":\"i1\",\"text\":\"Flying machine\"", line);
    }

    [Fact]
    public void Bad_input_is_refused_and_writes_nothing()
    {
        Assert.Contains("unknown command", Fail("fly"));
        Assert.Contains("missing <title>", Fail("idea"));
        Assert.Contains("no idea i7", Fail("for", "i7", "x"));
        Assert.Contains("must be a number", Fail("log", "sleep", "lots"));
        Assert.False(File.Exists(JournalPath));
    }
}
