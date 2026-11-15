using Vinci;

namespace Vinci.Tests;

/// Drives vinci the way a person does: one command at a time against a real
/// journal file, with a clock the test moves by hand.
public sealed class VinciTests : IDisposable
{
    private readonly string dir = Directory.CreateTempSubdirectory("vinci-test").FullName;
    private DateTimeOffset now = new(2026, 1, 1, 9, 0, 0, TimeSpan.Zero);

    public void Dispose() => Directory.Delete(dir, recursive: true);

    private (int Code, string Out) Try(params string[] args)
    {
        var output = new StringWriter();
        var code = App.Run(args, new Journal(Path.Combine(dir, "journal.jsonl")), output, output, now);
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

    /// An idea taken as far as Build, by the shortest honest route.
    private void GrowToBuild()
    {
        Run("idea", "Flying machine");
        Run("goal", "i1", "Carry one person 100 m");
        Run("advance", "i1");
        Run("note", "Birds gain lift from the shape of the wing", "i1");
        Run("req", "i1", "Lifts 80 kg", "Leonardo", "A person has to be on it");
        Run("advance", "i1", "2");
        Wait(TimeSpan.FromDays(2));
        Run("advance", "i1");
    }

    [Fact]
    public void An_idea_cannot_skip_a_gate()
    {
        Run("idea", "Flying machine");
        Assert.Contains("no goal", Fail("advance", "i1"));
        Assert.Contains("ships from Build or Tend", Fail("ship", "i1", "a sketch"));

        Run("goal", "i1", "Carry one person 100 m");
        Run("advance", "i1");
        Assert.Contains("no notes", Fail("advance", "i1"));

        Run("note", "Birds gain lift from the shape of the wing", "i1");
        Assert.Contains("no requirements", Fail("advance", "i1"));

        Run("req", "i1", "Lifts 80 kg", "Leonardo", "A person has to be on it");
        Run("advance", "i1", "2");
        Wait(TimeSpan.FromDays(1));
        Assert.Contains("Leave it alone", Fail("advance", "i1"));

        Wait(TimeSpan.FromDays(1));
        Assert.Contains("now in Build", Run("advance", "i1"));
    }

    [Fact]
    public void A_cut_requirement_does_not_count_as_a_requirement()
    {
        Run("idea", "Flying machine");
        Run("goal", "i1", "Carry one person 100 m");
        Run("advance", "i1");
        Run("note", "Birds gain lift from the shape of the wing", "i1");
        Run("req", "i1", "Flaps like a bird", "Leonardo", "Birds do");
        Assert.Contains("deleted 1 of 1", Run("cut", "r1", "Fixed wings glide; flapping is not the goal"));

        Assert.Contains("no requirements", Fail("advance", "i1"));
        Assert.Contains("CUT Flaps like a bird", Run("show", "i1"));
    }

    [Fact]
    public void Shipping_grows_the_idea_and_it_can_ship_again()
    {
        GrowToBuild();
        Wait(TimeSpan.FromDays(1));

        var first = Run("ship", "i1", "Glider model");
        Assert.Contains("Cycle time 3d0h since inception", first);
        Assert.Contains("now in Tend", first);

        Wait(TimeSpan.FromHours(5));
        Assert.Contains("Cycle time 5h0m since the last ship", Run("ship", "i1", "Bigger wing"));
        Assert.Contains("tended, not finished", Fail("advance", "i1"));
    }

    [Fact]
    public void Time_is_tracked_per_idea()
    {
        Run("idea", "Flying machine");
        Run("idea", "Bridge");

        Run("start", "i1");
        Wait(TimeSpan.FromMinutes(90));
        Assert.Contains("Stopped i1 after 1h30m", Run("start", "i2"));
        Wait(TimeSpan.FromMinutes(20));
        Run("stop");
        Run("spent", "i2", "10");
        Assert.Contains("no timer", Fail("stop"));

        var ideas = Run("ideas").Split('\n');
        Assert.Contains("touch 1h30m", ideas[0]);
        Assert.Contains("touch 30m", ideas[1]);
    }

    [Fact]
    public void A_timer_left_running_is_corrected_by_stopping_with_minutes()
    {
        Run("idea", "Flying machine");
        Run("start", "i1");
        Wait(TimeSpan.FromHours(20));
        Assert.Contains("timer has run 20h0m", Run("review"));

        Run("stop", "45");
        Assert.Contains("touch 45m", Run("ideas"));
    }

    [Fact]
    public void A_note_keeps_where_it_came_from()
    {
        Run("idea", "Flying machine");
        Run("source", "Leonardo da Vinci, Codex on the Flight of Birds, 1505", "https://example.org/codex");
        Run("note", "A bird steers by shifting its centre of gravity", "s1", "i1");
        Assert.Contains("your own thought", Run("note", "So a pilot could steer by leaning", "n1"));

        Assert.Contains("is not a note, idea or source", Fail("note", "Unfounded claim", "s9"));

        var cited = Run("show", "n1");
        Assert.Contains("origin   s1 Leonardo da Vinci, Codex on the Flight of Birds, 1505 <https://example.org/codex>", cited);
        Assert.Contains("n2", cited); // linked from
        Assert.Contains("origin   your own thought", Run("show", "n2"));

        Assert.Contains("n1", Run("show", "s1"));
        Assert.Contains("s1   Leonardo da Vinci", Run("show", "i1")); // credits
    }

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
        Run("idea", "Flying machine");
        Run("goal", "i1", "Carry one person 100 m");
        Run("advance", "i1");
        Run("note", "Birds gain lift from the shape of the wing", "i1");
        Run("req", "i1", "Lifts 80 kg", "Leonardo", "A person has to be on it");
        Run("advance", "i1", "30");

        Wait(TimeSpan.FromDays(20));
        Assert.Contains("Nothing to fix", Run("review"));
        Wait(TimeSpan.FromDays(10));
        Assert.Contains("incubation is over", Run("review"));
    }

