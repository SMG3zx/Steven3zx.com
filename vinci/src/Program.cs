using System.Globalization;
using Vinci;

// Settings for the memory system and the chat model. The same file the memory
// service reads, so `vinci` and the service see the same configuration.
Vinci.Memory.Env.LoadFile(
    Environment.GetEnvironmentVariable("VINCI_ENV_FILE")
        ?? Path.Combine(
            Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
            ".config", "vinci", "memory.env"
        )
);

var home = Environment.GetEnvironmentVariable("VINCI_HOME")
    ?? Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.UserProfile), ".vinci");
var activityWatch = Environment.GetEnvironmentVariable("VINCI_ACTIVITYWATCH")
    ?? "http://127.0.0.1:5600";
var journal = new Journal(Path.Combine(home, "journal.jsonl"));

// The one command that does not end: it answers requests until it is stopped.
if (args is ["serve", ..])
{
    var port = 4000;
    if (
        args.Length > 1
        && !int.TryParse(args[1], NumberStyles.None, CultureInfo.InvariantCulture, out port)
    )
    {
        Console.Error.WriteLine($"vinci: <port> must be a number, got '{args[1]}'");
        return 1;
    }
    await new PortalServer(journal, port).Run();
    return 0;
}

// vinci memory serve | worker | mcp | backup | restore
if (args is ["memory", ..])
    return await Vinci.Memory.MemoryCommands.Run(args[1..], Console.In, Console.Out, Console.Error);

var activity = ActivityWatch.At(activityWatch);

// No command, or `vinci chat`: the conversation with the chat model.
if (args is [] or ["chat"])
    return await Vinci.Chat.ChatCommand.Run(
        journal,
        (commandArgs, output) =>
            App.Run(
                commandArgs,
                journal,
                TextReader.Null,
                output,
                output,
                DateTimeOffset.Now,
                activity,
                Git.Log
            ),
        Console.In,
        Console.Out,
        Console.Error,
        () => DateTimeOffset.Now
    );

return App.Run(
    args,
    journal,
    Console.In,
    Console.Out,
    Console.Error,
    DateTimeOffset.Now,
    activity,
    Git.Log
);
