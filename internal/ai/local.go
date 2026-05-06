package ai

import (
	"context"
	"hash/fnv"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

type LocalProvider struct {
	dim int
}

func NewLocalProvider(dim int) *LocalProvider {
	if dim <= 0 {
		dim = 384
	}
	return &LocalProvider{dim: dim}
}

func (p *LocalProvider) Rewrite(ctx context.Context, query string) (string, error) {
	return strings.Join(tokenize(query), " "), nil
}

func (p *LocalProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = p.embed(t)
	}
	return out, nil
}

func (p *LocalProvider) Rerank(ctx context.Context, query string, docs []string) ([]float64, error) {
	queryTokens := tokenize(query)
	querySet := makeSet(queryTokens)
	queryBigrams := ngrams(queryTokens, 2)
	scores := make([]float64, len(docs))
	for i, doc := range docs {
		docTokens := tokenize(doc)
		if len(docTokens) == 0 || len(queryTokens) == 0 {
			continue
		}
		docSet := makeSet(docTokens)
		coverage := overlapRatio(querySet, docSet)
		bigramCoverage := overlapRatio(makeSet(queryBigrams), makeSet(ngrams(docTokens, 2)))
		phraseBoost := exactPhraseScore(queryTokens, strings.Join(docTokens, " "))
		proximity := proximityScore(queryTokens, docTokens)
		lengthPenalty := 1.0
		if len(docTokens) > 260 {
			lengthPenalty = 0.92
		}
		score := (0.42*coverage + 0.24*bigramCoverage + 0.22*phraseBoost + 0.12*proximity) * lengthPenalty
		if score > 1 {
			score = 1
		}
		scores[i] = score
	}
	return scores, nil
}

func (p *LocalProvider) embed(text string) []float32 {
	vec := make([]float32, p.dim)
	tokens := tokenize(text)
	features := make([]weightedFeature, 0, len(tokens)*3)
	for i, tok := range tokens {
		if tok == "" {
			continue
		}
		features = append(features, weightedFeature{"w:" + tok, 1.0})
		if i+1 < len(tokens) {
			features = append(features, weightedFeature{"b:" + tok + "_" + tokens[i+1], 0.85})
		}
		if len([]rune(tok)) >= 5 {
			for _, gram := range charNgrams(tok, 3) {
				features = append(features, weightedFeature{"c:" + gram, 0.25})
			}
		}
	}
	for _, f := range features {
		idx := hashIndex(f.Value, p.dim)
		vec[idx] += float32(f.Weight)
	}
	for i, v := range vec {
		if v > 0 {
			vec[i] = float32(math.Sqrt(float64(v)))
		}
	}
	l2Normalize(vec)
	return vec
}

type weightedFeature struct {
	Value  string
	Weight float64
}

var tokenRe = regexp.MustCompile(`[a-z0-9]+(?:[./&-][a-z0-9]+)*`)

func tokenize(text string) []string {
	text = strings.ToLower(strings.TrimSpace(text))
	text = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '/' || r == '&' || r == '-' || unicode.IsSpace(r) {
			return r
		}
		return ' '
	}, text)
	raw := tokenRe.FindAllString(text, -1)
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		t = strings.Trim(t, ".-/&")
		if len(t) < 2 || stopWords[t] {
			continue
		}
		out = append(out, t)
	}
	return out
}

var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "with": true, "that": true, "this": true, "from": true, "into": true,
	"what": true, "when": true, "where": true, "which": true, "who": true, "why": true, "how": true,
	"are": true, "was": true, "were": true, "has": true, "have": true, "had": true, "does": true, "did": true,
	"can": true, "could": true, "should": true, "would": true, "about": true, "between": true, "under": true,
}

func hashIndex(value string, dim int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	return int(h.Sum32() % uint32(dim))
}

func l2Normalize(vec []float32) {
	var norm float32
	for _, v := range vec {
		norm += v * v
	}
	if norm > 0 {
		root := float32(math.Sqrt(float64(norm)))
		for i := range vec {
			vec[i] /= root
		}
	}
}

func charNgrams(token string, n int) []string {
	r := []rune(token)
	if len(r) < n {
		return nil
	}
	out := make([]string, 0, len(r)-n+1)
	for i := 0; i <= len(r)-n; i++ {
		out = append(out, string(r[i:i+n]))
	}
	return out
}

func ngrams(tokens []string, n int) []string {
	if len(tokens) < n || n <= 0 {
		return nil
	}
	out := make([]string, 0, len(tokens)-n+1)
	for i := 0; i <= len(tokens)-n; i++ {
		out = append(out, strings.Join(tokens[i:i+n], "_"))
	}
	return out
}

func makeSet(tokens []string) map[string]bool {
	out := map[string]bool{}
	for _, t := range tokens {
		if t != "" {
			out[t] = true
		}
	}
	return out
}

func overlapRatio(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	matches := 0
	for t := range a {
		if b[t] {
			matches++
		}
	}
	return float64(matches) / float64(len(a))
}

func exactPhraseScore(queryTokens []string, doc string) float64 {
	if len(queryTokens) == 0 {
		return 0
	}
	phrases := []string{strings.Join(queryTokens, " ")}
	if len(queryTokens) > 3 {
		phrases = append(phrases, strings.Join(queryTokens[:3], " "))
	}
	if len(queryTokens) > 5 {
		phrases = append(phrases, strings.Join(queryTokens[len(queryTokens)-3:], " "))
	}
	best := 0.0
	for _, p := range phrases {
		if strings.Contains(doc, p) {
			s := float64(len(strings.Fields(p))) / float64(len(queryTokens))
			if s > best {
				best = s
			}
		}
	}
	return best
}

func proximityScore(queryTokens, docTokens []string) float64 {
	positions := map[string][]int{}
	for i, t := range docTokens {
		positions[t] = append(positions[t], i)
	}
	matched := []int{}
	for _, q := range queryTokens {
		if pos := positions[q]; len(pos) > 0 {
			matched = append(matched, pos[0])
		}
	}
	if len(matched) < 2 {
		return 0
	}
	sort.Ints(matched)
	span := matched[len(matched)-1] - matched[0] + 1
	ideal := len(matched)
	if span <= ideal {
		return 1
	}
	return math.Max(0, 1-float64(span-ideal)/80.0)
}
