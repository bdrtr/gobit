package link

import "github.com/bdrtr/gobit/internal/core/errors"

// DefineForTest writes the definition ONLY into the in-process registry; it
// does not touch the database and creates no table.
//
// It is for tests only (this file is not part of a production build). It
// exists so the paths that need no database — validation order, the undeclared
// link gate, the empty-set short circuit — can be exercised without Docker;
// the real Define behavior is verified in the integration tests.
func DefineForTest(svc LinkService, def LinkDefinition) error {
	s, ok := svc.(*service)
	if !ok {
		return errors.Internal("link_test_helper", "the concrete type is not the expected *service")
	}
	if err := def.Validate(); err != nil {
		return err
	}
	lt, err := newLinkTable(def)
	if err != nil {
		return err
	}
	s.defs.put(lt)
	return nil
}
