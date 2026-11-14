package redact

import "sort"

// dictionaryExceptions subtracts the union of exact allowed occurrences from
// detected spans. Dropping an entire overlapping detection would also release
// an adjacent secret that the user never allowed.
func dictionaryExceptions(s string, found []span, o Options) []span {
	if len(found) == 0 || o.AllowDictionary == nil {
		return found
	}
	allowed := o.AllowDictionary.Spans(s)
	o.reserveDictionaryExceptions(len(allowed))
	if len(allowed) == 0 {
		return found
	}
	merged := allowed[:0]
	for _, p := range allowed {
		if n := len(merged); n != 0 && p[0] <= merged[n-1][1] {
			merged[n-1][1] = max(merged[n-1][1], p[1])
		} else {
			merged = append(merged, p)
		}
	}
	var out []span
	appendPart := func(f span, start, end int) {
		if start < end {
			f.start, f.end = start, end
			out = append(out, f)
			// A long detection split by many exceptions is still bounded.
			o.reserveDictionaryExceptions(1)
		}
	}
	for _, f := range found {
		start := f.start
		i := sort.Search(len(merged), func(i int) bool { return merged[i][1] > start })
		if f.bare {
			if i < len(merged) && merged[i][0] <= f.start && merged[i][1] >= f.end {
				continue
			}
			// A bare JSON value needs one enclosing pair of quotes, even
			// when an exception splits its masked content into several parts.
			out = append(out, f)
			o.reserveDictionaryExceptions(1)
			continue
		}
		for ; i < len(merged) && merged[i][0] < f.end; i++ {
			appendPart(f, start, min(f.end, merged[i][0]))
			start = max(start, merged[i][1])
		}
		appendPart(f, start, f.end)
	}
	return out
}
