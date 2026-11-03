package redact

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

func TestDictionaryLiteralSpans(t *testing.T) {
	for _, tc := range []struct {
		words []string
		text  string
		want  [][2]int
	}{
		{[]string{"ab", "abc", "bc", "c"}, "zabc", [][2]int{{1, 4}, {2, 4}, {3, 4}}},
		{[]string{"bcde", "de"}, "abcde", [][2]int{{1, 5}, {3, 5}}},
		{[]string{"密碼🔑", "a,b", " x ", "ABC", "ABC"}, "密碼🔑 a,b x abc ABC", [][2]int{{0, 10}, {11, 14}, {14, 17}, {21, 24}}},
		{[]string{"2026-11-02"}, "2026/11/02", nil},
		{nil, "abc", nil},
	} {
		d, err := CompileDictionary(tc.words)
		if err != nil {
			t.Fatal(err)
		}
		if got := d.Spans(tc.text); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("spans=%v want=%v", got, tc.want)
		}
	}
	var d *Dictionary
	if got := d.Spans("abc"); len(got) != 0 {
		t.Fatal("nil matcher matched")
	}
}

func TestDictionaryMatchesReference(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for trial := 0; trial < 100; trial++ {
		words := make([]string, 30)
		for i := range words {
			var b strings.Builder
			for n := r.Intn(8) + 1; n > 0; n-- {
				b.WriteByte(byte('a' + r.Intn(3)))
			}
			words[i] = b.String()
		}
		var b strings.Builder
		for i := 0; i < 100; i++ {
			b.WriteByte(byte('a' + r.Intn(3)))
		}
		text := b.String()
		d, err := CompileDictionary(words)
		if err != nil {
			t.Fatal(err)
		}
		var want [][2]int
		for i := range text {
			longest := 0
			for _, w := range words {
				if strings.HasPrefix(text[i:], w) && len(w) > longest {
					longest = len(w)
				}
			}
			if longest > 0 {
				want = append(want, [2]int{i, i + longest})
			}
		}
		if got := d.Spans(text); !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d mismatch: %v != %v", trial, got, want)
		}
	}
}

func TestDictionaryLimitsAndPrivacy(t *testing.T) {
	for _, words := range [][]string{{""}, {string([]byte{0xff})}, {strings.Repeat("x", MaxDictionaryBytes+1)}, make([]string, MaxDictionaryWords+1)} {
		if _, err := CompileDictionary(words); err == nil {
			t.Fatal("invalid dictionary accepted")
		}
	}
	secret := "synthetic-do-not-echo"
	_, err := CompileDictionary([]string{secret, ""})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal("invalid entry error leaked dictionary")
	}
}

func TestDictionaryLargeAndLong(t *testing.T) {
	words := make([]string, MaxDictionaryWords)
	for i := range words {
		words[i] = fmt.Sprintf("synthetic-secret-%08d", i)
	}
	words[50000] = strings.Repeat("long-", 14000)
	d, err := CompileDictionary(words)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("100k synthetic generation: nodes=%d nodeBytes=%d retainedArrayBytes=%d", len(d.nodes), unsafe.Sizeof(dictionaryNode{}), uintptr(cap(d.nodes))*unsafe.Sizeof(dictionaryNode{}))
	for _, index := range []int{0, 50000, 99999} {
		text := "before " + words[index] + " after"
		got := d.Spans(text)
		if len(got) != 1 || got[0] != [2]int{7, 7 + len(words[index])} {
			t.Fatalf("entry %d did not match exactly", index)
		}
	}
	// Caller mutations do not alter the compiled generation.
	words[0] = "different"
	if len(d.Spans("synthetic-secret-00000000")) != 1 {
		t.Fatal("matcher retained caller slice")
	}
}

func BenchmarkDictionaryCompiled(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			words := make([]string, n)
			for i := range words {
				words[i] = fmt.Sprintf("synthetic-secret-%08d", i)
			}
			d, err := CompileDictionary(words)
			if err != nil {
				b.Fatal(err)
			}
			text := strings.Repeat("ordinary text without a match. ", 2048)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				d.Spans(text)
			}
		})
	}
}

func TestDictionaryConcurrentReaders(t *testing.T) {
	d, err := CompileDictionary([]string{"abc", "bcde", "de", "密碼"})
	if err != nil {
		t.Fatal(err)
	}
	want := d.Spans("abcde 密碼")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				if !reflect.DeepEqual(d.Spans("abcde 密碼"), want) {
					t.Error("immutable generation changed")
				}
			}
		}()
	}
	wg.Wait()
}
