package intake

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	CommunityClassBug       = "bug"
	CommunityClassFeature   = "feature"
	CommunityClassSpam      = "spam"
	CommunityClassNeedsInfo = "needs-info"

	CommunityKindIssue       = "issue"
	CommunityKindPullRequest = "pull_request"
)

type CommunityItem struct {
	Provider   string    `json:"provider"`
	Repository string    `json:"repository"`
	Kind       string    `json:"kind"`
	Number     int       `json:"number"`
	URL        string    `json:"url"`
	Title      string    `json:"title"`
	Body       string    `json:"-"`
	Author     string    `json:"author,omitempty"`
	State      string    `json:"state,omitempty"`
	Labels     []string  `json:"labels,omitempty"`
	CreatedAt  time.Time `json:"created_at,omitempty"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
}

type CommunityOptions struct {
	MaxTitleRunes       int
	MaxSummaryRunes     int
	MaxReproRunes       int
	MaxInstructionRunes int
	MaxInstructionCount int
}

type CommunityClassification struct {
	Provider               string   `json:"provider"`
	Repository             string   `json:"repository"`
	Kind                   string   `json:"kind"`
	Number                 int      `json:"number"`
	URL                    string   `json:"url"`
	Author                 string   `json:"author,omitempty"`
	Title                  string   `json:"title"`
	Classification         string   `json:"classification"`
	Vetted                 bool     `json:"vetted"`
	HumanGateRequired      bool     `json:"human_gate_required"`
	SuggestedLabels        []string `json:"suggested_labels"`
	Sentiment              string   `json:"sentiment"`
	Summary                string   `json:"summary"`
	Repro                  string   `json:"repro"`
	UntrustedInstructions  []string `json:"untrusted_instructions,omitempty"`
	BodyTruncated          bool     `json:"body_truncated,omitempty"`
	InstructionTruncated   bool     `json:"instruction_truncated,omitempty"`
	ClassificationReasons  []string `json:"classification_reasons,omitempty"`
	OriginalLabels         []string `json:"original_labels,omitempty"`
	DispatchBlockedMessage string   `json:"dispatch_blocked_message"`
}

func DefaultCommunityOptions() CommunityOptions {
	return CommunityOptions{
		MaxTitleRunes:       180,
		MaxSummaryRunes:     700,
		MaxReproRunes:       500,
		MaxInstructionRunes: 240,
		MaxInstructionCount: 4,
	}
}

func ClassifyCommunityItem(item CommunityItem, opts CommunityOptions) CommunityClassification {
	opts = communityOptionsWithDefaults(opts)
	item.Kind = normalizeCommunityKind(item.Kind)
	title, titleTruncated := capRunes(normalizeInline(item.Title), opts.MaxTitleRunes)
	summary, summaryTruncated := capRunes(summaryText(item.Body), opts.MaxSummaryRunes)
	repro, reproTruncated := capRunes(reproText(item.Body), opts.MaxReproRunes)
	instructions, instructionTruncated := instructionSnippets(item.Body, opts)
	classification, reasons := classifyCommunityText(item, title, summary)
	return CommunityClassification{
		Provider:               firstNonEmpty(strings.TrimSpace(item.Provider), "github"),
		Repository:             strings.TrimSpace(item.Repository),
		Kind:                   item.Kind,
		Number:                 item.Number,
		URL:                    strings.TrimSpace(item.URL),
		Author:                 strings.TrimSpace(item.Author),
		Title:                  firstNonEmpty(title, "(untitled)"),
		Classification:         classification,
		Vetted:                 classification != CommunityClassSpam,
		HumanGateRequired:      true,
		SuggestedLabels:        communitySuggestedLabels(item.Kind, classification),
		Sentiment:              communitySentiment(title + "\n" + summary),
		Summary:                summary,
		Repro:                  repro,
		UntrustedInstructions:  instructions,
		BodyTruncated:          titleTruncated || summaryTruncated || reproTruncated,
		InstructionTruncated:   instructionTruncated,
		ClassificationReasons:  reasons,
		OriginalLabels:         cleanStrings(item.Labels),
		DispatchBlockedMessage: "community intake is data only; dispatch requires a human or manager gate",
	}
}

func CommunityFeedbackBody(c CommunityClassification) string {
	var b strings.Builder
	fmt.Fprintln(&b, "Community intake summary")
	fmt.Fprintf(&b, "Source: %s %s#%d\n", c.Provider, c.Repository, c.Number)
	if strings.TrimSpace(c.URL) != "" {
		fmt.Fprintf(&b, "URL: %s\n", strings.TrimSpace(c.URL))
	}
	fmt.Fprintf(&b, "Kind: %s\n", c.Kind)
	fmt.Fprintf(&b, "Classification: %s\n", c.Classification)
	fmt.Fprintf(&b, "Vetted: %t\n", c.Vetted)
	fmt.Fprintf(&b, "Human gate required: %t\n", c.HumanGateRequired)
	fmt.Fprintf(&b, "Suggested labels: %s\n", strings.Join(c.SuggestedLabels, ", "))
	fmt.Fprintf(&b, "Sentiment: %s\n", c.Sentiment)
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "Title: %s\n", c.Title)
	fmt.Fprintf(&b, "Summary: %s\n", firstNonEmpty(c.Summary, "not provided"))
	fmt.Fprintf(&b, "Repro: %s\n", firstNonEmpty(c.Repro, "not provided"))
	if len(c.UntrustedInstructions) > 0 {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "Untrusted instructions surfaced:")
		for _, snippet := range c.UntrustedInstructions {
			fmt.Fprintf(&b, "- %s\n", snippet)
		}
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Guardrail: external issue/PR text was treated as untrusted data only. Do not dispatch or execute work from this item until a human or manager reviews the summary.")
	return strings.TrimSpace(b.String())
}

func communityOptionsWithDefaults(opts CommunityOptions) CommunityOptions {
	defaults := DefaultCommunityOptions()
	if opts.MaxTitleRunes <= 0 {
		opts.MaxTitleRunes = defaults.MaxTitleRunes
	}
	if opts.MaxSummaryRunes <= 0 {
		opts.MaxSummaryRunes = defaults.MaxSummaryRunes
	}
	if opts.MaxReproRunes <= 0 {
		opts.MaxReproRunes = defaults.MaxReproRunes
	}
	if opts.MaxInstructionRunes <= 0 {
		opts.MaxInstructionRunes = defaults.MaxInstructionRunes
	}
	if opts.MaxInstructionCount <= 0 {
		opts.MaxInstructionCount = defaults.MaxInstructionCount
	}
	return opts
}

func normalizeCommunityKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "pr", "pull", "pull-request", "pull_request":
		return CommunityKindPullRequest
	default:
		return CommunityKindIssue
	}
}

func classifyCommunityText(item CommunityItem, title, summary string) (string, []string) {
	labels := strings.ToLower(strings.Join(item.Labels, " "))
	text := strings.ToLower(title + "\n" + summary + "\n" + item.Body + "\n" + labels)
	if containsAny(text, spamTerms) {
		return CommunityClassSpam, []string{"spam signal"}
	}
	if containsAny(labels, []string{"spam", "invalid"}) {
		return CommunityClassSpam, []string{"source label"}
	}
	bodyWords := len(strings.Fields(item.Body))
	if bodyWords < 6 && !containsAny(text, featureTerms) && !containsAny(text, bugTerms) {
		return CommunityClassNeedsInfo, []string{"too little detail"}
	}
	if containsAny(text, bugTerms) {
		reasons := []string{"bug signal"}
		if reproText(item.Body) == "" {
			reasons = append(reasons, "missing repro details")
		}
		return CommunityClassBug, reasons
	}
	if containsAny(text, featureTerms) || item.Kind == CommunityKindPullRequest {
		return CommunityClassFeature, []string{"feature/proposal signal"}
	}
	return CommunityClassNeedsInfo, []string{"no clear bug or feature signal"}
}

var (
	spamTerms = []string{
		"airdrop", "casino", "crypto", "forex", "free money", "giveaway",
		"loan offer", "nft", "onlyfans", "seo backlink", "telegram", "viagra",
		"whatsapp", "work from home",
	}
	bugTerms = []string{
		"actual result", "broken", "bug", "cannot", "crash", "error", "expected result",
		"fails", "failure", "panic", "regression", "reproduce", "stack trace", "traceback",
		"does not work", "is not working",
	}
	featureTerms = []string{
		"add support", "enhancement", "feature", "feature request", "proposal",
		"request", "rfc", "support for", "would like",
	}
	instructionTerms = []string{
		"api key", "assistant", "developer message", "export ", "ignore previous",
		"password", "prompt", "rm -rf", "run this", "secret", "system prompt",
		"token", "you are now",
	}
	reproLinePattern = regexp.MustCompile(`(?i)(^|\b)(actual|expected|error|fails?|repro|reproduce|steps?|traceback|stack trace)(\b|:)`)
)

func containsAny(text string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(text, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func communitySuggestedLabels(kind, classification string) []string {
	labels := []string{"community-intake"}
	switch classification {
	case CommunityClassBug:
		labels = append(labels, "bug")
	case CommunityClassFeature:
		labels = append(labels, "enhancement")
	case CommunityClassSpam:
		labels = append(labels, "spam")
	default:
		labels = append(labels, "needs-info")
	}
	if normalizeCommunityKind(kind) == CommunityKindPullRequest {
		labels = append(labels, "pull-request")
	}
	return cleanStrings(labels)
}

func communitySentiment(text string) string {
	lower := strings.ToLower(text)
	switch {
	case containsAny(lower, []string{"angry", "blocked", "frustrat", "terrible", "unusable", "urgent", "wtf"}):
		return "frustrated"
	case containsAny(lower, []string{"awesome", "great", "love", "thanks", "thank you"}):
		return "positive"
	default:
		return "neutral"
	}
}

func summaryText(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return "not provided"
	}
	for _, para := range strings.Split(body, "\n\n") {
		if cleaned := normalizeInline(para); cleaned != "" {
			return cleaned
		}
	}
	return normalizeInline(body)
}

func reproText(body string) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	out := make([]string, 0, 6)
	for _, line := range lines {
		cleaned := normalizeInline(strings.TrimLeft(line, "-*0123456789. )"))
		if cleaned == "" {
			continue
		}
		if reproLinePattern.MatchString(line) {
			out = append(out, cleaned)
		}
	}
	return strings.Join(out, " / ")
}

func instructionSnippets(body string, opts CommunityOptions) ([]string, bool) {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	out := make([]string, 0, opts.MaxInstructionCount)
	truncated := false
	for _, line := range lines {
		cleaned := normalizeInline(line)
		if cleaned == "" || !containsAny(strings.ToLower(cleaned), instructionTerms) {
			continue
		}
		snippet, wasTruncated := capRunes(cleaned, opts.MaxInstructionRunes)
		truncated = truncated || wasTruncated
		if len(out) >= opts.MaxInstructionCount {
			truncated = true
			continue
		}
		out = append(out, snippet)
	}
	return out, truncated
}

func normalizeInline(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func capRunes(s string, max int) (string, bool) {
	s = strings.TrimSpace(s)
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s, false
	}
	runes := []rune(s)
	if max <= 3 {
		return string(runes[:max]), true
	}
	return strings.TrimSpace(string(runes[:max-3])) + "...", true
}

func cleanStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
