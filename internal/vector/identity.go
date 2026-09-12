package vector

import (
	"fmt"

	"gorm.io/gorm"
)

// Identity is the single rule for "which embedding does this configuration use". It is
// what the engines stamp on every stored vector and compare on reload, and what the
// upgrade migration uses to decide which of two duplicate vectors the runtime can load.
//
// With the account readable, of embedding type and enabled it is
// "<account id>|<base url>|<upstream model>", the upstream model being the request
// model (or the account's test model when empty) after the account's model mapping.
// Otherwise it degrades to "<account id>:<request model>"; an unconfigured vector
// service (account 0) has the empty identity.
//
// The lookups use plain SQL so the resolver also works on a database that predates
// some columns or tables (the migration runs before AutoMigrate); missing tables or
// columns mean "not resolvable", a query error is returned.
type Resolved struct {
	Identity string
	BaseURL  string
	Upstream string
	Live     bool // the account resolved: BaseURL / Upstream are meaningful
}

func Identity(db *gorm.DB, accountID uint, requestModel string) (Resolved, error) {
	if accountID == 0 {
		return Resolved{}, nil
	}
	fallback := Resolved{Identity: fmt.Sprintf("%d:%s", accountID, requestModel)}
	m := db.Migrator()
	if !m.HasTable("accounts") {
		return fallback, nil
	}
	cols := "base_url, type, enabled"
	hasTest := m.HasColumn("accounts", "test_model")
	if hasTest {
		cols += ", test_model"
	}
	var acc struct {
		BaseURL   string
		Type      string
		Enabled   bool
		TestModel string
	}
	res := db.Raw("SELECT "+cols+" FROM accounts WHERE id = ?", accountID).Scan(&acc)
	if res.Error != nil {
		return Resolved{}, res.Error
	}
	if res.RowsAffected == 0 || acc.Type != "embedding" || !acc.Enabled {
		return fallback, nil
	}
	upstream := requestModel
	if upstream == "" {
		upstream = acc.TestModel
	}
	if m.HasTable("model_mappings") {
		var mm struct{ UpstreamModel string }
		r := db.Raw("SELECT upstream_model FROM model_mappings WHERE account_id = ? AND request_model = ?", accountID, upstream).Scan(&mm)
		if r.Error != nil {
			return Resolved{}, r.Error
		}
		if r.RowsAffected > 0 && mm.UpstreamModel != "" {
			upstream = mm.UpstreamModel
		}
	}
	return Resolved{Identity: fmt.Sprintf("%d|%s|%s", accountID, acc.BaseURL, upstream), BaseURL: acc.BaseURL, Upstream: upstream, Live: true}, nil
}

// Compatible reports whether a stored vector identity can be loaded under the current
// one: the engines accept an exact match and, for backward compatibility, vectors
// stored without an identity.
func Compatible(stored, current string) bool {
	return stored == "" || current == "" || stored == current
}
