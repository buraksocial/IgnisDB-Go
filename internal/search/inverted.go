// Package search implements a full-text search engine with inverted index
// and TF-IDF scoring.
package search

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Common English stopwords.
var stopwords = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true, "but": true,
	"in": true, "on": true, "at": true, "to": true, "for": true, "of": true,
	"with": true, "by": true, "from": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "have": true,
	"has": true, "had": true, "do": true, "does": true, "did": true,
	"will": true, "would": true, "could": true, "should": true, "may": true,
	"might": true, "shall": true, "can": true, "need": true, "dare": true,
	"it": true, "its": true, "this": true, "that": true, "these": true,
	"those": true, "i": true, "you": true, "he": true, "she": true,
	"we": true, "they": true, "me": true, "him": true, "her": true,
	"us": true, "them": true, "my": true, "your": true, "his": true,
}

var tokenRegex = regexp.MustCompile(`\b[a-z0-9]+\b`)

// SearchResult represents a search result with relevance score.
type SearchResult struct {
	DocID string
	Score float64
}

// InvertedIndex is a full-text search engine with inverted index.
type InvertedIndex struct {
	index         map[string]map[string]int // term -> {doc_id: frequency}
	docLengths    map[string]int            // doc_id -> total terms
	DocCount      int
	useStopwords  bool
	minTermLength int
	mu            sync.RWMutex
}

// NewInvertedIndex creates a new inverted index.
func NewInvertedIndex() *InvertedIndex {
	return &InvertedIndex{
		index:         make(map[string]map[string]int),
		docLengths:    make(map[string]int),
		useStopwords:  true,
		minTermLength: 2,
	}
}

// AddDocument adds or updates a document in the index.
func (idx *InvertedIndex) AddDocument(docID, text string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	tokens := idx.tokenize(text)

	// Remove old document if updating
	if _, exists := idx.docLengths[docID]; exists {
		idx.removeDocumentLocked(docID)
	}

	idx.docLengths[docID] = len(tokens)
	idx.DocCount++

	// Count term frequencies
	termFreq := make(map[string]int)
	for _, token := range tokens {
		termFreq[token]++
	}

	for term, freq := range termFreq {
		if idx.index[term] == nil {
			idx.index[term] = make(map[string]int)
		}
		idx.index[term][docID] = freq
	}
}

// RemoveDocument removes a document from the index.
func (idx *InvertedIndex) RemoveDocument(docID string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.removeDocumentLocked(docID)
}

func (idx *InvertedIndex) removeDocumentLocked(docID string) {
	var termsToRemove []string
	for term, docs := range idx.index {
		if _, ok := docs[docID]; ok {
			delete(docs, docID)
			if len(docs) == 0 {
				termsToRemove = append(termsToRemove, term)
			}
		}
	}
	for _, term := range termsToRemove {
		delete(idx.index, term)
	}
	delete(idx.docLengths, docID)
	idx.DocCount--
}

// Search searches for documents matching the query.
// Returns results sorted by relevance (TF-IDF).
func (idx *InvertedIndex) Search(query string, limit int) []SearchResult {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	queryTerms := idx.tokenize(query)
	if len(queryTerms) == 0 {
		return nil
	}

	scores := make(map[string]float64)
	N := float64(max(1, idx.DocCount))

	for _, term := range queryTerms {
		postingList, ok := idx.index[term]
		if !ok {
			continue
		}

		df := float64(len(postingList))
		idf := math.Log(N / df)

		for docID, tf := range postingList {
			docLen := float64(max(1, idx.docLengths[docID]))
			scores[docID] += (float64(tf) / docLen) * idf
		}
	}

	// Sort by score descending
	results := make([]SearchResult, 0, len(scores))
	for docID, score := range scores {
		results = append(results, SearchResult{DocID: docID, Score: score})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if limit > 0 && len(results) > limit {
		results = results[:limit]
	}
	return results
}

// tokenize tokenizes text into terms.
func (idx *InvertedIndex) tokenize(text string) []string {
	text = strings.ToLower(text)
	matches := tokenRegex.FindAllString(text, -1)

	filtered := make([]string, 0, len(matches))
	for _, token := range matches {
		if len(token) < idx.minTermLength {
			continue
		}
		if idx.useStopwords && stopwords[token] {
			continue
		}
		filtered = append(filtered, token)
	}
	return filtered
}

// GetTerms returns all terms for a document (for debugging).
func (idx *InvertedIndex) GetTerms(docID string) []string {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	var terms []string
	for term, docs := range idx.index {
		if _, ok := docs[docID]; ok {
			terms = append(terms, term)
		}
	}
	return terms
}

// Stats returns index statistics.
func (idx *InvertedIndex) Stats() map[string]interface{} {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	avgDocLen := 0.0
	if idx.DocCount > 0 {
		total := 0
		for _, l := range idx.docLengths {
			total += l
		}
		avgDocLen = float64(total) / float64(idx.DocCount)
	}

	return map[string]interface{}{
		"document_count": idx.DocCount,
		"unique_terms":   len(idx.index),
		"avg_doc_length": avgDocLen,
	}
}

// Clear clears the entire index.
func (idx *InvertedIndex) Clear() {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	idx.index = make(map[string]map[string]int)
	idx.docLengths = make(map[string]int)
	idx.DocCount = 0
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
