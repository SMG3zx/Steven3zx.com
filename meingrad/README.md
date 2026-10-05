# meingrad

`meingrad` is a Python standard-library tool for discovering projects, running their own tasks, seeing which projects changed, and checking Jujutsu-visible files for likely secrets or generated data. It keeps each project's native toolchain and task definitions.

## Commands

Run from this directory:

```powershell
python .\meingrad.py list
python .\meingrad.py run personal-memory test
python .\meingrad.py changed
python .\meingrad.py check
python .\meingrad.py doctor
python .\meingrad.py ci
python .\meingrad.py ci --changed
python .\meingrad.py ci --changed --base master@origin
python .\meingrad.py ci --security-only
python .\meingrad.py ci --project rusty-mines
```

`run` task names are configured per project in `projects.json`. Add or edit the `tasks` entries there to expose another project-native command. The runner invokes argument arrays without a shell.

`changed` reports projects changed in the current Jujutsu working-copy change. Use `--base REVSET` (for example, `master@origin`) to compare the working copy with another Jujutsu revision. Projects that depend on a changed project are included in the affected list.

`check` checks files visible to Jujutsu in the working-copy revision for environment secrets, common dependency/build output, database files, and machine logs.

`doctor` validates project paths, dependency names and cycles, task command shapes, and whether configured executables are available on `PATH`. It does not run builds or tests.

`ci` runs the repository safety check and secret scan first, then runs the commands listed in each project's `ci` array in `projects.json`. Use `--project NAME` to run one project's checks, `--changed` to run checks for changed projects and their dependents, or `--security-only` to skip project commands. `--base REVSET` can be combined with `--changed`. Commands run as argument arrays with no shell and are offline/local by default. CI stops after the first security gate failure.

The secret scan checks recognized private-key blocks, common provider token formats, JWT-like values, and high-entropy values assigned to credential-like keys. It prints paths and finding categories only, never matched values. Generic credential assignments in test folders are ignored because fixtures intentionally use synthetic values; recognized provider tokens and private-key blocks are still checked there. This is a heuristic scanner, not proof that every file is safe; review any candidate before publishing.

## Project catalog

The catalog includes the project folders currently under `D:\steven3zx.com`, with a path, language/toolchain, lifecycle state, dependency notes, and available tasks. Edit `projects.json` as folders move or lifecycle states change. Service folders such as Caddy and Gitea are excluded from the source-project catalog.

## Jujutsu setup

This repository uses Jujutsu (`jj`) with the Git remote `origin`. `changed`, `check`, and `ci` use Jujutsu to inspect repository files and changes. For committed history, pass a Jujutsu revision such as `master@origin` to `--base`; fetch from `origin` separately when you need a fresh remote bookmark.

Avoid adding service state, credentials, databases, or generated files. Review the root `.gitignore` and run `python .\meingrad.py check` before describing and pushing a Jujutsu change.

## Jujutsu shell setup

The PowerShell profile defines `jj` to use the current repository when inside one and defaults to `D:\steven3zx.com` otherwise. It also adds Cargo's binary directory to `PATH`. Open a new PowerShell session to load the profile. In this repo, `origin` is configured as the default fetch and push remote.
