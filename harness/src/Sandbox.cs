using System.Diagnostics;
using System.Text;

namespace Harness;

/// <summary>
/// Runs a shell command on model-written code. It runs under bubblewrap with no network, a read-only
/// filesystem and only the workspace writable. Without bwrap it refuses to run, because the command
/// executes code the model just wrote.
/// </summary>
public static class Sandbox
{
    public static bool Available => File.Exists("/usr/bin/bwrap") || File.Exists("/bin/bwrap");

    public static (int ExitCode, string Output) Run(
        string command,
        string workspace,
        int seconds = 60
    )
    {
        if (!Available)
            return (
                126,
                "Refusing to run: bubblewrap (bwrap) is not installed, and test commands run model-written code."
            );
        var ws = Path.GetFullPath(workspace);
        var psi = new ProcessStartInfo("bwrap")
        {
            RedirectStandardOutput = true,
            RedirectStandardError = true,
        };
        foreach (
            var a in new[]
            {
                "--ro-bind",
                "/",
                "/",
                "--dev",
                "/dev",
                "--proc",
                "/proc",
                "--tmpfs",
                "/tmp",
                "--bind",
                ws,
                ws,
                "--chdir",
                ws,
                "--unshare-all",
                "--die-with-parent",
                "--setenv",
                "PYTHONDONTWRITEBYTECODE",
                "1",
                "--setenv",
                "HOME",
                ws,
                "bash",
                "-c",
                command,
            }
        )
            psi.ArgumentList.Add(a);

        using var proc = Process.Start(psi)!;
        var sb = new StringBuilder();
        var gate = new object();
        void Append(string? line)
        {
            if (line is null)
                return;
            lock (gate)
                sb.AppendLine(line);
        }
        proc.OutputDataReceived += (_, e) => Append(e.Data);
        proc.ErrorDataReceived += (_, e) => Append(e.Data);
        proc.BeginOutputReadLine();
        proc.BeginErrorReadLine();
        if (!proc.WaitForExit(seconds * 1000))
        {
            proc.Kill(true);
            proc.WaitForExit();
            lock (gate)
                return (124, sb + $"\nTimed out after {seconds} seconds.");
        }
        proc.WaitForExit();
        lock (gate)
            return (proc.ExitCode, sb.ToString());
    }

    public static string Tail(string s, int max = 4000) => s.Length <= max ? s : "…" + s[^max..];
}
