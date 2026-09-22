package groups

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// ScrapeGroupTrends paginates through a group's feed and produces a TrendReport.
// It respects the client's MinRequestGap between pages. If ctx is cancelled
// mid-scrape, a partial report is returned alongside ErrPartialResult.
//
// Default options: 200 posts, top-20 keywords, top-20 hashtags.
func (c *Client) ScrapeGroupTrends(ctx context.Context, groupID string, opts ...TrendOption) (*TrendReport, error) {
	if groupID == "" {
		return nil, fmt.Errorf("%w: groupID must not be empty", ErrInvalidParams)
	}

	to := &trendOptions{maxPosts: 200, topN: 20}
	for _, o := range opts {
		o(to)
	}

	stopSet := buildStopSet(to.stopWords)

	report := &TrendReport{GroupID: groupID}

	// Per-hour post counts for peak-hour analysis.
	hourCounts := make(map[int]int, 24)
	// Author post counts.
	authorCounts := make(map[string]*AuthorActivity)
	// Term frequencies.
	termFreq := make(map[string]int)
	hashFreq := make(map[string]int)
	// Engagement totals.
	var totalEngagement float64

	page, err := c.GetGroupFeed(ctx, groupID)
	if err != nil {
		return nil, err
	}

	var partial bool

	for {
		for _, p := range page.Posts {
			if report.PostsAnalyzed >= to.maxPosts {
				partial = false // hit cap cleanly
				goto done
			}
			report.PostsAnalyzed++
			totalEngagement += float64(p.ReactionCount + p.CommentCount + p.ShareCount)

			if !p.CreatedAt.IsZero() {
				hourCounts[p.CreatedAt.UTC().Hour()]++
			}

			if p.AuthorID != "" {
				if a, ok := authorCounts[p.AuthorID]; ok {
					a.PostCount++
				} else {
					authorCounts[p.AuthorID] = &AuthorActivity{
						AuthorID:   p.AuthorID,
						AuthorName: p.AuthorName,
						PostCount:  1,
					}
				}
			}

			extractTerms(p.Message, stopSet, termFreq, hashFreq)
		}

		if !page.HasNext || page.NextCursor == "" {
			break
		}

		select {
		case <-ctx.Done():
			partial = true
			goto done
		default:
		}

		page, err = c.GetGroupFeedPage(ctx, groupID, page.NextCursor)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				partial = true
				goto done
			}
			return nil, err
		}
	}

done:
	if report.PostsAnalyzed > 0 {
		report.AvgEngagement = totalEngagement / float64(report.PostsAnalyzed)
	}

	report.TopKeywords = topN(termFreq, to.topN)
	report.TopHashtags = topN(hashFreq, to.topN)
	report.PeakHours = peakHours(hourCounts)
	report.ActiveAuthors = topAuthors(authorCounts, 10)
	report.SentimentScore = naiveSentiment(termFreq)

	if partial {
		return report, fmt.Errorf("%w after %d posts", ErrPartialResult, report.PostsAnalyzed)
	}
	return report, nil
}

// ---------------------------------------------------------------------------
// Text analysis helpers
// ---------------------------------------------------------------------------

var (
	reHashtag = regexp.MustCompile(`#([A-Za-z]\w*)`)
	reWord    = regexp.MustCompile(`[A-Za-z]{3,}`)
)

func extractTerms(text string, stopSet map[string]struct{}, terms, hashes map[string]int) {
	// Hashtags first.
	for _, m := range reHashtag.FindAllStringSubmatch(text, -1) {
		tag := strings.ToLower(m[1])
		hashes[tag]++
	}

	// Strip hashtags before word extraction.
	clean := reHashtag.ReplaceAllString(text, " ")

	words := make([]string, 0, 16)
	for _, w := range reWord.FindAllString(clean, -1) {
		lw := strings.ToLower(w)
		if _, stop := stopSet[lw]; stop {
			continue
		}
		if !isPunctuation(lw) {
			words = append(words, lw)
		}
	}

	// Unigrams.
	for _, w := range words {
		terms[w]++
	}
	// Bigrams.
	for i := 0; i+1 < len(words); i++ {
		bi := words[i] + " " + words[i+1]
		terms[bi]++
	}
}

func isPunctuation(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

func topN(freq map[string]int, n int) []KeywordFreq {
	type kv struct {
		k string
		v int
	}
	all := make([]kv, 0, len(freq))
	for k, v := range freq {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].v != all[j].v {
			return all[i].v > all[j].v
		}
		return all[i].k < all[j].k
	})
	if n > len(all) {
		n = len(all)
	}
	out := make([]KeywordFreq, n)
	for i := 0; i < n; i++ {
		out[i] = KeywordFreq{Term: all[i].k, Count: all[i].v}
	}
	return out
}

