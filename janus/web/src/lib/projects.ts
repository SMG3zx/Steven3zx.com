/** The fields of a project that the list page shows. Matches the gateway's JSON. */
export type ProjectSummary = {
  id: string;
  name: string;
  description: string;
  repository?: string;
};

export function sourceLabel(project: ProjectSummary): string {
  return project.repository || "Empty project";
}
