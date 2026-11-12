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

// listingFlags are --public, --private and --no-author, which every publishing command takes.
type listingFlags struct {
	public, private, noAuthor *bool
}

func addListingFlags(fs *flag.FlagSet) listingFlags {
	return listingFlags{
		noAuthor: fs.Bool("no-author", false, "leave \"Author: <your key id>\" off the page (shown by default)"),
		public:   fs.Bool("public", false, "list the page in the public launch80 Directory (the default for benchmark and scan pages)"),
		private:  fs.Bool("private", false, "keep the page unlisted: reachable only by its link, not in the Directory"),
	}
}

// listing is what was decided about a publish, and why.
type listing struct {
	Public bool
	// Asked is true when the person answered the question on the terminal.
	Asked bool
	// Reason says why, when the page was left unlisted without a --private.
	Reason string
	// NoAuthor leaves "Author: <key id>" off the page (--no-author, or publish.author hide).
	NoAuthor bool
}

// decideListing settles whether a page is listed, before anything is sent:
//
//	--public / --private      the caller chose
//	not a benchmark template  never listed (the directory only holds benchmarks)
//	publish.public always     listed (the default since 1.4.1)
//	publish.public never      unlisted
//	publish.public ask        ask on a terminal, defaulting to no; unlisted
//	                          when there is no terminal to ask on
//
// Whatever the outcome, printListing says where the page is and how to change it.
func decideListing(e env, lf listingFlags, template string, asJSON bool) (listing, *api.Error) {
	l, err := decideVisibility(e, lf, template, asJSON)
	if err != nil {
		return l, err
	}
	hidden, src, aErr := config.PublishAuthorHidden()
	if aErr != nil {
		output.Detailf(e.stderr, "warning: publish.author (%s): %v; showing the author", src, aErr)
	}
	l.NoAuthor = (lf.noAuthor != nil && *lf.noAuthor) || hidden
	return l, nil
}

// decideVisibility is decideListing's listed-or-not half.
func decideVisibility(e env, lf listingFlags, template string, asJSON bool) (listing, *api.Error) {
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

// printListing explains a successful publish, under the "published:" line:
// what the page is, who can see it, its trust, what is left of the quota and
// what to do next. The server's quota is used when it sends one; everything
// else is known here, so an older server gets the same explanation.
func printListing(e env, art *api.Artifact, l listing, baseURL string) {
	row := func(label, format string, args ...any) {
		output.Detailf(e.stdout, "%-12s "+format, append([]any{label}, args...)...)
	}
	more := func(format string, args ...any) {
		output.Detailf(e.stdout, "%-12s "+format, append([]any{""}, args...)...)
	}

	page := fmt.Sprintf("%s v%d · %s", art.TemplateID, art.TemplateVersion, humanBytes(art.ByteSize))
	if art.Title != "" {
		page = fmt.Sprintf("%q · %s", art.Title, page)
	}
	row("page", "%s", page)
	switch {
	case art.Author != "":
		row("author", "%s (shown on the page; --no-author leaves it off)", art.Author)
	case l.NoAuthor:
		row("author", "not shown (--no-author)")
	}

	listable := api.Listable(art.TemplateID)
	switch {
	case !listable:
		row("who sees it", "anyone with the link; %s pages cannot be listed in the Directory", art.TemplateID)
	case art.Listed:
		row("who sees it", "anyone: listed in the launch80 Directory, where anyone can find, search and compare it")
		more("%s", api.DirectoryURL(baseURL))
	default:
		row("who sees it", "only people you send the link to (unlisted)")
		if l.Reason != "" {
			more("(%s)", l.Reason)
		}
	}
	if listable && l.Public && !art.Listed {
		// An older server ignores ?listed=true.
		output.Detailf(e.stderr, "warning: the server did not list the page; try `L80 visibility %s --public`", art.URL)
	}

	if art.TrustTier == "unverified" || art.TrustTier == "" {
		row("trust", "self-reported until Launch80 verifies it")
	} else {
		row("trust", "%s", art.TrustTier)
	}
	row("changes", "none: the page never changes; publish again to correct it")
	if q := art.Quota; q != nil && q.Limit > 0 {
		row("quota", "%d of %d publishes left in the next %d hours", q.Remaining, q.Limit, q.WindowHours)
	}

	var next [][2]string
	if listable {
		if art.Listed {
			next = append(next, [2]string{"take it out of the Directory", "L80 visibility " + art.URL + " --private"})
		} else {
			next = append(next, [2]string{"list it in the Directory", "L80 visibility " + art.URL + " --public"})
		}
	}
	if api.Verifiable(art.TemplateID) {
		next = append(next, [2]string{"ask Launch80 to verify it", "L80 verify request " + art.URL})
	}
	for i, n := range next {
		label := ""
		if i == 0 {
			label = "next"
		}
		output.Detailf(e.stdout, "%-12s %-30s %s", label, n[0], n[1])
	}
}

// humanBytes is 412 B, 3.1 KB or 1.2 MB.
func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
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
		output.Detailf(e.stdout, "anyone can now find, search and compare it in the launch80 Directory (within a minute):")
		output.Detailf(e.stdout, "%s", api.DirectoryURL(cfg.BaseURL))
		output.Detailf(e.stdout, "undo: L80 visibility %s --private", v.URL)
	} else {
		output.Successf(e.stdout, "unlisted: %s", v.URL)
		output.Detailf(e.stdout, "out of the Directory now; the link still works for anyone who has it")
		output.Detailf(e.stdout, "undo: L80 visibility %s --public", v.URL)
	}
	return api.ExitOK
}
