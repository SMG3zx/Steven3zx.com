const test = Bun.spawn(["bun", "x", "vitest", "run", "tests/ollama.integration.test.ts"], {
  cwd: import.meta.dir.replace(/\\scripts$/, ""),
  env: { ...process.env, MEINFACTORY_REAL_MODEL: "1" },
  stdin: "inherit",
  stdout: "inherit",
  stderr: "inherit",
});

process.exit(await test.exited);
