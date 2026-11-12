package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/launch80/L80-CLI/internal/api"
	"github.com/launch80/L80-CLI/internal/app"
	"github.com/launch80/L80-CLI/internal/betterbench"
	"github.com/launch80/L80-CLI/internal/config"
	"github.com/launch80/L80-CLI/internal/output"
)

// listingFlags are --public and --private, which every publishing command takes.
type listingFlags struct {
	public, private *bool
}

func addListingFlags(fs *flag.FlagSet) listingFlags {
	return listingFlags{
		public:  fs.Bool("public", false, "also list the page in the public launch80 directory (benchmark and scan pages)"),
		private: fs.Bool("private", false, "keep the page unlisted: reachable only by its link (the default when L80 cannot ask)"),
	}
}

// listing is what was decided about a publish, and why.
type listing struct {
	Public bool
	// Asked is true when the person answered the question on the terminal.
	Asked bool
	// Reason is shown when the page was left unlisted without anyone choosing.
	Reason string
}

// decideListing settles whether a page is listed, before anything is sent:
//
//	--public / --private      the caller chose
//	not a benchmark template  never listed (the directory only holds benchmarks)
//	publish.public always|never
//	a terminal to ask on      ask, defaulting to no
//	otherwise                 unlisted, with a note saying how to list it
//
// Unlisted is the answer whenever nobody chose, so a script or an agent that
// did not ask the person never makes a page public.
func decideListing(e env, lf listingFlags, template string, asJSON bool) (listing, *api.Error) {
	pub, priv := lf.public != nil && *lf.public, lf.private != nil && *lf.private
	if pub && priv {
		return listing{}, api.Newf("E_USAGE", "Pass --public to list the page in the directory, or --private to keep it unlisted, not both.",
			"--public and --private were both given")
	}
	if !api.Listable(template) {
		if pub {
			return listing{}, api.Newf("E_USAGE",
				fmt.Sprintf("Only benchmark and scan pages can be listed (%s). Leave --public out; the page is still reachable by its link.", strings.Join(api.ListableTemplates, ", ")),
				"%s pages cannot be listed in the directory", template)
		}
		return listing{}, nil
	}
	switch {
	case pub:
		return listing{Public: true}, nil
	case priv:
		return listing{}, nil
	}
	mode, src, err := config.PublishListing()
	if err != nil {
		output.Detailf(e.stderr, "warning: publish.public (%s): %v; asking", src, err)
	}
	switch mode {
	case config.ListingAlways:
		return listing{Public: true}, nil
	case config.ListingNever:
		return listing{Reason: "publish.public is never"}, nil
	}
	if asJSON || !stdinIsTTY() || !stderrIsTTY() {
		return listing{Reason: "no --public or --private, and no terminal to ask on"}, nil
	}
	return listing{Public: askYesNo(e, listingQuestion), Asked: true}, nil
}

const listingQuestion = "List this page in the public launch80 Directory (launch80.com/directory)?\n" +
	"  Anyone can find, search and compare it there; without it, only people you send the link to see it.\n" +
	"  You can change it later with `L80 visibility <url> --public|--private`.\n" +
	"List it? [y/N] "

// promptReader reads answers from promptIn. One reader for every question:
// a fresh bufio.Reader per question would buffer past its line and swallow
// the answer to the next one (the listing question, then the GPU).
var (
	promptReader *bufio.Reader
	promptFrom   io.Reader
)

func readAnswer() string {
	if promptReader == nil || promptFrom != promptIn {
		promptReader, promptFrom = bufio.NewReader(promptIn), promptIn
	}
	line, _ := promptReader.ReadString('\n')
	return strings.TrimSpace(line)
}

// askYesNo asks on stderr and reads one line; anything but y/yes is no.
func askYesNo(e env, question string) bool {
	fmt.Fprint(e.stderr, question)
	ans := strings.ToLower(readAnswer())
	return ans == "y" || ans == "yes"
}

