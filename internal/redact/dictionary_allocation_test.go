package redact

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

func TestDictionaryExactNodeAllocation(t *testing.T) {
	cases := [][]string{nil, {"é", "Ω"}, {"a", "a", "a"}, {"abc", "bc", "c", "xbc"}, {"密碼🔑", "碼🔑", "🔑", "x🔑"}, {" a,b ", "b ", " "}}
	r := rand.New(rand.NewSource(314))
	for trial := 0; trial < 100; trial++ {
		words := make([]string, 30)
		for i := range words {
			b := make([]byte, 1+r.Intn(40))
			for j := range b {
				b[j] = 'a' + byte(r.Intn(4))
			}
			words[i] = string(b)
		}
		cases = append(cases, words)
	}
	for _, words := range cases {
		original := append([]string(nil), words...)
		d, err := CompileDictionary(words)
		if err != nil {
			t.Fatal(err)
		}
		if cap(d.nodes) != len(d.nodes) {
			t.Fatalf("node capacity %d exceeds exact count %d", cap(d.nodes), len(d.nodes))
		}
		if !reflect.DeepEqual(words, original) {
			t.Fatal("compilation mutated caller order")
		}
		suffixes := map[string]bool{"": true}
		for _, word := range words {
			for i := range len(word) {
				suffixes[word[i:]] = true
			}
		}
		if counted := dictionaryNodeCount(words); counted != len(suffixes) {
			t.Fatalf("predicted node count=%d suffix oracle=%d", counted, len(suffixes))
		}
		if len(d.nodes) != len(suffixes) {
			t.Fatalf("node count=%d suffix oracle=%d", len(d.nodes), len(suffixes))
		}
	}
}

func TestDictionarySharedSuffixAllocation(t *testing.T) {
	suffix := strings.Repeat("x", 1024)
	words := make([]string, 1000)
	for i := range words {
		words[i] = fmt.Sprintf("%04d", i) + suffix
	}
	d, err := CompileDictionary(words)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.nodes) > 6000 || cap(d.nodes) != len(d.nodes) {
		t.Fatalf("shared suffix retained nodes=%d capacity=%d", len(d.nodes), cap(d.nodes))
	}
	for _, i := range []int{0, 499, 999} {
		if got := d.Spans(words[i]); !reflect.DeepEqual(got, [][2]int{{0, len(words[i])}}) {
			t.Fatal("shared suffix match changed")
		}
	}
}

func TestDictionaryAllocationKeepsHeldGeneration(t *testing.T) {
	old, err := CompileDictionary([]string{"old-secret", "secret", "密碼🔑"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan bool, 1)
	go func() {
		good := true
		for i := 0; i < 1000; i++ {
			good = good && reflect.DeepEqual(old.Spans("old-secret"), [][2]int{{0, 10}, {4, 10}})
		}
		done <- good
	}()
	current, err := CompileDictionary([]string{"new-token", "token", "新密碼🔑"})
	readersOK := <-done
	if err != nil {
		t.Fatal(err)
	}
	if !readersOK || len(current.Spans("old-secret")) != 0 || len(old.Spans("new-token")) != 0 {
		t.Fatal("generation isolation changed")
	}
	if !reflect.DeepEqual(current.Spans("new-token"), [][2]int{{0, 9}, {4, 9}}) {
		t.Fatal("longest/new generation matching changed")
	}
}
