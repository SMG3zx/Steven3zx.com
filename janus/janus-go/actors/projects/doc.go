// Package projects coordinates project records, GitHub repository access,
// repository imports, and snapshot activation. Long-running imports are handled
// by ImportWorker, while durable state is owned by the persistence actor.
package projects
