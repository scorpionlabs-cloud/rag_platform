package ingestion

import "strings"

type PageText struct {
	Page      int
	Text      string
	ImagePath string
	ObjectKey string
	ObjectURL string
}

type TextChunk struct {
	Page       int
	ChunkIndex int
	Text       string
	ImagePath  string
	ObjectKey  string
	ObjectURL  string
}

type Chunker struct {
	MaxChars     int
	OverlapChars int
	MinChars     int
}

func NewChunker() Chunker { return NewChunkerWithOptions(1200, 180) }

func NewChunkerWithOptions(maxChars, overlapChars int) Chunker {
	if maxChars <= 0 {
		maxChars = 1200
	}
	if overlapChars < 0 {
		overlapChars = 0
	}
	if overlapChars >= maxChars/2 {
		overlapChars = maxChars / 5
	}
	return Chunker{MaxChars: maxChars, OverlapChars: overlapChars, MinChars: 40}
}

func (c Chunker) Chunk(text string) []string {
	chunks := c.ChunkPages([]PageText{{Page: 1, Text: text}})
	out := make([]string, 0, len(chunks))
	for _, ch := range chunks {
		out = append(out, ch.Text)
	}
	return out
}

func (c Chunker) ChunkPages(pages []PageText) []TextChunk {
	var chunks []TextChunk
	chunkIndex := 0
	for _, page := range pages {
		pageNum := page.Page
		if pageNum <= 0 {
			pageNum = 1
		}
		parts := splitIntoPassages(page.Text)
		var current string
		flush := func() {
			current = strings.TrimSpace(current)
			if len(current) < c.MinChars {
				current = ""
				return
			}
			chunkIndex++
			chunks = append(chunks, TextChunk{Page: pageNum, ChunkIndex: chunkIndex, Text: current, ImagePath: page.ImagePath, ObjectKey: page.ObjectKey, ObjectURL: page.ObjectURL})
			current = tailOverlap(current, c.OverlapChars)
		}
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if len(part) > c.MaxChars {
				if strings.TrimSpace(current) != "" {
					flush()
				}
				for _, hard := range splitHard(part, c.MaxChars, c.OverlapChars) {
					if len(strings.TrimSpace(hard)) >= c.MinChars {
						chunkIndex++
						chunks = append(chunks, TextChunk{Page: pageNum, ChunkIndex: chunkIndex, Text: strings.TrimSpace(hard), ImagePath: page.ImagePath, ObjectKey: page.ObjectKey, ObjectURL: page.ObjectURL})
					}
				}
				current = ""
				continue
			}
			if len(current)+len(part)+2 > c.MaxChars {
				flush()
			}
			if current != "" {
				current += "\n"
			}
			current += part
		}
		if strings.TrimSpace(current) != "" {
			current = strings.TrimSpace(current)
			if len(current) >= c.MinChars {
				chunkIndex++
				chunks = append(chunks, TextChunk{Page: pageNum, ChunkIndex: chunkIndex, Text: current, ImagePath: page.ImagePath, ObjectKey: page.ObjectKey, ObjectURL: page.ObjectURL})
			}
		}
	}
	return chunks
}

func splitIntoPassages(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	paragraphs := strings.Split(text, "\n\n")
	var out []string
	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if len(p) < 300 {
			out = append(out, p)
			continue
		}
		out = append(out, splitSentences(p)...)
	}
	return out
}

func splitSentences(text string) []string {
	var out []string
	start := 0
	r := []rune(text)
	for i, ch := range r {
		if ch == '.' || ch == '?' || ch == '!' || ch == ';' {
			seg := strings.TrimSpace(string(r[start : i+1]))
			if seg != "" {
				out = append(out, seg)
			}
			start = i + 1
		}
	}
	if start < len(r) {
		seg := strings.TrimSpace(string(r[start:]))
		if seg != "" {
			out = append(out, seg)
		}
	}
	return out
}

func splitHard(text string, maxChars, overlap int) []string {
	r := []rune(text)
	if len(r) <= maxChars {
		return []string{text}
	}
	var out []string
	step := maxChars - overlap
	if step <= 0 {
		step = maxChars
	}
	for start := 0; start < len(r); start += step {
		end := start + maxChars
		if end > len(r) {
			end = len(r)
		}
		out = append(out, string(r[start:end]))
		if end == len(r) {
			break
		}
	}
	return out
}

func tailOverlap(text string, overlap int) string {
	if overlap <= 0 {
		return ""
	}
	r := []rune(text)
	if len(r) <= overlap {
		return string(r)
	}
	return strings.TrimSpace(string(r[len(r)-overlap:]))
}
