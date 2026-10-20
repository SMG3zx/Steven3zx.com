import { useEffect, useMemo, useState, type ChangeEvent, type FormEvent } from "react";
import { api, Unauthorized } from "./api";
import { JOB_STATUSES, type Job, type JobInput, type JobStatus } from "./types";

const LABELS: Record<JobStatus, string> = {
  saved: "Saved",
  applied: "Applied",
  interviewing: "Interviewing",
  offer: "Offer",
  rejected: "Rejected",
  withdrawn: "Withdrawn",
};

// Statuses that still need attention; closed ones are rejected and withdrawn.
const ACTIVE: JobStatus[] = ["saved", "applied", "interviewing", "offer"];

const EMPTY: JobInput = {
  company: "",
  title: "",
  location: "",
  url: "",
  status: "saved",
  salary: "",
  applied: "",
  notes: "",
};

function toInput(job: Job): JobInput {
  const { id: _id, createdAt: _c, updatedAt: _u, ...input } = job;
  return input;
}

function message(e: unknown) {
  return e instanceof Error ? e.message : String(e);
}

export function JobTracker({ onSignedOut }: { onSignedOut: () => void }) {
  const [jobs, setJobs] = useState<Job[] | null>(null);
  const [error, setError] = useState("");
  // null: no form open. "new": blank form. A job: editing that job.
  const [editing, setEditing] = useState<Job | "new" | null>(null);

  // Session expiry anywhere in the tracker sends the admin back to sign-in.
  const fail = (e: unknown) => {
    if (e instanceof Unauthorized) {
      onSignedOut();
      return;
    }
    setError(message(e));
  };

  useEffect(() => {
    api.jobs.list().then(setJobs).catch(fail);
    // The list loads once per mount.
  }, []);

  const columns = useMemo(
    () => JOB_STATUSES.map((status) => ({ status, jobs: (jobs ?? []).filter((j) => j.status === status) })),
    [jobs],
  );
  const activeCount = (jobs ?? []).filter((j) => ACTIVE.includes(j.status)).length;

  function upsert(saved: Job) {
    setJobs((prev) => [saved, ...(prev ?? []).filter((j) => j.id !== saved.id)]);
  }

  async function save(input: JobInput) {
    if (editing === "new") {
      upsert(await api.jobs.create(input));
    } else if (editing) {
      upsert(await api.jobs.update(editing.id, input));
    }
    setEditing(null);
  }

  async function remove(job: Job) {
    if (!window.confirm(`Delete “${job.title}” at ${job.company}? This cannot be undone.`)) return;
    try {
      await api.jobs.remove(job.id);
      setJobs((prev) => (prev ?? []).filter((j) => j.id !== job.id));
      setEditing(null);
    } catch (e) {
      fail(e);
    }
  }

  async function moveTo(job: Job, status: JobStatus) {
    try {
      upsert(await api.jobs.update(job.id, { ...toInput(job), status }));
    } catch (e) {
      fail(e);
    }
  }

  return (
    <section className="jobs">
      <div className="jobs-head">
        <div>
          <h2>Job tracker</h2>
          <p className="muted small">
            {jobs ? `${activeCount} active · ${jobs.length} total` : "Loading…"}
          </p>
        </div>
        <button onClick={() => setEditing("new")}>Add job</button>
      </div>

      {error && <p className="error" role="alert">{error}</p>}

      {editing && (
        <JobForm
          key={editing === "new" ? "new" : editing.id}
          initial={editing === "new" ? EMPTY : toInput(editing)}
          isNew={editing === "new"}
          onSave={save}
          onDelete={editing === "new" ? undefined : () => remove(editing)}
          onCancel={() => setEditing(null)}
        />
      )}

      <div className="job-board">
        {columns.map((col) => (
          <div className="job-column" key={col.status} data-status={col.status}>
            <header>
              <span>{LABELS[col.status]}</span>
              <span className="badge">{col.jobs.length}</span>
            </header>
            {col.jobs.map((job) => (
              <article className="job-card" key={job.id}>
                <button className="job-open" onClick={() => setEditing(job)}>
                  <strong>{job.company}</strong>
                  <span>{job.title}</span>
                  {(job.location || job.salary) && (
                    <span className="muted small">{[job.location, job.salary].filter(Boolean).join(" · ")}</span>
                  )}
                  {job.applied && <span className="muted small">Applied {job.applied}</span>}
                </button>
                <select
                  aria-label={`Status for ${job.company}`}
                  value={job.status}
                  onChange={(e) => moveTo(job, e.target.value as JobStatus)}
                >
                  {JOB_STATUSES.map((s) => (
                    <option key={s} value={s}>
                      {LABELS[s]}
                    </option>
                  ))}
                </select>
              </article>
            ))}
            {jobs && col.jobs.length === 0 && <p className="muted small job-empty">Nothing here</p>}
          </div>
        ))}
      </div>
    </section>
  );
}

function JobForm({
  initial,
  isNew,
  onSave,
  onDelete,
  onCancel,
}: {
  initial: JobInput;
  isNew: boolean;
  onSave: (input: JobInput) => Promise<void>;
  onDelete?: () => void;
  onCancel: () => void;
}) {
  const [form, setForm] = useState<JobInput>(initial);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const field =
    (name: keyof JobInput) =>
    (e: ChangeEvent<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>) =>
      setForm((f) => ({ ...f, [name]: e.target.value }));

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await onSave(form);
    } catch (err) {
      setError(message(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="job-form" onSubmit={submit}>
      <div className="job-form-grid">
        <label>
          Company
          <input required maxLength={200} value={form.company} onChange={field("company")} />
        </label>
        <label>
          Role
          <input required maxLength={200} value={form.title} onChange={field("title")} />
        </label>
        <label>
          Status
          <select value={form.status} onChange={field("status")}>
            {JOB_STATUSES.map((s) => (
              <option key={s} value={s}>
                {LABELS[s]}
              </option>
            ))}
          </select>
        </label>
        <label>
          Applied on
          <input type="date" value={form.applied} onChange={field("applied")} />
        </label>
        <label>
          Location
          <input maxLength={200} value={form.location} onChange={field("location")} />
        </label>
        <label>
          Salary
          <input maxLength={200} value={form.salary} onChange={field("salary")} />
        </label>
        <label className="wide">
          Posting link
          <input type="url" maxLength={2000} placeholder="https://" value={form.url} onChange={field("url")} />
        </label>
        <label className="wide">
          Notes
          <textarea rows={4} maxLength={5000} value={form.notes} onChange={field("notes")} />
        </label>
      </div>
      {error && <p className="error" role="alert">{error}</p>}
      <div className="job-form-actions">
        {onDelete && (
          <button type="button" className="ghost danger" onClick={onDelete} disabled={busy}>
            Delete
          </button>
        )}
        <span className="spacer" />
        <button type="button" className="ghost" onClick={onCancel} disabled={busy}>
          Cancel
        </button>
        <button type="submit" disabled={busy}>
          {busy ? "Saving…" : isNew ? "Add job" : "Save"}
        </button>
      </div>
    </form>
  );
}
