//go:build !next

package update

import "context"

const nextDistribution = false

// Site is magpie's home; its /api/latest is the upstream update feed.
const Site = "https://usemagpie.ai"
const nextFeed = ""

func latestNext(context.Context, string) (*Release, error)              { panic("not a Next build") }
func nextNotes(context.Context, string, string, string) ([]Note, error) { panic("not a Next build") }
