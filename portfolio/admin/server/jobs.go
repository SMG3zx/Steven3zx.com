package main

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

// Job statuses, in pipeline order. The tracker shows one column per status.
var jobStatuses = []string{"saved", "applied", "interviewing", "offer", "rejected", "withdrawn"}

const (
	maxJobShort = 200
	maxJobURL   = 2000
	maxJobNotes = 5000
)

// job is one row of the job tracker. Applied is a YYYY-MM-DD date or empty.
type job struct {
	ID        string `json:"id"`
	Company   string `json:"company"`
	Title     string `json:"title"`
	Location  string `json:"location"`
	URL       string `json:"url"`
	Status    string `json:"status"`
	Salary    string `json:"salary"`
	Applied   string `json:"applied"`
	Notes     string `json:"notes"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// normalize trims the editable fields and reports the first invalid one.
func (j *job) normalize() error {
	j.Company = strings.TrimSpace(j.Company)
	j.Title = strings.TrimSpace(j.Title)
	j.Location = strings.TrimSpace(j.Location)
	j.URL = strings.TrimSpace(j.URL)
	j.Salary = strings.TrimSpace(j.Salary)
	j.Applied = strings.TrimSpace(j.Applied)
	j.Notes = strings.TrimSpace(j.Notes)
	switch {
	case j.Company == "" || j.Title == "":
		return errors.New("company and title are required")
	case len(j.Company) > maxJobShort || len(j.Title) > maxJobShort ||
		len(j.Location) > maxJobShort || len(j.Salary) > maxJobShort:
		return errors.New("a field is too long")
	case len(j.Notes) > maxJobNotes:
		return errors.New("notes are too long")
	case !validStatus(j.Status):
		return errors.New("unknown status")
	case j.Applied != "":
		if _, err := time.Parse("2006-01-02", j.Applied); err != nil {
			return errors.New("applied must be a date (YYYY-MM-DD)")
		}
	}
	if j.URL != "" {
		u, err := url.Parse(j.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(j.URL) > maxJobURL {
			return errors.New("url must be an http or https link")
		}
	}
	return nil
}

func validStatus(s string) bool {
	for _, known := range jobStatuses {
		if s == known {
			return true
		}
	}
	return false
}

func (s *store) listJobs(ctx context.Context) ([]job, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, company, title, location, url, status, salary, applied, notes, created_at, updated_at
		FROM jobs ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []job{}
	for rows.Next() {
		var j job
		if err := rows.Scan(&j.ID, &j.Company, &j.Title, &j.Location, &j.URL, &j.Status,
			&j.Salary, &j.Applied, &j.Notes, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (s *store) createJob(ctx context.Context, j job) (job, error) {
	now := time.Now().Unix()
	j.ID, j.CreatedAt, j.UpdatedAt = randomID(), now, now
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO jobs (id, company, title, location, url, status, salary, applied, notes, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.Company, j.Title, j.Location, j.URL, j.Status, j.Salary, j.Applied, j.Notes, j.CreatedAt, j.UpdatedAt)
	return j, err
}

// updateJob replaces every editable field of the job with the same ID.
func (s *store) updateJob(ctx context.Context, j job) (job, error) {
	j.UpdatedAt = time.Now().Unix()
	res, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET company = ?, title = ?, location = ?, url = ?, status = ?, salary = ?,
			applied = ?, notes = ?, updated_at = ?
		WHERE id = ?`,
		j.Company, j.Title, j.Location, j.URL, j.Status, j.Salary, j.Applied, j.Notes, j.UpdatedAt, j.ID)
	if err != nil {
		return j, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return j, errNotFound
	}
	return j, s.db.QueryRowContext(ctx, `SELECT created_at FROM jobs WHERE id = ?`, j.ID).Scan(&j.CreatedAt)
}

func (s *store) deleteJob(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errNotFound
	}
	return nil
}
