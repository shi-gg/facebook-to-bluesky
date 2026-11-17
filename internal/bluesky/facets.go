package bluesky

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/bluesky-social/indigo/api/bsky"
)

var (
	linkPattern = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `]+`)
	tagPattern  = regexp.MustCompile(`#[\p{L}\p{N}_]+`)
)

func buildFacets(text string) []*bsky.RichtextFacet {
	if strings.TrimSpace(text) == "" {
		return nil
	}

	type match struct {
		start   int
		end     int
		feature *bsky.RichtextFacet_Features_Elem
	}

	matches := make([]match, 0, 4)
	linkRanges := make([][2]int, 0, 4)

	for _, idx := range linkPattern.FindAllStringIndex(text, -1) {
		raw := text[idx[0]:idx[1]]
		trimmed := strings.TrimRight(raw, ".,;:!?)]}'\"")
		if trimmed == "" {
			continue
		}

		parsed, err := url.Parse(trimmed)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			continue
		}

		matches = append(matches, match{
			start: idx[0],
			end:   idx[0] + len(trimmed),
			feature: &bsky.RichtextFacet_Features_Elem{
				RichtextFacet_Link: &bsky.RichtextFacet_Link{
					LexiconTypeID: "app.bsky.richtext.facet#link",
					Uri:           trimmed,
				},
			},
		})
		linkRanges = append(linkRanges, [2]int{idx[0], idx[0] + len(trimmed)})
	}

	for _, idx := range tagPattern.FindAllStringIndex(text, -1) {
		raw := text[idx[0]:idx[1]]
		tag := strings.TrimPrefix(raw, "#")
		if tag == "" || utf8.RuneCountInString(tag) > 64 {
			continue
		}
		if insideAny(idx[0], linkRanges) {
			continue
		}

		matches = append(matches, match{
			start: idx[0],
			end:   idx[1],
			feature: &bsky.RichtextFacet_Features_Elem{
				RichtextFacet_Tag: &bsky.RichtextFacet_Tag{
					LexiconTypeID: "app.bsky.richtext.facet#tag",
					Tag:           tag,
				},
			},
		})
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].start == matches[j].start {
			return matches[i].end > matches[j].end
		}
		return matches[i].start < matches[j].start
	})

	facets := make([]*bsky.RichtextFacet, 0, len(matches))
	lastEnd := -1

	for _, m := range matches {
		if m.start < lastEnd {
			continue
		}

		facets = append(facets, &bsky.RichtextFacet{
			Features: []*bsky.RichtextFacet_Features_Elem{m.feature},
			Index: &bsky.RichtextFacet_ByteSlice{
				ByteStart: int64(m.start),
				ByteEnd:   int64(m.end),
			},
		})
		lastEnd = m.end
	}

	return facets
}

func insideAny(pos int, ranges [][2]int) bool {
	for _, r := range ranges {
		if pos >= r[0] && pos < r[1] {
			return true
		}
	}
	return false
}
