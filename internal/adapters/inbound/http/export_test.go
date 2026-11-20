package http

import (
	"net/http"
)

// ProblemEntry exposes the production catalogue to the replay-class test.
type ProblemEntry struct {
	Slug   string
	Status int
	Class  string
}

// Catalogue lists every problem type of the admin API with its declared class.
func Catalogue() []ProblemEntry {
	var out []ProblemEntry
	for _, p := range allProblems() {
		out = append(out, ProblemEntry{Slug: p.slug, Status: p.status, Class: string(p.class)})
	}
	return out
}

// Refuse answers slug through the production writer, so the class marking runs.
func Refuse(w http.ResponseWriter, r *http.Request, slug string) {
	for _, p := range allProblems() {
		if p.slug == slug {
			writeProblem(w, r, p, "refused")
			return
		}
	}
	panic("no problem type " + slug)
}

// KeyRows reports how many stored answers the middleware holds for key (0 or 1).
func (m *Idempotency) KeyRows(key string) int { return m.store.count(key) }
