package vector

import (
	"fmt"

	"gorm.io/gorm"
)

// Resolved is one consistent snapshot of the embedding account as the runtime and
// the upgrade migration see it. Every field comes from a single SQL statement (the
// account joined with its model mapping), so a concurrent account update can only
// produce a complete old or a complete new generation, never a mix of the two.
type Resolved struct {
	Identity string // "<id>|<base url>|<upstream model>", or "<id>:<request model>" when the account does not resolve; "" when unconfigured
	Exists   bool
	Type     string
	Enabled  bool
	BaseURL  string
	KeyEnc   string // the encrypted API key of the same snapshot
	Upstream string // request model (or the account's test model when empty) after the account's mapping
	Live     bool   // the account exists, is an embedding account and is enabled
}

// Identity resolves the embedding account in one statement. It is the single rule for
// "which embedding does this configuration use": the engines stamp it on every stored
// vector and compare it on reload, and the migration uses it to decide which of two
// duplicate vectors the runtime can load.
//
// The lookup uses plain SQL so it also works on a database that predates some columns
// or tables (the migration runs before AutoMigrate); a missing table or column means
// "not resolvable", a query error is returned.
func Identity(db *gorm.DB, accountID uint, requestModel string) (Resolved, error) {
	if accountID == 0 {
		return Resolved{}, nil
	}
	fallback := Resolved{Identity: fmt.Sprintf("%d:%s", accountID, requestModel)}
	m := db.Migrator()
	if !m.HasTable("accounts") {
		return fallback, nil
	}
	hasTest := m.HasColumn("accounts", "test_model")
	hasMap := m.HasTable("model_mappings")
	testExpr := "''"
	if hasTest {
		testExpr = "a.test_model"
	}
	// The requested model (or the account's test model when empty) is resolved in the
	// join condition, so the mapping row belongs to the same snapshot as the account.
	sql := "SELECT a.base_url AS base_url, a.type AS type, a.enabled AS enabled, a.api_key_enc AS key_enc, " + testExpr + " AS test_model"
	args := []any{}
	if hasMap {
		sql += ", COALESCE(m.upstream_model, '') AS mapped FROM accounts a LEFT JOIN model_mappings m ON m.account_id = a.id AND m.request_model = CASE WHEN ? = '' THEN " + testExpr + " ELSE ? END"
		args = append(args, requestModel, requestModel)
	} else {
		sql += ", '' AS mapped FROM accounts a"
	}
	sql += " WHERE a.id = ?"
	args = append(args, accountID)
	var row struct {
		BaseURL   string
		Type      string
		Enabled   bool
		KeyEnc    string
		TestModel string
		Mapped    string
	}
	res := db.Raw(sql, args...).Scan(&row)
	if res.Error != nil {
		return Resolved{}, res.Error
	}
	if res.RowsAffected == 0 {
		return fallback, nil
	}
	out := Resolved{Exists: true, Type: row.Type, Enabled: row.Enabled, BaseURL: row.BaseURL, KeyEnc: row.KeyEnc, Identity: fallback.Identity}
	if row.Type != "embedding" || !row.Enabled {
		return out, nil
	}
	upstream := requestModel
	if upstream == "" {
		upstream = row.TestModel
	}
	if row.Mapped != "" {
		upstream = row.Mapped
	}
	out.Upstream = upstream
	out.Live = true
	out.Identity = fmt.Sprintf("%d|%s|%s", accountID, row.BaseURL, upstream)
	return out, nil
}

// Compatible reports whether a stored vector identity can be loaded under the current
// one: the engines accept an exact match and, for backward compatibility, vectors
// stored without an identity.
func Compatible(stored, current string) bool {
	return stored == "" || current == "" || stored == current
}
