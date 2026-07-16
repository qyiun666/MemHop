package testsupport

import "github.com/qyiun666/memhop/memhop"

// Search opens a MemHop database, runs a search with the given text,
// and returns the result.
func Search(text string) (*memhop.SearchResult, error) {
	return SearchWith(memhop.SearchQuery{Text: text})
}

// SearchWith opens a MemHop database, runs a search with the given query,
// and returns the result.
func SearchWith(q memhop.SearchQuery) (*memhop.SearchResult, error) {
	mh := OpenMemHop()
	defer mh.Close()
	return mh.Search(q)
}
