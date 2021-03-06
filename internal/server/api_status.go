package server

import (
	"context"
	"net/http"

	"github.com/kopia/kopia/internal/serverapi"
)

func (s *Server) handleStatus(ctx context.Context, r *http.Request) (interface{}, *apiError) {
	bf := s.rep.Content.Format
	bf.HMACSecret = nil
	bf.MasterKey = nil

	return &serverapi.StatusResponse{
		ConfigFile:      s.rep.ConfigFile,
		CacheDir:        s.rep.Content.CachingOptions.CacheDirectory,
		BlockFormatting: bf,
		Storage:         s.rep.Blobs.ConnectionInfo().Type,
	}, nil
}
