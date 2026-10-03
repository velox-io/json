package corpus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// The payload of the GitHub REST issues benchmark: a page of 30 issues of a
// repository ( GET /repos/{owner}/{repo}/issues ). It is generated on first
// use with a fixed seed, instead of being a file of the repository: the keys,
// their order, the types and the nulls are the ones of the API response, and
// every run and every commit a benchmark compares sees the same bytes. The
// titles and the descriptions are markdown with lists, code and escapes.
//
// The decode types are the ones of the most used client,
// github.com/google/go-github: pointer fields, times through a type with
// UnmarshalJSON, and response keys which no field matches, as the API adds
// keys. The field order is an invariant of the benchmark: the decoder
// predicts the next key by the field after the last match, so the order of
// the fields must not change.

// GitHubTimestamp is a time of the REST API, as go-github decodes it: an RFC
// 3339 string or Unix seconds.
type GitHubTimestamp struct {
	time.Time
}

func (t *GitHubTimestamp) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] != '"' {
		var sec int64
		if err := json.Unmarshal(data, &sec); err != nil {
			return err
		}
		t.Time = time.Unix(sec, 0)
		return nil
	}
	var err error
	t.Time, err = time.Parse(`"`+time.RFC3339+`"`, string(data))
	return err
}

type GitHubUser struct {
	Login             *string `json:"login,omitempty"`
	ID                *int64  `json:"id,omitempty"`
	NodeID            *string `json:"node_id,omitempty"`
	AvatarURL         *string `json:"avatar_url,omitempty"`
	HTMLURL           *string `json:"html_url,omitempty"`
	GravatarID        *string `json:"gravatar_id,omitempty"`
	Type              *string `json:"type,omitempty"`
	SiteAdmin         *bool   `json:"site_admin,omitempty"`
	URL               *string `json:"url,omitempty"`
	EventsURL         *string `json:"events_url,omitempty"`
	FollowingURL      *string `json:"following_url,omitempty"`
	FollowersURL      *string `json:"followers_url,omitempty"`
	GistsURL          *string `json:"gists_url,omitempty"`
	OrganizationsURL  *string `json:"organizations_url,omitempty"`
	ReceivedEventsURL *string `json:"received_events_url,omitempty"`
	ReposURL          *string `json:"repos_url,omitempty"`
	StarredURL        *string `json:"starred_url,omitempty"`
	SubscriptionsURL  *string `json:"subscriptions_url,omitempty"`
}

type GitHubLabel struct {
	ID          *int64  `json:"id,omitempty"`
	URL         *string `json:"url,omitempty"`
	Name        *string `json:"name,omitempty"`
	Color       *string `json:"color,omitempty"`
	Description *string `json:"description,omitempty"`
	Default     *bool   `json:"default,omitempty"`
	NodeID      *string `json:"node_id,omitempty"`
}

type GitHubMilestone struct {
	URL          *string          `json:"url,omitempty"`
	HTMLURL      *string          `json:"html_url,omitempty"`
	LabelsURL    *string          `json:"labels_url,omitempty"`
	ID           *int64           `json:"id,omitempty"`
	Number       *int             `json:"number,omitempty"`
	State        *string          `json:"state,omitempty"`
	Title        *string          `json:"title,omitempty"`
	Description  *string          `json:"description,omitempty"`
	Creator      *GitHubUser      `json:"creator,omitempty"`
	OpenIssues   *int             `json:"open_issues,omitempty"`
	ClosedIssues *int             `json:"closed_issues,omitempty"`
	CreatedAt    *GitHubTimestamp `json:"created_at,omitempty"`
	UpdatedAt    *GitHubTimestamp `json:"updated_at,omitempty"`
	ClosedAt     *GitHubTimestamp `json:"closed_at,omitempty"`
	DueOn        *GitHubTimestamp `json:"due_on,omitempty"`
	NodeID       *string          `json:"node_id,omitempty"`
}