// askGPU asks which GPU ran a BetterBench run that names none, when it is
// about to be listed: the directory's GPU filter files it under "Not stated"
// otherwise. Only on a terminal; an empty answer skips.
func askGPU(e env) string {
	fmt.Fprint(e.stderr, "Which GPU ran this? BetterBench could not detect it (it ran on another machine).\n"+
		"  The directory filters by GPU; e.g. \"RTX 4090\" or \"2x RTX 3090\". Press enter to skip.\nGPU: ")
	gpu, err := betterbench.CleanGPU(readAnswer())
	if err != nil {
		output.Detailf(e.stderr, "warning: %v; skipped", err)
		return ""
	}
	return gpu
}

// withGPU applies --gpu to a BetterBench body, or asks for one when the page
// is being listed, names no GPU, and there is a terminal to ask on.
func withGPU(e env, body []byte, gpu string, l listing, asJSON bool) ([]byte, *api.Error) {
	if gpu == "" && l.Public && !betterbench.NamesGPU(body) && !asJSON && stdinIsTTY() && stderrIsTTY() {
		gpu = askGPU(e)
	}
	if gpu == "" {
		return body, nil
	}
	out, err := betterbench.WithGPUNote(body, gpu)
	if err != nil {
		return nil, api.Newf("E_USAGE", "Pass a GPU name such as --gpu \"RTX 4090\".", "%v", err)
	}
	return out, nil
}

// printListing says where the page is, after a successful publish.
func printListing(e env, art *api.Artifact, l listing, baseURL string) {
	if !api.Listable(art.TemplateID) {
		return
	}
	switch {
	case l.Public && art.Listed:
		output.Detailf(e.stdout, "listed in the directory: %s", api.DirectoryURL(baseURL))
	case l.Public:
		// An older server ignores ?listed=true.
		output.Detailf(e.stderr, "warning: the server did not list the page; try `L80 visibility %s --public`", art.URL)
	default:
		output.Detailf(e.stdout, "unlisted: only people with the link can see it · list it: L80 visibility %s --public", art.URL)
	}
}

// runVisibility is `L80 visibility <url|guid> --public|--private`.
func runVisibility(e env, args []string) int {
	fs := flag.NewFlagSet("visibility", flag.ContinueOnError)
	fs.SetOutput(e.stderr)
	apiBase, asJSON := addCommonFlags(fs)
	lf := addListingFlags(fs)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return api.ExitUsage
	}
	if len(pos) != 1 {
		return fail(e, api.Newf("E_USAGE", "Run `L80 visibility <page url or guid> --public` (or --private). `L80 history` lists your pages.",
			"visibility takes one page URL or guid"))
	}
	if *lf.public == *lf.private {
		return fail(e, api.Newf("E_USAGE", "Pass --public to list the page in the directory, or --private to take it out.",
			"pass exactly one of --public or --private"))
	}
	t, apiErr := app.ResolveTarget(pos[0])
	if apiErr != nil {
		return fail(e, apiErr)
	}
	if t.GUID == "" {
		return fail(e, api.Newf("E_USAGE", "Pass the page URL or guid (see `L80 history`), not the file.",
			"%s is a file, and it has not been published from this machine", pos[0]))
	}
	cfg := config.Load(*apiBase)
	v, apiErr := newClient(cfg).SetVisibility(t.GUID, *lf.public)
	if apiErr != nil {
		return fail(e, apiErr)
	}
	if *asJSON {
		return emitJSON(e, v)
	}
	if v.Listed {
		output.Successf(e.stdout, "listed: %s", v.URL)
		output.Detailf(e.stdout, "in the directory: %s", api.DirectoryURL(cfg.BaseURL))
	} else {
		output.Successf(e.stdout, "unlisted: %s", v.URL)
		output.Detailf(e.stdout, "out of the directory; the link still works")
	}
	return api.ExitOK
}
