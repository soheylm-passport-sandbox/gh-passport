package localserver

import (
	"net/http"
	"path/filepath"
)

func (server *Server) handoverRoot() string {
	return filepath.Join(filepath.Dir(server.practiceRoot()), "handover-example")
}
func (server *Server) prepareHandover(response http.ResponseWriter, request *http.Request) {
	server.preparePracticeMode(response, request, true)
}