type GitHubPullRequestLinks struct {
	URL      *string          `json:"url,omitempty"`
	HTMLURL  *string          `json:"html_url,omitempty"`
	DiffURL  *string          `json:"diff_url,omitempty"`
	PatchURL *string          `json:"patch_url,omitempty"`
	MergedAt *GitHubTimestamp `json:"merged_at,omitempty"`
}

type GitHubReactions struct {
	TotalCount *int    `json:"total_count,omitempty"`
	PlusOne    *int    `json:"+1,omitempty"`
	MinusOne   *int    `json:"-1,omitempty"`
	Laugh      *int    `json:"laugh,omitempty"`
	Confused   *int    `json:"confused,omitempty"`
	Heart      *int    `json:"heart,omitempty"`
	Hooray     *int    `json:"hooray,omitempty"`
	Rocket     *int    `json:"rocket,omitempty"`
	Eyes       *int    `json:"eyes,omitempty"`
	URL        *string `json:"url,omitempty"`
}

type GitHubIssue struct {
	ID                *int64                  `json:"id,omitempty"`
	Number            *int                    `json:"number,omitempty"`
	State             *string                 `json:"state,omitempty"`
	StateReason       *string                 `json:"state_reason,omitempty"`
	Locked            *bool                   `json:"locked,omitempty"`
	Title             *string                 `json:"title,omitempty"`
	Body              *string                 `json:"body,omitempty"`
	AuthorAssociation *string                 `json:"author_association,omitempty"`
	User              *GitHubUser             `json:"user,omitempty"`
	Labels            []*GitHubLabel          `json:"labels,omitempty"`
	Assignee          *GitHubUser             `json:"assignee,omitempty"`
	Comments          *int                    `json:"comments,omitempty"`
	ClosedAt          *GitHubTimestamp        `json:"closed_at,omitempty"`
	CreatedAt         *GitHubTimestamp        `json:"created_at,omitempty"`
	UpdatedAt         *GitHubTimestamp        `json:"updated_at,omitempty"`
	ClosedBy          *GitHubUser             `json:"closed_by,omitempty"`
	URL               *string                 `json:"url,omitempty"`
	HTMLURL           *string                 `json:"html_url,omitempty"`
	CommentsURL       *string                 `json:"comments_url,omitempty"`
	EventsURL         *string                 `json:"events_url,omitempty"`
	LabelsURL         *string                 `json:"labels_url,omitempty"`
	RepositoryURL     *string                 `json:"repository_url,omitempty"`
	Milestone         *GitHubMilestone        `json:"milestone,omitempty"`
	PullRequestLinks  *GitHubPullRequestLinks `json:"pull_request,omitempty"`
	Reactions         *GitHubReactions        `json:"reactions,omitempty"`
	Assignees         []*GitHubUser           `json:"assignees,omitempty"`
	NodeID            *string                 `json:"node_id,omitempty"`
	Draft             *bool                   `json:"draft,omitempty"`
	ActiveLockReason  *string                 `json:"active_lock_reason,omitempty"`
}

// payloadRand is a generator of pseudo-random numbers ( SplitMix64 ) whose
// sequence is fixed by its seed.
type payloadRand struct {
	state uint64
}

func newPayloadRand(seed uint64) *payloadRand {
	return &payloadRand{state: seed}
}

func (r *payloadRand) next() uint64 {
	r.state += 0x9e3779b97f4a7c15
	z := r.state
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// intn returns a number in [0, n).
func (r *payloadRand) intn(n int) int {
	return int(r.next() % uint64(n))
}

// between returns a number in [min, max].
func (r *payloadRand) between(min, max int) int {
	return min + r.intn(max-min+1)
}

// chance is true once in n.
func (r *payloadRand) chance(n int) bool {
	return r.intn(n) == 0
}

func (r *payloadRand) pick(words []string) string {
	return words[r.intn(len(words))]
}

// token returns n characters of the alphabet, as the identifiers, the
// signatures and the cursors of the API.
func (r *payloadRand) token(alphabet string, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[r.intn(len(alphabet))]
	}
	return string(b)
}