    [Fact]
    public void Review_finds_notes_that_connect_to_nothing()
    {
        Run("note", "Water moves fastest in the middle of a river");
        Assert.Contains("n1     note connects to nothing", Run("review"));

        Run("note", "So silt settles at the banks", "n1");
        Assert.DoesNotContain("connects to nothing", Run("review"));
    }

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
    public void Each_review_is_itself_measured()
    {
        Run("note", "An orphan");
        Run("review");
        Run("review");
        Assert.Contains("vinci.findings: 2 measurements", Run("stats", "vinci.findings"));
        Assert.Contains("kept by vinci", Fail("log", "vinci.findings", "0"));
    }

    [Fact]
    public void Measurements_get_control_limits_and_a_sigma_level()
    {
        foreach (var hours in new[] { "7", "8", "7", "8", "7", "8", "7" })
        {
            Run("log", "sleep", hours);
            Wait(TimeSpan.FromDays(1));
        }
        Assert.Contains("stable", Run("stats", "sleep"));
        Assert.Contains("Nothing to fix", Run("review"));

        Run("log", "sleep", "2", "Deadline night");
        var stats = Run("stats", "sleep");
        Assert.Contains("special cause: 2 on 2026-01-08", stats);
        Assert.Contains("sleep  out of control", Run("review"));

        Run("spec", "sleep", "7", "9");
        Assert.Contains("1 of 8 outside, 125000 per million, sigma level 2.7", Run("stats", "sleep"));
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

    [Fact]
    public void Bad_input_is_refused_and_writes_nothing()
    {
        Assert.Contains("unknown command", Fail("fly"));
        Assert.Contains("missing <title>", Fail("idea"));
        Assert.Contains("no idea i7", Fail("goal", "i7", "x"));
        Assert.Contains("must be a number", Fail("log", "sleep", "lots"));
        Assert.False(File.Exists(Path.Combine(dir, "journal.jsonl")));
    }
}