func peakHours(counts map[int]int) []int {
	type hv struct {
		h int
		v int
	}
	all := make([]hv, 0, len(counts))
	for h, v := range counts {
		all = append(all, hv{h, v})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].v != all[j].v {
			return all[i].v > all[j].v
		}
		return all[i].h < all[j].h
	})
	out := make([]int, len(all))
	for i, x := range all {
		out[i] = x.h
	}
	return out
}

func topAuthors(m map[string]*AuthorActivity, n int) []AuthorActivity {
	all := make([]AuthorActivity, 0, len(m))
	for _, a := range m {
		all = append(all, *a)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].PostCount != all[j].PostCount {
			return all[i].PostCount > all[j].PostCount
		}
		return all[i].AuthorName < all[j].AuthorName
	})
	if n > len(all) {
		n = len(all)
	}
	return all[:n]
}

// naiveSentiment scores the post term frequency using simple positive/negative
// word lists. Score is the difference normalised to [-1, 1].
func naiveSentiment(freq map[string]int) float64 {
	var pos, neg int
	for w, c := range freq {
		if positiveWords[w] {
			pos += c
		}
		if negativeWords[w] {
			neg += c
		}
	}
	total := pos + neg
	if total == 0 {
		return 0
	}
	return math.Round(float64(pos-neg)/float64(total)*100) / 100
}

// buildStopSet merges the bundled English stop list with caller-supplied extras.
func buildStopSet(extra []string) map[string]struct{} {
	set := make(map[string]struct{}, len(englishStopWords)+len(extra))
	for _, w := range englishStopWords {
		set[w] = struct{}{}
	}
	for _, w := range extra {
		set[strings.ToLower(w)] = struct{}{}
	}
	return set
}

// ---------------------------------------------------------------------------
// Bundled English stop-word list (~200 words)
// ---------------------------------------------------------------------------

var englishStopWords = []string{
	"a", "about", "above", "after", "again", "against", "ago", "all", "also",
	"am", "an", "and", "any", "are", "aren", "as", "at",
	"be", "because", "been", "before", "being", "below", "between", "both",
	"but", "by",
	"can", "cannot", "could", "couldn",
	"did", "didn", "do", "does", "doesn", "doing", "don", "done", "down",
	"during",
	"each", "either", "else", "ever", "every",
	"few", "for", "from", "further",
	"get", "got",
	"had", "hadn", "has", "hasn", "have", "haven", "having", "he", "her",
	"here", "hers", "herself", "him", "himself", "his", "how",
	"i", "if", "in", "into", "is", "isn", "it", "its", "itself",
	"just",
	"know",
	"let", "like",
	"ma", "me", "might", "mightn", "more", "most", "must", "mustn", "my",
	"myself",
	"needn", "no", "nor", "not", "now",
	"of", "off", "on", "once", "only", "or", "other", "our", "ours",
	"ourselves", "out", "over", "own",
	"re", "really",
	"s", "same", "shan", "she", "should", "shouldn", "so", "some", "such",
	"t", "than", "that", "the", "their", "theirs", "them", "themselves",
	"then", "there", "these", "they", "this", "those", "through", "to",
	"too",
	"under", "until", "up",
	"us",
	"ve", "very",
	"was", "wasn", "we", "were", "weren", "what", "when", "where", "which",
	"while", "who", "whom", "why", "will", "with", "won", "would", "wouldn",
	"y", "you", "your", "yours", "yourself", "yourselves",
}

// ---------------------------------------------------------------------------
// Naive sentiment word lists
// ---------------------------------------------------------------------------

var positiveWords = toSet([]string{
	"great", "good", "love", "amazing", "awesome", "excellent", "fantastic",
	"wonderful", "best", "happy", "glad", "excited", "brilliant", "perfect",
	"helpful", "thanks", "thank", "beautiful", "incredible", "outstanding",
	"positive", "success", "recommend", "win", "winning", "benefit",
	"improve", "growth", "opportunity", "gain",
})

var negativeWords = toSet([]string{
	"bad", "terrible", "awful", "horrible", "hate", "poor", "worst",
	"disappointing", "sad", "angry", "frustrated", "broken", "wrong",
	"fail", "failure", "problem", "issue", "bug", "error", "crash",
	"scam", "spam", "fake", "misleading", "danger", "risk", "loss",
	"decline", "drop", "worse", "warning", "avoid",
})

func toSet(words []string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}
