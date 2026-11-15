using Vinci;

var home = Environment.GetEnvironmentVariable("VINCI_HOME")
    ?? Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), ".vinci");
var activityWatch = Environment.GetEnvironmentVariable("VINCI_ACTIVITYWATCH") ?? "http://127.0.0.1:5600";

return App.Run(args, new Journal(Path.Combine(home, "journal.jsonl")), Console.In, Console.Out, Console.Error, DateTimeOffset.Now,
    ActivityWatch.At(activityWatch), Git.Log);
