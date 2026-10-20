package httpapi

import "net/http"

// handleCapabilities exposes only the feature switches that change which UI
// controls are truthful to render. It is not a permission claim: every write
// path still enforces its own gate. Values are process-immutable config.
func (s *Server) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不支持")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"selective_revision": s.Cfg.SelectiveRevisionEnabled,
		"bg_plate_lock":      s.Cfg.BgPlateLockEnabled,
	})
}