const (
	alphanumeric = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	base64URL    = alphanumeric + "-_"
	hexDigits    = "0123456789abcdef"
)

// object is a JSON object whose keys are in their order, as the API writes
// them.
type object []field

type field struct {
	key   string
	value any
}

func (o object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, f := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(encodePayload(f.key))
		b.WriteByte(':')
		b.Write(encodePayload(f.value))
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// encodePayload encodes the value as the API does: '<', '>' and '&' are not
// escaped.
func encodePayload(v any) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		panic(err)
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

// timestamp is the time of the API, in RFC 3339 and UTC.
func timestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339)
}

// The words of the texts: the ones of the issues and the pull requests of a
// library, and of a coding agent.
var (
	nouns = strings.Fields(`decoder encoder struct field key value string number buffer cursor byte escape table
		hash map slice pointer type cache benchmark allocation loop word mask scan parser token stream reader
		writer error test case path option compiler function call register branch layout runtime memory copy
		length offset index array object literal rune quote backslash character input output result request
		response message tool content model session event delta chunk payload schema property interface method
		receiver generic release commit change regression fix issue report platform architecture version`)
	verbs = strings.Fields(`decodes encodes returns reads writes skips scans copies checks compares allocates
		reuses keeps handles escapes folds matches parses validates stores loads calls avoids measures reports
		breaks fixes adds removes moves splits merges replaces caches`)
	adjectives = strings.Fields(`short long plain escaped unknown nested empty large small first last next
		previous invalid valid exact loose fast slow hot cold new old same other whole`)
	smallWords = strings.Fields(`the a of to in is and for with by on at from as it that this when which not
		only each every more than then so if but or`)
	nonASCIIPunctuation = []string{"—", "→", "…", "✓", "×", "≤"}
)

// sentence returns a sentence of prose, which has code, numbers and
// references of issues now and then.
func sentence(r *payloadRand) string {
	n := r.between(6, 18)
	words := make([]string, 0, n)
	for i := 0; i < n; i++ {
		var w string
		switch k := r.intn(20); {
		case k < 7:
			w = r.pick(smallWords)
		case k < 12:
			w = r.pick(nouns)
		case k < 15:
			w = r.pick(verbs)
		case k < 17:
			w = r.pick(adjectives)
		case k == 17:
			w = "`" + identifier(r) + "`"
		case k == 18:
			w = fmt.Sprintf("%d", r.between(2, 4096))
		default:
			w = fmt.Sprintf("(#%d)", r.between(100, 659))
		}
		words = append(words, w)
	}
	if r.chance(4) {
		words[r.intn(n)] += ","
	}
	if r.chance(6) {
		i := r.intn(n)
		words[i] = `"` + words[i] + `"`
	}
	if r.chance(12) {
		words[r.intn(n)] += " " + r.pick(nonASCIIPunctuation)
	}
	s := strings.Join(words, " ")
	return strings.ToUpper(s[:1]) + s[1:] + "."
}

// prose returns paragraphs of about n bytes.
func prose(r *payloadRand, n int) string {
	var b strings.Builder
	for b.Len() < n {
		if b.Len() > 0 {
			if r.chance(3) {
				b.WriteString("\n\n")
			} else {
				b.WriteByte(' ')
			}
		}
		b.WriteString(sentence(r))
	}
	return b.String()
}

// markdownWithCode is markdown whose parts are blocks of code once in
// codeOneIn, as the ones of issues, pull requests and answers of a model:
// headings, lists with bold text, paragraphs and blocks of Go source.
func markdownWithCode(r *payloadRand, n, codeOneIn int) string {
	var b strings.Builder
	for b.Len() < n {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		if r.chance(codeOneIn) {
			b.WriteString("```go\n" + goSource(r, r.between(120, 600)) + "```")
			continue
		}
		switch k := r.intn(9); {
		case k < 2:
			b.WriteString("## " + strings.ToUpper(r.pick(nouns)[:1]) + r.pick(nouns)[1:] + " " + r.pick(nouns))
		case k < 6:
			for i, items := 0, r.between(2, 6); i < items; i++ {
				if i > 0 {
					b.WriteByte('\n')
				}
				if i > 0 && r.chance(3) {
					b.WriteString("  - " + sentence(r))
				} else {
					b.WriteString("- **" + sentence(r) + "** " + sentence(r))
				}
			}
		default:
			b.WriteString(prose(r, r.between(80, 400)))
		}
	}
	return b.String()
}

