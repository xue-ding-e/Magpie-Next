package redact

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"unicode/utf8"
)

// Dictionary limits count input entries and UTF-8 bytes, before deduplication.
// They bound compilation too: there is at most one trie node per input byte.
const (
	MaxDictionaryWords       = 100000
	MaxDictionaryBytes       = 16 << 20
	MaxDictionaryTextBytes   = 32 << 20
	MaxDictionaryMatches     = 4096
	MaxDictionaryGrowthBytes = 128 << 10
)

// Dictionary is an immutable exact-literal matcher. Compile once per saved
// generation and share it between requests. It does not apply the legacy
// custom words' trimming, case folding, whole-word or date equivalence rules.
type Dictionary struct{ nodes []dictionaryNode }

// A node is also its incoming edge. Siblings are sparse linked edges, so a
// long password does not allocate a map (or 256 transitions) for every byte.
// Index zero is both the root and the absent-edge sentinel.
type dictionaryNode struct {
	first, next, fail, longest uint32
	label                      byte
}

// CompileDictionary checks the full input before compiling it. Errors contain
// counts only, never words. Duplicate entries share a terminal and exact spaces
// are meaningful. An empty dictionary is valid; an empty entry is not.
func CompileDictionary(words []string) (*Dictionary, error) {
	if len(words) > MaxDictionaryWords {
		return nil, fmt.Errorf("dictionary exceeds %d entries", MaxDictionaryWords)
	}
	size := 0
	for _, word := range words {
		if word == "" || !utf8.ValidString(word) {
			return nil, fmt.Errorf("dictionary entries must be nonempty UTF-8 text")
		}
		if len(word) > MaxDictionaryBytes-size {
			return nil, fmt.Errorf("dictionary exceeds %d UTF-8 bytes", MaxDictionaryBytes)
		}
		size += len(word)
	}
	d := &Dictionary{nodes: make([]dictionaryNode, 1, dictionaryNodeCount(words))}
	for _, word := range words {
		at := uint32(0)
		// Reverse byte order deliberately, not rune order: offsets remain those
		// of the caller's original UTF-8 text, including multi-byte passwords.
		for i := len(word) - 1; i >= 0; i-- {
			to := d.edge(at, word[i])
			if to == 0 {
				to = uint32(len(d.nodes))
				d.nodes = append(d.nodes, dictionaryNode{label: word[i], next: d.nodes[at].first})
				d.nodes[at].first = to
			}
			at = to
		}
		d.nodes[at].longest = uint32(len(word))
	}
	queue := make([]uint32, 0, len(d.nodes))
	for child := d.nodes[0].first; child != 0; child = d.nodes[child].next {
		queue = append(queue, child)
	}
	for head := 0; head < len(queue); head++ {
		at := queue[head]
		for child := d.nodes[at].first; child != 0; child = d.nodes[child].next {
			failure := d.nodes[at].fail
			for failure != 0 && d.edge(failure, d.nodes[child].label) == 0 {
				failure = d.nodes[failure].fail
			}
			d.nodes[child].fail = d.edge(failure, d.nodes[child].label)
			inherited := d.nodes[d.nodes[child].fail].longest
			if inherited > d.nodes[child].longest {
				d.nodes[child].longest = inherited
			}
			queue = append(queue, child)
		}
	}
	return d, nil
}

// dictionaryNodeCount counts distinct reversed-byte prefixes without constructing
// a second trie. Equal suffixes are adjacent in reversed lexicographic order.
// Sorting only a slice copy preserves caller order and immutable generations.
// Call only after the entry and byte budgets have been validated.
func dictionaryNodeCount(words []string) int {
	ordered := slices.Clone(words)
	slices.SortFunc(ordered, func(a, b string) int {
		for i, j := len(a)-1, len(b)-1; i >= 0 && j >= 0; i, j = i-1, j-1 {
			if a[i] != b[j] {
				return int(a[i]) - int(b[j])
			}
		}
		return len(a) - len(b)
	})
	count := 1
	previous := ""
	for _, word := range ordered {
		shared := 0
		for shared < len(previous) && shared < len(word) && previous[len(previous)-1-shared] == word[len(word)-1-shared] {
			shared++
		}
		count += len(word) - shared
		previous = word
	}
	return count
}

func (d *Dictionary) edge(at uint32, label byte) uint32 {
	for child := d.nodes[at].first; child != 0; child = d.nodes[child].next {
		if d.nodes[child].label == label {
			return child
		}
	}
	return 0
}

