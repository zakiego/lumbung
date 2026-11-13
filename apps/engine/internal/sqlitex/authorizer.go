package sqlitex

import (
	"strings"

	"github.com/ncruces/go-sqlite3"
)

// pragmaRule says how a pragma may be used from user SQL.
type pragmaRule int

const (
	// pragmaQuery: may run, with or without an argument. Covers pragmas whose
	// argument names a table or index (table_info) and ones that only read.
	pragmaQuery pragmaRule = iota + 1
	// pragmaRead: may only be read. An assignment is denied.
	pragmaRead
	// pragmaReadWrite: may be read or set. Setting writes the database file,
	// so a read-only connection still refuses it.
	pragmaReadWrite
	// pragmaNoEffect: may be set, but user statements always run inside a
	// transaction the engine opened, where SQLite ignores these, and the
	// engine restores the setting after each request. Allowed so migrations
	// that toggle them (as drizzle-kit's do) keep working.
	pragmaNoEffect
)

// pragmas is the allowlist for user SQL. Anything missing is denied. It
// covers both PRAGMA statements and the pragma_* table-valued functions,
// which SQLite authorizes the same way. Documented in the Security model page.
var pragmas = map[string]pragmaRule{
	// Schema introspection.
	"table_info":        pragmaQuery,
	"table_xinfo":       pragmaQuery,
	"table_list":        pragmaQuery,
	"index_list":        pragmaQuery,
	"index_info":        pragmaQuery,
	"index_xinfo":       pragmaQuery,
	"foreign_key_list":  pragmaQuery,
	"foreign_key_check": pragmaQuery,
	"collation_list":    pragmaQuery,
	"function_list":     pragmaQuery,
	"module_list":       pragmaQuery,
	"pragma_list":       pragmaQuery,
	"compile_options":   pragmaQuery,
	"integrity_check":   pragmaQuery,
	"quick_check":       pragmaQuery,

	// Read-only state.
	"schema_version": pragmaRead,
	"data_version":   pragmaRead,
	"page_count":     pragmaRead,
	"page_size":      pragmaRead,
	"freelist_count": pragmaRead,
	"max_page_count": pragmaRead,
	"journal_mode":   pragmaRead,
	"encoding":       pragmaRead,
	"auto_vacuum":    pragmaRead,

	// Header fields an application owns.
	"user_version":   pragmaReadWrite,
	"application_id": pragmaReadWrite,

	// Transaction-scoped or ignored inside a transaction.
	"foreign_keys":       pragmaNoEffect,
	"defer_foreign_keys": pragmaNoEffect,
}

// deniedFunctions are refused by name even if a build happens to include
// them. load_extension isn't compiled into the driver today; this keeps it
// out if that ever changes.
var deniedFunctions = map[string]bool{
	"load_extension": true,
	"fts3_tokenizer": true,
	"readfile":       true,
	"writefile":      true,
	"edit":           true,
}

// allowedModules are the virtual table modules users may create.
var allowedModules = map[string]bool{
	"fts5":      true,
	"fts5vocab": true,
}

// authorize is the authorizer for user SQL. It runs when a statement is
// prepared, including statements that expand views and triggers.
func authorize(action sqlite3.AuthorizerActionCode, arg3, arg4, _, _ string) sqlite3.AuthorizerReturnCode {
	switch action {
	case sqlite3.AUTH_ATTACH, sqlite3.AUTH_DETACH:
		// VACUUM INTO also arrives here, as an attach of its target file.
		return sqlite3.AUTH_DENY

	case sqlite3.AUTH_TRANSACTION, sqlite3.AUTH_SAVEPOINT:
		// The engine owns transactions: query and batch each run in one.
		return sqlite3.AUTH_DENY

	case sqlite3.AUTH_CREATE_TEMP_INDEX, sqlite3.AUTH_CREATE_TEMP_TABLE,
		sqlite3.AUTH_CREATE_TEMP_TRIGGER, sqlite3.AUTH_CREATE_TEMP_VIEW:
		// Temp objects would outlive the request on a pooled connection and
		// leak into the next one.
		return sqlite3.AUTH_DENY

	case sqlite3.AUTH_PRAGMA:
		return authorizePragma(arg3, arg4)

	case sqlite3.AUTH_FUNCTION:
		if deniedFunctions[strings.ToLower(arg4)] {
			return sqlite3.AUTH_DENY
		}
		return sqlite3.AUTH_OK

	case sqlite3.AUTH_CREATE_VTABLE, sqlite3.AUTH_DROP_VTABLE:
		if allowedModules[strings.ToLower(arg4)] {
			return sqlite3.AUTH_OK
		}
		return sqlite3.AUTH_DENY
	}
	return sqlite3.AUTH_OK
}

func authorizePragma(name, arg string) sqlite3.AuthorizerReturnCode {
	// A schema prefix (PRAGMA main.user_version) arrives separately; the name
	// itself has no dot. Lower-case it, since SQLite matches pragmas that way.
	switch pragmas[strings.ToLower(name)] {
	case pragmaQuery, pragmaReadWrite, pragmaNoEffect:
		return sqlite3.AUTH_OK
	case pragmaRead:
		if arg == "" {
			return sqlite3.AUTH_OK
		}
	}
	return sqlite3.AUTH_DENY
}