// identifier returns a name of Go code.
func identifier(r *payloadRand) string {
	s := r.pick(verbs)
	s = strings.TrimSuffix(s, "s") + strings.ToUpper(r.pick(nouns)[:1])
	return s + r.pick(nouns)[1:]
}

// goSource returns Go source code of about n bytes, whose indentation,
// strings and characters are the ones of the files which a coding agent
// reads: its escapes in JSON are its newlines, tabs, quotes and backslashes.
func goSource(r *payloadRand, n int) string {
	var b strings.Builder
	for b.Len() < n {
		name := identifier(r)
		fmt.Fprintf(&b, "// %s %s the %s %s of the %s.\n", name, r.pick(verbs), r.pick(adjectives), r.pick(nouns), r.pick(nouns))
		fmt.Fprintf(&b, "func (d *%sDecoder) %s(buf []byte, cursor int64) (int64, error) {\n", r.pick(nouns), name)
		depth := 1
		for i, lines := 0, r.between(4, 16); i < lines; i++ {
			indent := strings.Repeat("\t", depth)
			switch k := r.intn(9); k {
			case 0:
				fmt.Fprintf(&b, "%sfor %s < len(buf) {\n", indent, r.pick(nouns))
				depth++
			case 1:
				fmt.Fprintf(&b, "%sswitch buf[cursor] {\n%scase ' ', '\\n', '\\t', '\\r':\n", indent, indent)
				depth++
			case 2:
				fmt.Fprintf(&b, "%sreturn 0, errors.ErrSyntax(fmt.Sprintf(\"invalid character %%q in %s\", buf[cursor]), cursor)\n", indent, r.pick(nouns))
			case 3:
				fmt.Fprintf(&b, "%s%s := %s(buf[cursor:], %q)\n", indent, r.pick(nouns), identifier(r), r.pick(nouns))
			case 4:
				fmt.Fprintf(&b, "%sif %s == nil {\n", indent, r.pick(nouns))
				depth++
			default:
				fmt.Fprintf(&b, "%s%s = %s(%s, %d)\n", indent, r.pick(nouns), identifier(r), r.pick(nouns), r.between(0, 64))
			}
			if depth > 1 && r.chance(3) {
				depth--
				fmt.Fprintf(&b, "%s}\n", strings.Repeat("\t", depth))
			}
		}
		for depth > 1 {
			depth--
			fmt.Fprintf(&b, "%s}\n", strings.Repeat("\t", depth))
		}
		b.WriteString("\treturn cursor, nil\n}\n\n")
	}
	return b.String()
}

const (
	githubOwner = "example"
	githubRepo  = "json-codec"
	githubAPI   = "https://api.github.com"
	githubWeb   = "https://github.com"
)

var githubNow = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

type githubUserInfo struct {
	login string
	id    int64
}

var githubUsers = []githubUserInfo{{"maintainer", 209884}, {"contributor-one", 28623}, {"gopher42", 982358}, {"dependabot[bot]", 49699333}}