// Spans returns byte ranges, earliest start first and longest at the same
// start. Different starts can overlap: the caller combines these with its other
// masking rules before choosing the earliest, longest non-overlapping spans.
// Scanning backwards lets each node keep just its longest output, rather than a potentially quadratic list of suffix matches. At each
// original start the longest literal is enough for the forward greedy choice.
// Work is independent of the number of dictionary entries after compilation;
// a transition examines at most 256 sparse byte edges. Nil matches nothing.
// Exceeding the input/candidate budget panics with ErrDictionaryBudget; callers
// must use the gateway fail-closed boundary, never recover and send raw input.
func (d *Dictionary) Spans(text string) [][2]int {
	if d == nil || len(d.nodes) < 2 || len(text) == 0 {
		return nil
	}
	if len(text) > MaxDictionaryTextBytes {
		panic(ErrDictionaryBudget)
	}
	var spans [][2]int
	at := uint32(0)
	for i := len(text) - 1; i >= 0; i-- {
		to := d.edge(at, text[i])
		for to == 0 && at != 0 {
			at = d.nodes[at].fail
			to = d.edge(at, text[i])
		}
		at = to
		if length := d.nodes[at].longest; length != 0 {
			if len(spans) >= MaxDictionaryMatches {
				panic(ErrDictionaryBudget)
			}
			spans = append(spans, [2]int{i, i + int(length)})
		}
	}
	slices.Reverse(spans)
	return spans
}

// ErrDictionaryBudget contains no dictionary text. The gateway's gwmw.Safely
// converts this panic to a failed request, never a partially masked send.
var ErrDictionaryBudget = errors.New("local dictionary protection limit exceeded; request was not sent")

type dictionaryBudget struct {
	sync.Mutex
	input, matches, growth int
}

func dictionaryOptions(o Options, size int) Options {
	if o.Dictionary == nil || len(o.Dictionary.nodes) < 2 {
		return o
	}
	if size > MaxDictionaryTextBytes {
		panic(ErrDictionaryBudget)
	}
	if o.dictionaryBudget == nil {
		o.dictionaryBudget = &dictionaryBudget{}
	}
	o.reserveDictionaryInput(size)
	return o
}
func (o Options) reserveDictionary(matches int) {
	if o.dictionaryBudget == nil {
		return
	}
	b := o.dictionaryBudget
	b.Lock()
	defer b.Unlock()
	b.matches += matches
	// A LOCAL_TERM placeholder is fixed width, independent of password length.
	b.growth += matches * (len("{{LOCAL_TERM_") + 8 + len("}}"))
	if b.matches > MaxDictionaryMatches || b.growth > MaxDictionaryGrowthBytes {
		panic(ErrDictionaryBudget)
	}
}
func (o Options) checkDictionaryOutput(size int) {
	if o.dictionaryBudget != nil && size > MaxDictionaryGrowthBytes {
		panic(ErrDictionaryBudget)
	}
}

// Signed replay may contain a restored local literal after the original user
// message was compacted away. Reconstitute only a placeholder this process
// actually generated and still knows, and only while the literal is active.
// Unknown provenance fails closed rather than rewriting signed text arbitrarily.
func maskSignedDictionary(s string, o Options) (string, int) {
	o.reserveDictionaryInput(len(s))
	spans := o.Dictionary.Spans(s)
	o.reserveDictionary(len(spans))
	var found []span
	placeholders := placeholderRe.FindAllStringIndex(s, -1)
	next := 0
	for _, p := range spans {
		if p[0] < next {
			continue
		}
		protected := false
		for _, q := range placeholders {
			if p[0] < q[1] && p[1] > q[0] {
				if _, ok := Value(s[q[0]:q[1]]); ok {
					protected = true
					break
				}
			}
		}
		if protected {
			continue
		}
		value := s[p[0]:p[1]]
		tag := placeholderTag("LOCAL_TERM", value)
		if old, ok := Value(tag); !ok || old != value {
			panic(ErrDictionarySigned)
		}
		found = append(found, span{p[0], p[1], "LOCAL_TERM", ByWord})
		next = p[1]
	}
	return apply(s, found, placeholder, o.Seen)
}

var ErrDictionarySigned = errors.New("local dictionary cannot safely restore signed content; request was not sent")

// DictionaryRequest fixes one budget across an outbound request assembled from
// multiple fields. Ordinary Mask callers still get an independent budget.
func DictionaryRequest(o Options) Options {
	if o.Dictionary != nil && len(o.Dictionary.nodes) > 1 {
		o.dictionaryBudget = &dictionaryBudget{}
	}
	return o
}

func (o Options) reserveDictionaryInput(size int) {
	if o.dictionaryBudget == nil {
		return
	}
	b := o.dictionaryBudget
	b.Lock()
	defer b.Unlock()
	b.input += size
	if b.input > MaxDictionaryTextBytes {
		panic(ErrDictionaryBudget)
	}
}
