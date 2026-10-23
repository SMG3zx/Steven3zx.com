/** A project as the gateway lists it. Matches the gateway's JSON. */
export type ProjectSummary = {
  id: string;
  name: string;
  description: string;
  repository?: string;
};

export type ImportJob = {
  id: string;
  status: string;
  progress: number;
  error?: string;
  project_id: string;
  repository: string;
  branch: string;
  commit: string;
};

export type Commit = { sha: string; message: string; date: string };

export function sourceLabel(project: ProjectSummary): string {
  return project.repository || "Empty project";
}

export function shortSha(sha: string): string {
  return sha.slice(0, 7);
}

export function commitLine(commit: Commit): string {
  const firstLine = commit.message.split("\n")[0] ?? "";
  return `${shortSha(commit.sha)} ${firstLine}`;
}

/** What an import's progress says, and whether there is nothing more to wait for. */
export function importLabel(job: ImportJob): { label: string; done: boolean; failed: boolean } {
  if (job.status === "failed") {
    return { label: `Failed: ${job.error ?? "unknown error"}`, done: true, failed: true };
  }
  if (job.status === "complete") {
    return { label: "Imported", done: true, failed: false };
  }
  return { label: `Importing, ${job.progress}% done`, done: false, failed: false };
}
