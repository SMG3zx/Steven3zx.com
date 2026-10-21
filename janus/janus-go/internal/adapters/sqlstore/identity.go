package sqlstore

import "context"

// AddOwner records a User as an Owner, so the records it owns can refer to it
// and go with it when the Account is deleted. The User's email and sign-in
// methods live in the identity service; the row here keeps only the ID.
func (s *Store) AddOwner(ctx context.Context, id string) error {
	ctx, done := s.start(ctx, "AddOwner")
	_, err := s.db.ExecContext(ctx, `INSERT INTO users(id) VALUES(?) ON CONFLICT(id) DO NOTHING`, id)
	return done(err)
}
