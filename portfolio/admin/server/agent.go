package main

import (
	"log"
	"net/http"
)

// restartAgent asks the host supervisor to restart the host agent. The request
// goes to the supervisor, not the agent, because the agent is usually the
// thing that is down. The supervisor answers 202 as soon as it has queued the
// restart; the live socket reconnects by itself once the agent is back.
func (s *server) restartAgent(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-site request refused")
		return
	}
	u := r.Context().Value(ctxKey{}).(user)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.agentControlURL+"/restart", nil)
	if err != nil {
		log.Printf("restart host agent: %v", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	req.Header.Set("Authorization", "Bearer "+s.agentToken)
	res, err := s.agent.Do(req)
	if err != nil {
		log.Printf("restart host agent by %s: supervisor unreachable: %v", u.Username, err)
		writeError(w, http.StatusBadGateway, "host supervisor unreachable")
		return
	}
	res.Body.Close()
	if res.StatusCode != http.StatusAccepted {
		log.Printf("restart host agent by %s: supervisor answered %d", u.Username, res.StatusCode)
		writeError(w, http.StatusBadGateway, "host supervisor refused the restart")
		return
	}
	log.Printf("host agent restart requested by %s", u.Username)
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "restart requested"})
}
