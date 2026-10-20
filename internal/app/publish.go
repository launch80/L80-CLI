package app

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/history"
)

// PublishRequest is an already-validated body ready to send.
type PublishRequest struct {
	Body       []byte
	Template   string
	Title      string // as the page will show it, for the local record
	SourcePath string // the file the body came from, if any
	APIBase    string
}

// Published is a successful publish plus anything worth telling the user.
type Published struct {
	Artifact *api.Artifact
	// LedgerErr is set when the local record could not be written. The
	// publish itself still succeeded.
	LedgerErr error
}

// Publish sends the body and records it in the local ledger with the hashes
// a verification request later uses to show the page came from this file.
func Publish(client *api.Client, req PublishRequest) (Published, *api.Error) {
	artifact, apiErr := client.Publish(req.Body)
	if apiErr != nil {
		return Published{}, apiErr
	}
	// The ledger outlives the working directory, so it records where the
	// file is, not where it was relative to.
	if req.SourcePath != "" {
		if abs, err := filepath.Abs(req.SourcePath); err == nil {
			req.SourcePath = abs
		}
	}
	entry := history.Entry{
		GUID: artifact.GUID, URL: artifact.URL, TemplateID: artifact.TemplateID,
		TemplateVersion: artifact.TemplateVersion, Title: req.Title,
		ByteSize: artifact.ByteSize, CreatedAt: artifact.CreatedAt,
		SourcePath: req.SourcePath, APIBase: req.APIBase,
		BodySHA256: sha256Hex(req.Body), ContentHash: artifact.ContentHash, TrustTier: artifact.TrustTier,
	}
	if req.SourcePath != "" {
		if data, err := os.ReadFile(req.SourcePath); err == nil {
			entry.SourceSHA256 = sha256Hex(data)
		}
	}
	return Published{Artifact: artifact, LedgerErr: history.Append(entry)}, nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
