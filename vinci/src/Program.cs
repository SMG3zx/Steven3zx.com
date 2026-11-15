using Vinci;

var home = Environment.GetEnvironmentVariable("VINCI_HOME")
    ?? Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), ".vinci");

return App.Run(args, new Journal(Path.Combine(home, "journal.jsonl")), Console.Out, Console.Error, DateTimeOffset.Now);