func githubRESTUser(u githubUserInfo) object {
	api := githubAPI + "/users/" + u.login
	typ := "User"
	if u.id == 49699333 {
		typ = "Bot"
	}
	return object{
		{"login", u.login},
		{"id", u.id},
		{"node_id", fmt.Sprintf("MDQ6VXNlcjE%d", u.id)},
		{"avatar_url", fmt.Sprintf("https://avatars.githubusercontent.com/u/%d?v=4", u.id)},
		{"gravatar_id", ""},
		{"url", api},
		{"html_url", githubWeb + "/" + u.login},
		{"followers_url", api + "/followers"},
		{"following_url", api + "/following{/other_user}"},
		{"gists_url", api + "/gists{/gist_id}"},
		{"starred_url", api + "/starred{/owner}{/repo}"},
		{"subscriptions_url", api + "/subscriptions"},
		{"organizations_url", api + "/orgs"},
		{"repos_url", api + "/repos"},
		{"events_url", api + "/events{/privacy}"},
		{"received_events_url", api + "/received_events"},
		{"type", typ},
		{"user_view_type", "public"},
		{"site_admin", false},
	}
}

var githubLabels = []struct{ name, color, description string }{
	{"bug", "d73a4a", "Something isn't working"},
	{"enhancement", "a2eeef", "New feature or request"},
	{"performance", "fbca04", "Faster encoding or decoding"},
	{"good first issue", "7057ff", "Good for newcomers"},
}

func githubRESTLabel(r *payloadRand, i int) object {
	l := githubLabels[i]
	return object{
		{"id", 1000000000 + int64(i)*7919},
		{"node_id", "LA_kwDOD" + r.token(base64URL, 14)},
		{"url", githubAPI + "/repos/" + githubOwner + "/" + githubRepo + "/labels/" + l.name},
		{"name", l.name},
		{"color", l.color},
		{"default", i < 2},
		{"description", l.description},
	}
}

// githubIssue is what the payload of an issue is made of.
type githubIssue struct {
	number                     int
	title, body                string
	author, closer             githubUserInfo
	pullRequest, closed        bool
	merged                     bool
	labels                     []int
	milestone                  bool
	comments                   int
	reactions                  [8]int
	created, updated, closedAt time.Time
}

func newGitHubIssues(r *payloadRand, bodyBytes, codeOneIn int) []githubIssue {
	issues := make([]githubIssue, 30)
	for i := range issues {
		is := &issues[i]
		is.number = 659 - i
		is.title = sentence(r)
		is.title = is.title[:len(is.title)-1]
		is.body = markdownWithCode(r, r.between(bodyBytes/10, bodyBytes*2), codeOneIn)
		is.author = githubUsers[r.intn(len(githubUsers))]
		is.closer = githubUsers[0]
		is.pullRequest = !r.chance(8)
		is.closed = !r.chance(6)
		is.merged = is.pullRequest && is.closed && !r.chance(5)
		for l := range githubLabels {
			if r.chance(4) {
				is.labels = append(is.labels, l)
			}
		}
		is.milestone = r.chance(5)
		is.comments = r.intn(4)
		for k := range is.reactions {
			if r.chance(6) {
				is.reactions[k] = r.between(1, 21)
			}
		}
		is.created = githubNow.Add(-time.Duration(r.between(3600, 90*86400)) * time.Second)
		is.updated = is.created.Add(time.Duration(r.between(60, 86400)) * time.Second)
		is.closedAt = is.updated
	}
	return issues
}

