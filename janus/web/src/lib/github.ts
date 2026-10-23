import type { Commit } from "./projects";

export type Repository = { full_name: string; private: boolean };

/** Picks the branch to show: the one asked for when it exists, else the first one. */
export function chooseBranch(names: string[], requested: string): string {
  if (requested && names.includes(requested)) {
    return requested;
  }
  return names[0] ?? "";
}

type Gateway = {
  get<T>(path: string, options: { cookie?: string }): Promise<T>;
};

export type RepositoryChoices = {
  connected: boolean;
  repositories: Repository[];
  repository: string;
  branch: string;
  branches: string[];
  commits: Commit[];
};

/**
 * Loads what the GitHub pickers need: the repositories, and for a chosen
 * repository its branches and the newest commits on the chosen branch.
 */
export async function loadRepositoryChoices(
  gateway: Gateway,
  cookie: string,
  repository: string,
  requestedBranch: string,
): Promise<RepositoryChoices> {
  const repos = await gateway.get<{
    connected: boolean;
    repositories: Repository[];
  }>("/api/github/repos", { cookie });
  const empty = {
    connected: repos.connected,
    repositories: repos.repositories,
    repository,
    branch: "",
    branches: [] as string[],
    commits: [] as Commit[],
  };
  if (!repos.connected || !repository) {
    return empty;
  }
  const branches = await gateway.get<{ branches: { name: string }[] }>(
    `/api/github/branches?repository=${encodeURIComponent(repository)}`,
    { cookie },
  );
  const names = branches.branches.map((branch) => branch.name);
  const branch = chooseBranch(names, requestedBranch);
  const commits = branch
    ? await gateway.get<Commit[]>(
        `/api/github/commits?repository=${encodeURIComponent(repository)}&branch=${encodeURIComponent(branch)}`,
        { cookie },
      )
    : [];
  return { ...empty, branch, branches: names, commits };
}
