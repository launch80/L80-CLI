package pwdet

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// TemplateID is the launch80 template a pwdet report is published under.
const TemplateID = "generic.report.v1"

// generic.report.v1 limits, from the server's schema.
const (
	maxTitle, maxSubtitle, maxSummary = 120, 160, 600
	maxMetrics, maxLabel, maxValue    = 12, 48, 32
	maxNote                           = 120
	maxSections, maxHeading, maxBody  = 10, 100, 4000
	maxBullets, maxBullet             = 12, 240
)

type Payload struct {
	Template string    `json:"$template"`
	Title    string    `json:"title"`
	Subtitle string    `json:"subtitle,omitempty"`
	Summary  string    `json:"summary"`
	Metrics  []Metric  `json:"metrics,omitempty"`
	Sections []Section `json:"sections,omitempty"`
}

type Metric struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Unit  string `json:"unit,omitempty"`
	Note  string `json:"note,omitempty"`
}

type Section struct {
	Heading string   `json:"heading"`
	Body    string   `json:"body"`
	Bullets []string `json:"bullets,omitempty"`
}

// Options are publisher-supplied overrides.
type Options struct {
	Title, Summary string
}

var verdictWords = map[string]string{
	"clean": "clean", "suspicious": "suspicious", "likely_tampered": "likely tampered",
	"malicious": "malicious", "inconclusive": "inconclusive",
}