func githubRESTIssuesPayload() []byte {
	r := newPayloadRand(1)
	repo := githubAPI + "/repos/" + githubOwner + "/" + githubRepo
	var issues []any
	for _, is := range newGitHubIssues(r, 1600, 100) {
		url := fmt.Sprintf("%s/issues/%d", repo, is.number)
		kind := "issues"
		if is.pullRequest {
			kind = "pull"
		}
		labels := []any{}
		for _, l := range is.labels {
			labels = append(labels, githubRESTLabel(r, l))
		}
		var milestone any
		if is.milestone {
			milestone = object{
				{"url", repo + "/milestones/3"},
				{"html_url", githubWeb + "/" + githubOwner + "/" + githubRepo + "/milestone/3"},
				{"labels_url", repo + "/milestones/3/labels"},
				{"id", 11873412},
				{"node_id", "MI_kwDOD" + r.token(base64URL, 14)},
				{"number", 3},
				{"title", "v0.11.0"},
				{"description", sentence(r)},
				{"creator", githubRESTUser(githubUsers[0])},
				{"open_issues", 12},
				{"closed_issues", 41},
				{"state", "open"},
				{"created_at", timestamp(githubNow.AddDate(0, -4, 0))},
				{"updated_at", timestamp(githubNow.AddDate(0, 0, -2))},
				{"due_on", timestamp(githubNow.AddDate(0, 1, 0))},
				{"closed_at", nil},
			}
		}
		state, closedAt, closedBy, stateReason := "open", any(nil), any(nil), any(nil)
		if is.closed {
			state, closedAt, closedBy = "closed", timestamp(is.closedAt), githubRESTUser(is.closer)
			if !is.pullRequest {
				stateReason = "completed"
			}
		}
		o := object{
			{"url", url},
			{"repository_url", repo},
			{"labels_url", url + "/labels{/name}"},
			{"comments_url", url + "/comments"},
			{"events_url", url + "/events"},
			{"html_url", fmt.Sprintf("%s/%s/%s/%s/%d", githubWeb, githubOwner, githubRepo, kind, is.number)},
			{"id", 5523508452 + int64(is.number)*1537},
			{"node_id", "PR_kwDOD" + r.token(base64URL, 17)},
			{"number", is.number},
			{"title", is.title},
			{"user", githubRESTUser(is.author)},
			{"labels", labels},
			{"state", state},
			{"locked", false},
			{"assignees", []any{}},
			{"milestone", milestone},
			{"comments", is.comments},
			{"created_at", timestamp(is.created)},
			{"updated_at", timestamp(is.updated)},
			{"closed_at", closedAt},
			{"assignee", nil},
			{"author_association", "CONTRIBUTOR"},
			{"active_lock_reason", nil},
		}
		if is.pullRequest {
			var mergedAt any
			if is.merged {
				mergedAt = timestamp(is.closedAt)
			}
			pr := fmt.Sprintf("%s/%s/%s/pull/%d", githubWeb, githubOwner, githubRepo, is.number)
			o = append(o,
				field{"draft", false},
				field{"pull_request", object{
					{"url", fmt.Sprintf("%s/pulls/%d", repo, is.number)},
					{"html_url", pr},
					{"diff_url", pr + ".diff"},
					{"patch_url", pr + ".patch"},
					{"merged_at", mergedAt},
				}},
			)
		}
		total := 0
		for _, n := range is.reactions {
			total += n
		}
		o = append(o,
			field{"body", is.body},
			field{"closed_by", closedBy},
			field{"reactions", object{
				{"url", url + "/reactions"},
				{"total_count", total},
				{"+1", is.reactions[0]},
				{"-1", is.reactions[1]},
				{"laugh", is.reactions[2]},
				{"hooray", is.reactions[3]},
				{"confused", is.reactions[4]},
				{"heart", is.reactions[5]},
				{"rocket", is.reactions[6]},
				{"eyes", is.reactions[7]},
			}},
			field{"timeline_url", url + "/timeline"},
			field{"performed_via_github_app", nil},
			field{"state_reason", stateReason},
		)
		if !is.pullRequest {
			o = append(o,
				field{"sub_issues_summary", object{{"total", 0}, {"completed", 0}, {"percent_completed", 0}}},
				field{"issue_dependencies_summary", object{{"blocked_by", 0}, {"total_blocked_by", 0}, {"blocking", 0}, {"total_blocking", 0}}},
				field{"pinned_comment", nil},
			)
		}
		issues = append(issues, o)
	}
	return encodePayload(issues)
}

var (
	githubIssuesOnce sync.Once
	githubIssuesData []byte
)

// GitHubIssuesJSON is the compact JSON of a page of 30 issues of the GitHub
// REST API: mostly pull requests with markdown descriptions, some with
// labels, a milestone or reactions. The keys are in the order of the
// response.
func GitHubIssuesJSON() []byte {
	githubIssuesOnce.Do(func() { githubIssuesData = githubRESTIssuesPayload() })
	return githubIssuesData
}
