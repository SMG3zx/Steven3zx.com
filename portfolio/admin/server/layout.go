package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"time"
)

const (
	// maxLayoutNodes bounds the saved positions. The map has about nine fixed
	// nodes plus one per container, so this is far above real use.
	maxLayoutNodes = 512
	// maxLayoutIDLength bounds a node id such as "ctr-<container id>".
	maxLayoutIDLength = 128
	maxLayoutBody     = 64 << 10
)

// layoutPosition is where one map node sits, in flow coordinates.
type layoutPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type layoutBody struct {
	Positions map[string]layoutPosition `json:"positions"`
}

// getLayout returns the signed-in admin's saved node positions. A user who has
// never moved a node gets an empty map and the default layout.
func (s *server) getLayout(w http.ResponseWriter, r *http.Request) {
	u := r.Context().Value(ctxKey{}).(user)
	positions, err := s.store.loadLayout(r.Context(), u.ID)
	if errors.Is(err, errNotFound) {
		positions = map[string]layoutPosition{}
	} else if err != nil {
		log.Printf("load layout: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, layoutBody{Positions: positions})
}

// putLayout replaces the admin's saved node positions with the ones sent.
func (s *server) putLayout(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-site request refused")
		return
	}
	var in layoutBody
	if err := json.NewDecoder(io.LimitReader(r.Body, maxLayoutBody)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if len(in.Positions) > maxLayoutNodes {
		writeError(w, http.StatusBadRequest, "too many nodes")
		return
	}
	for id := range in.Positions {
		if id == "" || len(id) > maxLayoutIDLength {
			writeError(w, http.StatusBadRequest, "invalid node id")
			return
		}
	}
	u := r.Context().Value(ctxKey{}).(user)
	if err := s.store.saveLayout(r.Context(), u.ID, in.Positions); err != nil {
		log.Printf("save layout: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (st *store) saveLayout(ctx context.Context, userID string, positions map[string]layoutPosition) error {
	data, err := json.Marshal(positions)
	if err != nil {
		return err
	}
	_, err = st.db.ExecContext(ctx,
		`INSERT INTO layouts (user_id, positions, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET positions = excluded.positions, updated_at = excluded.updated_at`,
		userID, string(data), time.Now().Unix())
	return err
}

func (st *store) loadLayout(ctx context.Context, userID string) (map[string]layoutPosition, error) {
	var raw string
	err := st.db.QueryRowContext(ctx, `SELECT positions FROM layouts WHERE user_id = ?`, userID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	positions := map[string]layoutPosition{}
	if err := json.Unmarshal([]byte(raw), &positions); err != nil {
		return nil, err
	}
	return positions, nil
}