// Build maps a report to a generic.report.v1 payload. Every string is
// scrubbed and clipped to the template's limits; a user-supplied title or
// summary that is too long is an error rather than silently cut.
func Build(r *Report, opt Options) (*Payload, error) {
	sc := NewScrubber(r)
	if utf8.RuneCountInString(opt.Title) > maxTitle {
		return nil, fmt.Errorf("--title is longer than %d characters", maxTitle)
	}
	if utf8.RuneCountInString(opt.Summary) > maxSummary {
		return nil, fmt.Errorf("--summary is longer than %d characters", maxSummary)
	}
	label := r.Subject.Label()
	verdict := "no verdict"
	var rationale []string
	if r.Verdict != nil {
		verdict = verdictWords[r.Verdict.Label]
		if verdict == "" {
			verdict = r.Verdict.Label
		}
		rationale = r.Verdict.Rationale
	}

	p := &Payload{Template: TemplateID}
	p.Title = opt.Title
	if p.Title == "" {
		p.Title = clip("pwdet: "+label+" — "+verdict, maxTitle)
	}
	date := r.Finished
	if date == "" {
		date = r.Started
	}
	if len(date) >= 10 {
		date = date[:10]
	}
	p.Subtitle = clip(strings.Join(nonEmpty("Poisoned Weights Detector "+r.ToolVersion, "profile "+r.Profile, date), " · "), maxSubtitle)
	p.Summary = opt.Summary
	if p.Summary == "" {
		s := fmt.Sprintf("Automated scan verdict: %s.", verdict)
		if len(rationale) > 0 {
			s += " " + strings.Join(rationale, "; ") + "."
		}
		s += " An automated, self-reported scan, not an audit: a clean verdict means these checks found nothing, not that the model is safe."
		p.Summary = clip(s, maxSummary)
	}

	counts := r.Counts()
	run := 0
	for _, n := range counts {
		run += n
	}
	worst := "none"
	for _, f := range r.ToReview() {
		worst = f.EffectiveSeverity()
		break
	}
	add := func(label, value, note string) {
		if value == "" || len(p.Metrics) >= maxMetrics {
			return
		}
		p.Metrics = append(p.Metrics, Metric{Label: clip(label, maxLabel), Value: clip(value, maxValue), Note: clip(note, maxNote)})
	}
	add("Verdict", verdict, "")
	add("Observations", fmt.Sprint(run), "pass, warn, fail, skipped and error findings")
	add("Pass", fmt.Sprint(counts["pass"]), "")
	add("Warn", fmt.Sprint(counts["warn"]), "")
	add("Fail", fmt.Sprint(counts["fail"]), "")
	if n := counts["skipped"] + counts["error"]; n > 0 {
		add("Skipped or errored", fmt.Sprint(n), "checks that could not run here")
	}
	add("Worst open severity", worst, "after downgrading disclosed modifications one step")
	add("Profile", r.Profile, "")
	if r.Subject.Revision != "" {
		add("Revision", shortRev(r.Subject.Revision), "Hub commit scanned")
	}
	add("pwdet", r.ToolVersion, "")

	// Findings to review, worst first.
	review := r.ToReview()
	if len(review) > 0 {
		sec := Section{Heading: "Findings to review"}
		for _, f := range review {
			if len(sec.Bullets) == maxBullets {
				break
			}
			b := fmt.Sprintf("[%s] %s: %s", f.EffectiveSeverity(), f.CheckID, sc.Clean(f.Title))
			if f.Declared {
				b += " (disclosed by the publisher)"
			}
			sec.Bullets = append(sec.Bullets, clip(b, maxBullet))
		}
		sec.Body = fmt.Sprintf("%d warn or fail observation(s), most severe first.", len(review))
		if more := len(review) - len(sec.Bullets); more > 0 {
			sec.Body += fmt.Sprintf(" %d more are in the full report.", more)
		}
		p.Sections = append(p.Sections, sec)
	} else {
		p.Sections = append(p.Sections, Section{Heading: "Findings to review", Body: "No check reported a warning or a failure."})
	}

	if r.Verdict != nil && len(r.Verdict.Coverage) > 0 {
		layers := make([]string, 0, len(r.Verdict.Coverage))
		for l := range r.Verdict.Coverage {
			layers = append(layers, l)
		}
		sort.Strings(layers)
		sec := Section{Heading: "Coverage", Body: "Checks run per layer. A layer that is absent was not part of this profile."}
		for _, l := range layers {
			c := r.Verdict.Coverage[l]
			sec.Bullets = append(sec.Bullets, clip(fmt.Sprintf("%s: %d run, %d pass, %d warn, %d fail, %d skipped, %d error",
				l, c["run"], c["pass"], c["warn"], c["fail"], c["skipped"], c["error"]), maxBullet))
		}
		p.Sections = append(p.Sections, sec)
	}

	if len(r.Lineage) > 0 {
		sec := Section{Heading: "Lineage", Body: "The chain the model claims, as pwdet resolved it."}
		for _, h := range r.Lineage {
			v := "unverified"
			if h.Verified {
				v = "verified"
			}
			sec.Bullets = append(sec.Bullets, clip(sc.Clean(fmt.Sprintf("%s: %s (%s, %s)", h.Hop, h.Ref.Label(), h.Relation, v)), maxBullet))
			if len(sec.Bullets) == maxBullets {
				break
			}
		}
		p.Sections = append(p.Sections, sec)
	}

	p.Sections = append(p.Sections, Section{Heading: "Method", Body: "Poisoned Weights Detector " + r.ToolVersion +
		" checks a checkpoint in up to four layers: static (files, headers, configs, tokenizer, chat template, weight statistics), " +
		"provenance (Hub metadata, parent hashes, signed manifest), black-box (paired behavioural probes against a served copy) and " +
		"white-box (activations and per-layer deltas). Findings the publisher disclosed are downgraded one severity step but never hidden. " +
		"Published from the report.json the tool wrote; the scanning machine's name, paths and endpoints are removed."})

	// Scrub the page as a whole, then make sure nothing identifying is left.
	p.Title, p.Subtitle, p.Summary = sc.Clean(p.Title), sc.Clean(p.Subtitle), sc.Clean(p.Summary)
	for i := range p.Metrics {
		p.Metrics[i].Value, p.Metrics[i].Note = sc.Clean(p.Metrics[i].Value), sc.Clean(p.Metrics[i].Note)
	}
	for i := range p.Sections {
		p.Sections[i].Heading = clip(sc.Clean(p.Sections[i].Heading), maxHeading)
		p.Sections[i].Body = clip(sc.Clean(p.Sections[i].Body), maxBody)
	}
	if len(p.Sections) > maxSections {
		p.Sections = p.Sections[:maxSections]
	}
	body, _ := json.Marshal(p)
	if leaks := sc.Leaks(string(body)); len(leaks) > 0 {
		return nil, fmt.Errorf("refusing to publish: the page would still contain %q", leaks[0])
	}
	return p, nil
}

func clip(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:max-1])) + "…"
}

func shortRev(r string) string {
	if len(r) > 12 {
		return r[:12]
	}
	return r
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" && !strings.HasSuffix(p, " ") {
			out = append(out, p)
		}
	}
	return out
}
