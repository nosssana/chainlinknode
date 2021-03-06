package terratest

import (
	"testing"

	"github.com/smartcontractkit/sqlx"
	"github.com/stretchr/testify/require"

	"github.com/smartcontractkit/chainlink-terra/pkg/terra/db"
)

// MustInsertChain inserts chain in to db, or fails the test.
func MustInsertChain(t testing.TB, db *sqlx.DB, chain *db.Chain) {
	query, args, e := db.BindNamed(`
INSERT INTO terra_chains (id, cfg, enabled, created_at, updated_at) VALUES (:id, :cfg, :enabled, NOW(), NOW()) RETURNING *;`, chain)
	require.NoError(t, e)
	err := db.Get(chain, query, args...)
	require.NoError(t, err)
}
