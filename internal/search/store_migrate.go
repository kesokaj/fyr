package search

import "database/sql"

func (s *store) migrateV9ToV10() error {
	return s.withImmediateTx(s.migrateV9ToV10Tx)
}

func (s *store) migrateV10ToV11() error {
	return s.withImmediateTx(s.migrateV10ToV11Tx)
}

func (s *store) migrateV11ToV12() error {
	return s.withImmediateTx(s.migrateV11ToV12Tx)
}

const chunksDeleteTriggerSQL = `CREATE TRIGGER chunks_ad AFTER DELETE ON chunks BEGIN
  INSERT INTO chunks_fts(chunks_fts, rowid, heading, body, tokens) VALUES ('delete', old.id, old.heading, old.body, old.tokens);
END;`

func (s *store) migrateV9ToV10Tx() error {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'schema'`).Scan(&v); err != nil {
		return err
	}
	if v == schemaVersion || v == "10" {
		return s.ensureRefsOIDIndex()
	}
	if v != "9" {
		return staleSchemaError{v}
	}
	if err := ensureColumnDefaultZero(s.db, "blobs", "line_count"); err != nil {
		return err
	}
	stmts := []string{
		`DROP TRIGGER IF EXISTS chunks_ad`,
		`INSERT INTO chunks_fts(chunks_fts) VALUES ('delete-all')`,
		`DELETE FROM chunks`,
		chunksDeleteTriggerSQL,
		`DELETE FROM refs`, `DELETE FROM symbols`, `DELETE FROM sym_refs`, `DELETE FROM files`, `DELETE FROM blobs`,
		`DELETE FROM meta WHERE key LIKE 'touched:%'`,
	}
	for _, st := range stmts {
		if _, err := s.db.Exec(st); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(`UPDATE meta SET value = '10' WHERE key = 'schema'`); err != nil {
		return err
	}
	return s.ensureRefsOIDIndex()
}

func (s *store) migrateV10ToV11Tx() error {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'schema'`).Scan(&v); err != nil {
		return err
	}
	if v == schemaVersion || v == "11" {
		return s.ensureRefsOIDIndex()
	}
	if v != "10" {
		return staleSchemaError{v}
	}
	for _, col := range []string{"duration_ms", "timed_out", "injected_bytes"} {
		if err := ensureColumnDefaultZero(s.db, "vet_history", col); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(`UPDATE meta SET value = '11' WHERE key = 'schema'`); err != nil {
		return err
	}
	return s.ensureRefsOIDIndex()
}

func (s *store) migrateV11ToV12Tx() error {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key = 'schema'`).Scan(&v); err != nil {
		return err
	}
	if v == schemaVersion {
		return s.ensureRefsOIDIndex()
	}
	if v != "11" {
		return staleSchemaError{v}
	}
	for _, col := range []string{"last_invocation_num", "last_checkpoint_offset"} {
		if err := ensureColumnDefaultZero(s.db, "session_meta", col); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(`UPDATE meta SET value = ? WHERE key = 'schema'`, schemaVersion); err != nil {
		return err
	}
	return s.ensureRefsOIDIndex()
}

func ensureColumnDefaultZero(db *sql.DB, table, col string) error {
	has, err := tableHasColumn(db, table, col)
	if err != nil || has {
		return err
	}
	_, err = db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + col + ` INT DEFAULT 0`)
	return err
}

func tableHasColumn(db *sql.DB, table, col string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == col {
			return true, nil
		}
	}
	return false, rows.Err()
}
