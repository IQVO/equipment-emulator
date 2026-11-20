package http

import (
	"fmt"
	"net/http"

	"github.com/IQVO/equipment-emulator/internal/adapters/inbound/http/page"
)

// pageParams reads the query of a collection GET (copied from pricing): it answers 400
// unknown-query-parameter for any parameter that is not limit or cursor, invalid-limit for a
// limit outside 1..500 and invalid-cursor for a cursor that does not decode. It returns the
// limit and the id the page starts strictly after ("" for the first page), or false after
// writing the problem.
//
// The list is ordered by serialNumber, which is unique: the keyset is that serial as both sort
// key and id, so the cursor is the opaque page.Position of the last returned robot.
func pageParams(w http.ResponseWriter, r *http.Request, filters ...string) (limit int, after string, ok bool) {
	if name := page.Unknown(r, filters...); name != "" {
		writeProblem(w, r, unknownQueryParameter, fmt.Sprintf("query parameter %q is not supported; page with limit and cursor", name))
		return 0, "", false
	}
	limit, err := page.Limit(r)
	if err != nil {
		writeProblem(w, r, invalidLimit, err.Error())
		return 0, "", false
	}
	pos, err := page.After(r)
	if err == nil && pos != nil && pos.Sort != pos.ID {
		err = page.ErrInvalidCursor
	}
	if err != nil {
		writeProblem(w, r, invalidCursor, err.Error())
		return 0, "", false
	}
	if pos != nil {
		after = pos.ID
	}
	return limit, after, true
}
