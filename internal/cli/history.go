package cli

import (
	"flag"
	"strings"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/history"
	"github.com/launch80/L80-CLI/internal/output"
)

// runHistory lists what the configured key has published.
//
// The server is the source of truth: it knows every publish made with this
// key from any machine. The local ledger (written on every successful publish
// here) is the fallback when there is no token or no network, and the only
// place that knows which file each page came from.
func runHistory(e env, args []string) int {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	apiBase, asJSON := addCommonFlags(fs)
	limit := fs.Int("limit", 50, "how many to list, newest first (max 200)")
	local := fs.Bool("local", false, "read only the local record of publishes made from this machine")
	all := fs.Bool("all", false, "keep paging until the whole history is listed")
	tier := fs.String("tier", "", "only pages with this trust tier, e.g. verified or unverified")
	if err := fs.Parse(args); err != nil {
		return api.ExitUsage
	}
	if *limit < 1 {
		*limit = 1
	}
	if *limit > 200 {
		*limit = 200
	}

	cfg := config.Load(*apiBase)
	if !*local {
		entries, keyID, apiErr := fetchHistory(cfg, *limit, *all)
		if apiErr == nil {
			return printHistory(e, filterTier(entries, *tier), "server", keyID, *asJSON)
		}
		switch apiErr.Code {
		case "E_TOKEN_MISSING", "E_NETWORK", "E_NOT_FOUND":
			// No key or no server: fall back to what this machine recorded,
			// and say so, since that list can be shorter.
			output.Detailf(e.stderr, "%s; showing the local record from %s", apiErr.Message, history.Path())
		default:
			return fail(e, apiErr)
		}
	}

	local_, err := history.Read()
	if err != nil {
		return fail(e, api.Newf("E_INPUT_INVALID", "Check the file's permissions.", "could not read %s: %v", history.Path(), err))
	}
	entries := make([]history.Entry, 0, len(local_))
	for _, en := range local_ {
		if len(entries) == *limit && !*all {
			break
		}
		entries = append(entries, en)
	}
	return printHistory(e, filterTier(entries, *tier), "local", cfg.KeyID, *asJSON)
}

func filterTier(entries []history.Entry, tier string) []history.Entry {
	if tier == "" {
		return entries
	}
	out := entries[:0:0]
	for _, en := range entries {
		if en.TrustTier == tier || (tier == "unverified" && en.TrustTier == "") {
			out = append(out, en)
		}
	}
	return out
}

func fetchHistory(cfg config.Config, limit int, all bool) ([]history.Entry, string, *api.Error) {
	client := newClient(cfg)
	var out []history.Entry
	before := ""
	keyID := ""
	for {
		page, apiErr := client.History(limit, before)
		if apiErr != nil {
			return nil, "", apiErr
		}
		keyID = page.KeyID
		for _, a := range page.Artifacts {
			out = append(out, history.Entry{
				GUID: a.GUID, URL: a.URL, TemplateID: a.TemplateID, TemplateVersion: a.TemplateVersion,
				Title: a.Title, ByteSize: a.ByteSize, CreatedAt: a.CreatedAt, TrustTier: a.TrustTier,
			})
		}
		if !all || page.NextBefore == nil || *page.NextBefore == "" {
			return out, keyID, nil
		}
		before = *page.NextBefore
	}
}

func printHistory(e env, entries []history.Entry, source, keyID string, asJSON bool) int {
	if asJSON {
		return emitJSON(e, map[string]any{"source": source, "key_id": keyID, "artifacts": entries})
	}
	if len(entries) == 0 {
		output.Successf(e.stdout, "no artifacts published yet (%s)", source)
		return api.ExitOK
	}
	for _, en := range entries {
		when := en.CreatedAt
		if len(when) >= 16 {
			when = strings.Replace(when[:16], "T", " ", 1)
		}
		tier := en.TrustTier
		if tier == "" {
			tier = "-"
		}
		output.Successf(e.stdout, "%s  %-22s %-10s %s", when, en.TemplateID, tier, en.URL)
		if en.Title != "" {
			output.Detailf(e.stdout, "%s", en.Title)
		}
		if en.SourcePath != "" {
			output.Detailf(e.stdout, "from %s", en.SourcePath)
		}
	}
	output.Detailf(e.stdout, "%d artifact(s), %s record for key %s", len(entries), source, keyID)
	return api.ExitOK
}
