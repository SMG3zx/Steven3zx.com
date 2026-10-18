# 02: Resume cap

**What to build:** A job that has already been resumed three times and is interrupted again fails with an explanatory error, so a job that keeps crashing the server doesn't run forever.

**Blocked by:** 01

**Status:** done

- [x] A job interrupted for the fourth time is marked failed with an error that says it exceeded the resume limit.
- [x] A job resumed one, two, or three times continues to run normally.
- [x] The failed job keeps its resume count.
- [x] Tested through the projects actor's message interface, with a real persistence actor on a temporary SQLite file.
