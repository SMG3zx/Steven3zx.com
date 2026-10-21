package runs

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	contractruns "janus/internal/contract/runs"
	"janus/internal/domain/artifact"
	"janus/internal/domain/templates"
	"janus/internal/platform/wasmrun"
)

// newRun records a new running Run of owner's Build buildID and returns its ID.
// The table keeps the newest maxRunTable Runs.
func (s *Service) newRun(owner, buildID string) string {
	id := uuid.NewString()
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictOldest()
	s.issued++
	s.runs[id] = record{owner: owner, seq: s.issued, run: contractruns.Run{ID: id, BuildID: buildID, Status: contractruns.StatusRunning, CreatedAt: now, UpdatedAt: now}}
	return id
}

// evictOldest removes the oldest Run when the table is full. The caller holds mu.
func (s *Service) evictOldest() {
	if len(s.runs) < maxRunTable {
		return
	}
	var oldest string
	var when uint64
	for key, value := range s.runs {
		if oldest == "" || value.seq < when {
			oldest, when = key, value.seq
		}
	}
	delete(s.runs, oldest)
}

// execute opens the Artifact, runs its module and records the outcome. A panic
// anywhere in it becomes a failed Run. It runs in the background and is counted
// in jobs.
func (s *Service) execute(id, artifactName string, data []byte, args []string, limits templates.Resources) {
	defer s.jobs.Done()
	var (
		module string
		result wasmrun.Result
		err    error
	)
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("WASM run panicked: %v", recovered)
		}
		s.complete(id, module, result, err)
	}()
	var bytes []byte
	bytes, module, err = artifact.Module(data, artifactName)
	if err != nil {
		return
	}
	result, err = wasmrun.Run(context.Background(), bytes, wasmrun.Input{Args: args, Limits: limits})
}

// complete stores the outcome of a Run and frees its active slot.
func (s *Service) complete(id, module string, result wasmrun.Result, err error) {
	s.mu.Lock()
	rec, kept := s.runs[id]
	if kept {
		run := rec.run
		run.UpdatedAt = time.Now().UTC()
		run.Stdout = string(result.Stdout)
		if result.StdoutTruncated {
			run.Stdout += "\n[output truncated]"
		}
		run.Stderr = string(result.Stderr)
		run.Module = module
		run.Usage = result.Usage
		if err != nil {
			run.Status = contractruns.StatusFailed
			run.Error = err.Error()
		} else {
			run.Status = contractruns.StatusComplete
		}
		s.runs[id] = record{owner: rec.owner, seq: rec.seq, run: run}
	}
	s.mu.Unlock()
	s.active.Add(-1)
}
